package emit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/importers/swebench"
	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Input is one validated candidate with its environment and a checkout of
// the repository at the base commit. Emit scrubs RepoDir in place.
type Input struct {
	Validation issuebench.Validation `json:"validation"`
	Env        issuebench.EnvSpec    `json:"env"`
	RepoDir    string                `json:"repo_dir"`
}

// Queuer queues a Heimdall job (suite.lint, suite.lock, suite.admit) and
// returns its id. The server's job registry implements it.
type Queuer interface {
	Queue(ctx context.Context, kind string, params any) (string, error)
}

// Options shape one emit.
type Options struct {
	BenchRoot     string // project folder
	BenchmarksDir string // resolved benchmarks root; empty means BenchRoot/benchmarks
	WorkDir       string // scratch folder for rows and image build contexts
	HeldOut       HeldOutPlan
	Force         bool   // replace task folders that already exist
	Readiness     Queuer // nil: readiness is left for the caller
	SelfContained bool   // hosted: each task carries environment/Dockerfile and repo.tar.gz, no image
	Logger        schemas.Logger
}

// Suite is what Emit wrote for one repository.
type Suite struct {
	Repo      string            `json:"repo"`
	Suite     string            `json:"suite"`
	SuiteDir  string            `json:"suite_dir"`
	Tasks     []string          `json:"tasks"`
	HeldOut   []string          `json:"held_out"`
	Skipped   []importers.Skip  `json:"skipped"`
	Images    map[string]Layers `json:"images"`     // task id -> layers
	BuildDirs map[string]string `json:"build_dirs"` // task id -> image build context
	Jobs      []string          `json:"jobs,omitempty"`
}

// Report is the outcome of one Emit.
type Report struct {
	Suites []Suite `json:"suites"`
}

// readinessKinds run in this order: lint the files, lock the hashes, then
// admit, which runs the reference three times, the empty and exploit
// controls, the wrong patches as mutants, with the grader offline.
var readinessKinds = []string{"suite.lint", "suite.lock", "suite.admit"}

// Emit writes the inputs as one private benchmark per repository.
func Emit(ctx context.Context, in []Input, opts Options) (*Report, error) {
	if opts.Logger == nil {
		opts.Logger = schemas.NoopLogger{}
	}
	if opts.BenchRoot == "" || opts.WorkDir == "" {
		return nil, errors.New("emit: BenchRoot and WorkDir are required")
	}
	if opts.HeldOut == (HeldOutPlan{}) {
		opts.HeldOut = DefaultHeldOut
	}
	im, ok := importers.Get(swebench.Format)
	if !ok {
		return nil, errors.New("emit: swebench importer not registered")
	}
	byRepo := map[string][]Input{}
	for _, x := range in {
		byRepo[x.Validation.Candidate.Repo] = append(byRepo[x.Validation.Candidate.Repo], x)
	}
	repos := make([]string, 0, len(byRepo))
	for r := range byRepo {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	rep := &Report{}
	for _, repo := range repos {
		s, err := emitRepo(ctx, im, repo, byRepo[repo], opts)
		if err != nil {
			return rep, fmt.Errorf("emit %s: %w", repo, err)
		}
		rep.Suites = append(rep.Suites, *s)
	}
	return rep, nil
}

type prepared struct {
	in     Input
	id     string
	layers Layers
	build  string
}

func emitRepo(ctx context.Context, im importers.Importer, repo string, in []Input, opts Options) (*Suite, error) {
	suiteID := SuiteID(repo)
	out := &Suite{Repo: repo, Suite: suiteID, Images: map[string]Layers{}, BuildDirs: map[string]string{}}
	work := filepath.Join(opts.WorkDir, suiteID)
	if err := os.MkdirAll(work, 0o755); err != nil {
		return nil, err
	}
	var rows bytes.Buffer
	var ready []prepared
	var cands []issuebench.Candidate
	suiteLicense := ""
	for _, x := range in {
		id := InstanceID(x.Validation.Candidate)
		if why := Usable(x.Validation); why != "" {
			out.Skipped = append(out.Skipped, importers.Skip{ID: id, Reason: why})
			continue
		}
		if x.RepoDir == "" {
			out.Skipped = append(out.Skipped, importers.Skip{ID: id, Reason: "no checkout of the base commit"})
			continue
		}
		lic := DetectLicense(x.RepoDir)
		switch {
		case suiteLicense == "":
			suiteLicense = lic
		case suiteLicense != lic:
			suiteLicense = LicenseUnknown
		}
		base, err := ScrubGit(ctx, x.RepoDir)
		if err != nil {
			out.Skipped = append(out.Skipped, importers.Skip{ID: id, Reason: "scrub history: " + err.Error()})
			continue
		}
		layers, err := LayersOf(x.Env, id, base)
		if err != nil {
			out.Skipped = append(out.Skipped, importers.Skip{ID: id, Reason: err.Error()})
			continue
		}
		build, err := writeBuildContext(filepath.Join(work, "images", id), x.RepoDir, layers, x.Env.Language)
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(RowOf(x.Validation, x.Env, base, layers.TaskRef, lic))
		if err != nil {
			return nil, err
		}
		rows.Write(b)
		rows.WriteByte('\n')
		ready = append(ready, prepared{in: x, id: id, layers: layers, build: build})
		cands = append(cands, x.Validation.Candidate)
	}
	if len(ready) == 0 {
		return out, nil
	}
	rowsPath := filepath.Join(work, "rows.jsonl")
	if err := os.WriteFile(rowsPath, rows.Bytes(), 0o644); err != nil {
		return nil, err
	}
	iopts := importers.Options{
		BenchRoot:     opts.BenchRoot,
		BenchmarksDir: opts.BenchmarksDir,
		From:          rowsPath,
		SuiteID:       suiteID,
		Force:         opts.Force,
		Extra: map[string]string{
			"benchmark":      "issuebench",
			"workspace_path": Workspace,
			"license":        licenseOr(suiteLicense),
		},
		Logger: opts.Logger,
	}
	res, err := im.Import(ctx, iopts)
	if err != nil {
		return nil, err
	}
	out.SuiteDir = res.SuiteDir
	out.Skipped = append(out.Skipped, res.Skipped...)
	held := HeldOut(cands, opts.HeldOut)
	byInstance := map[string]prepared{}
	for _, p := range ready {
		byInstance[p.id] = p
	}
	for _, tid := range res.Tasks {
		dir := filepath.Join(res.SuiteDir, "tasks", tid)
		inst, err := taskTitle(dir)
		if err != nil {
			return nil, err
		}
		p, ok := byInstance[inst]
		if !ok {
			return nil, fmt.Errorf("task %s: no input for %s", tid, inst)
		}
		if err := finishTask(dir, p, held[inst]); err != nil {
			return nil, fmt.Errorf("task %s: %w", tid, err)
		}
		if opts.SelfContained {
			if err := SelfContain(dir, p.in.Env, p.in.RepoDir); err != nil {
				return nil, fmt.Errorf("task %s: %w", tid, err)
			}
		}
		out.Tasks = append(out.Tasks, tid)
		out.Images[tid] = p.layers
		out.BuildDirs[tid] = p.build
		if held[inst] {
			out.HeldOut = append(out.HeldOut, tid)
		}
	}
	if opts.Readiness != nil && len(out.Tasks) > 0 {
		for _, kind := range readinessKinds {
			id, err := opts.Readiness.Queue(ctx, kind, map[string]string{"suite": suiteID})
			if err != nil {
				return out, fmt.Errorf("queue %s: %w", kind, err)
			}
			out.Jobs = append(out.Jobs, id)
		}
	}
	opts.Logger.Info("issuebench emitted", "suite", suiteID, "tasks", len(out.Tasks), "held_out", len(out.HeldOut), "skipped", len(out.Skipped))
	return out, nil
}

// writeBuildContext lays out the three Dockerfiles beside a copy of the
// scrubbed repository: Dockerfile.base, Dockerfile.env, Dockerfile (task).
func writeBuildContext(dir, repoDir string, l Layers, language string) (string, error) {
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.Symlink(repoDir, filepath.Join(dir, "repo")); err != nil {
		return "", err
	}
	for name, body := range map[string]string{
		"Dockerfile.base": l.BaseDockerfile,
		"Dockerfile.env":  l.EnvDockerfile,
		"Dockerfile":      l.TaskDockerfile,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			return "", err
		}
	}
	// The language's test result converters go in heimdall/ beside the
	// repository (not inside it), for the image's /heimdall/ folder.
	if _, err := issuebench.WriteSupportFiles(dir, issuebench.LanguageFor(language)); err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(l, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "layers.json"), append(b, '\n'), 0o644); err != nil {
		return "", err
	}
	return dir, nil
}

func taskTitle(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "task.yaml"))
	if err != nil {
		return "", err
	}
	var d struct {
		Title string `yaml:"title"`
	}
	if err := yaml.Unmarshal(raw, &d); err != nil {
		return "", err
	}
	return d.Title, nil
}

// finishTask sets review: pending, the split, the issue date and the wrong
// patches as mutant controls on an imported task.
func finishTask(dir string, p prepared, heldOut bool) error {
	var mutants []string
	for i, patch := range p.in.Validation.Checks.WrongPatches {
		if strings.TrimSpace(patch) == "" {
			continue
		}
		rel := fmt.Sprintf("controls/wrong_%d.sh", i+1)
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(wrongScript(patch)), 0o755); err != nil {
			return err
		}
		mutants = append(mutants, rel)
	}
	path := filepath.Join(dir, "task.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return errors.New("task.yaml is not a mapping")
	}
	m := doc.Content[0]
	setKey(m, "review", str(schemas.ReviewPending))
	split := schemas.SplitPublic
	if heldOut {
		split = schemas.SplitHeldOut
	}
	setKey(m, "split", str(split))
	c := p.in.Validation.Candidate
	tags := getKey(m, "tags")
	if tags != nil && tags.Kind == yaml.SequenceNode {
		tags.Content = append(tags.Content, str("issuebench"), str(fmt.Sprintf("issue:%d", c.Issue)), str(fmt.Sprintf("pr:%d", c.PR)))
		if !c.IssueCreated.IsZero() {
			tags.Content = append(tags.Content, str("opened:"+c.IssueCreated.UTC().Format("2006-01-02")))
		}
	}
	if ctl := getKey(m, "controls"); ctl != nil && ctl.Kind == yaml.MappingNode && len(mutants) > 0 {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, mu := range mutants {
			seq.Content = append(seq.Content, str(mu))
		}
		setKey(ctl, "mutants", seq)
	}
	if prov := getKey(m, "provenance"); prov != nil && prov.Kind == yaml.MappingNode {
		// The org's own repository, not a public benchmark: no imported_from,
		// so the held-out split counts.
		delKey(prov, "imported_from")
		setKey(prov, "source", str(fmt.Sprintf("https://github.com/%s/issues/%d", c.Repo, c.Issue)))
		setKey(prov, "created_by", str("heimdall issuebench emit"))
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func str(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func getKey(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func delKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

func setKey(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, str(key), val)
}

// wrongScript applies a near-miss wrong patch; readiness expects it to fail.
func wrongScript(patch string) string {
	if !strings.HasSuffix(patch, "\n") {
		patch += "\n"
	}
	delim := "BENCH_WRONG_PATCH"
	for n := 0; strings.Contains(patch, delim); n++ {
		delim = fmt.Sprintf("BENCH_WRONG_PATCH_%d", n)
	}
	return "#!/bin/sh\n# Wrong patch control: a near miss that must fail the tests.\nset -e\n" +
		"git -c safe.directory='*' apply --whitespace=nowarn - <<'" + delim + "'\n" + patch + delim + "\n"
}

func licenseOr(l string) string {
	if l == "" {
		return LicenseUnknown
	}
	return l
}
