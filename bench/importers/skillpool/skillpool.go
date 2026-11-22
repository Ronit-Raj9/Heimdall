// Package skillpool imports the Skill-Usage setting (arXiv 2604.04323,
// https://github.com/UCSB-NLP-Chang/Skill-Usage): the tasks of an already
// imported Harbor suite (SkillsBench or Terminal-Bench 2.0) with the paper's
// alternative skill sets per task (curated, retrieved from the 34k pool with
// and without the curated skills) under skill-sets/<set>, a "pool" set that
// holds the paper's find-skills skill, and a suite service that serves BM25
// search over the 34k-skill pool. An experiment file compares no skills,
// curated, retrieved and pool arms through the agent config's skills.set.
package skillpool

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
)

const (
	Name      = "skillpool"
	sourceURL = "https://github.com/UCSB-NLP-Chang/Skill-Usage"
	hfURL     = "https://huggingface.co/datasets/Shiyu-Lab/Skill-Usage"
	// ServiceID is the host name of the pool search service in a run.
	ServiceID = "skills-pool"
	// ServicePort is upstream's search_server default port.
	ServicePort = 8742
	// ServiceImage is the tag the suite expects for the pool service.
	ServiceImage = "bench/skills-pool:1"
)

// sets maps the bench set name to the upstream folder per benchmark. The
// retrieved sets are the ones upstream retrieved with Claude Opus 4.6.
var sets = map[string]map[string]string{
	"skillsbench": {
		"curated":              "skillsbench/curated",
		"retrieved":            "skillsbench/retrieved_w_curated/claude",
		"retrieved-wo-curated": "skillsbench/retrieved_wo_curated/claude",
	},
	"terminalbench": {
		"retrieved": "terminalbench/retrieved/claude",
	},
}

func init() { importers.Register(&importer{}) }

type importer struct{}

// Info implements importers.Importer.
func (im *importer) Info() importers.Info {
	return importers.Info{
		Name:       Name,
		Benchmarks: []string{"skill-usage@03446d1 (skillsbench, terminalbench)"},
		Source:     "git clone " + sourceURL + " repo; download " + hfURL + " into hf/ (skills-34k, search_index, <bench>/<setting>/<task>/skills)",
		License:    "not stated upstream (no LICENSE file, no Hugging Face licence tag)",
		Track:      schemas.TrackSkills,
		Grading:    schemas.GradingVerified,
		Needs: []string{
			"an imported Harbor suite to extend (-set base=<suite folder>)",
			"the pool service image " + ServiceImage + " built from services/skills-pool",
		},
		Summary: "Skill-Usage: curated vs retrieved (34k pool) vs no skills on the same Harbor tasks",
	}
}

// Import implements importers.Importer. Settings: base (folder of the Harbor
// suite whose tasks are reused), benchmark (skillsbench or terminalbench).
// From is the dataset folder holding repo/ and hf/.
func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	base := opts.Extra["base"]
	bench := opts.Extra["benchmark"]
	for k := range opts.Extra {
		if k != "base" && k != "benchmark" {
			return nil, fmt.Errorf("skillpool: unknown setting %q (base, benchmark)", k)
		}
	}
	setDirs, ok := sets[bench]
	if !ok {
		return nil, fmt.Errorf("skillpool: -set benchmark=skillsbench or terminalbench")
	}
	if base == "" {
		return nil, fmt.Errorf("skillpool: -set base=<folder of the imported Harbor suite>")
	}
	raw, err := os.ReadFile(filepath.Join(base, "suite.yaml"))
	if err != nil {
		return nil, fmt.Errorf("skillpool: base suite: %w", err)
	}
	var suite map[string]any
	if err := yaml.Unmarshal(raw, &suite); err != nil {
		return nil, fmt.Errorf("skillpool: base suite.yaml: %w", err)
	}
	baseID, _ := suite["id"].(string)
	var tasks []string
	for _, t := range asList(suite["tasks"]) {
		if s, ok := t.(string); ok {
			tasks = append(tasks, s)
		}
	}
	if len(opts.IDs) > 0 {
		want := map[string]bool{}
		for _, id := range opts.IDs {
			want[strings.TrimSpace(id)] = true
		}
		var kept []string
		for _, t := range tasks {
			if want[t] {
				kept = append(kept, t)
			}
		}
		tasks = kept
	}
	if opts.Limit > 0 && len(tasks) > opts.Limit {
		tasks = tasks[:opts.Limit]
	}
	suiteDir := opts.SuiteDir()
	if _, err := os.Stat(filepath.Join(suiteDir, "suite.yaml")); err == nil && !opts.Force {
		return nil, fmt.Errorf("skillpool: %s exists (use -force)", suiteDir)
	}
	// Shared suite files (base image context and the like), not tasks/ and
	// not the lock, admissions or experiment files of the base suite.
	ents, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		n := e.Name()
		if n == "tasks" || n == "suite.yaml" || strings.HasPrefix(n, "suite.lock") || strings.HasPrefix(n, "experiment") || strings.HasPrefix(n, "admission") || strings.HasPrefix(n, ".") {
			continue
		}
		if err := copyTree(filepath.Join(base, n), filepath.Join(suiteDir, n)); err != nil {
			return nil, err
		}
	}
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: suiteDir}
	counts := map[string]int{}
	setNames := sortedKeys(setDirs)
	for _, t := range tasks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dst := filepath.Join(suiteDir, "tasks", t)
		if opts.Force {
			_ = os.RemoveAll(dst)
		}
		if err := copyTree(filepath.Join(base, "tasks", t), dst); err != nil {
			res.Skipped = append(res.Skipped, importers.Skip{ID: t, Reason: err.Error()})
			continue
		}
		for _, set := range setNames {
			src := filepath.Join(opts.From, "hf", filepath.FromSlash(setDirs[set]), t, "skills")
			if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
				continue
			}
			skills, err := os.ReadDir(src)
			if err != nil {
				return nil, err
			}
			for _, sk := range skills {
				if !sk.IsDir() {
					continue
				}
				if _, err := os.Stat(filepath.Join(src, sk.Name(), "SKILL.md")); err != nil {
					continue // incomplete download: a skill without SKILL.md would refuse the run
				}
				// Upstream names such as "benchflow-ai--FFmpeg Audio
				// Processing" are not valid skill folder names.
				if err := copyTree(filepath.Join(src, sk.Name()), filepath.Join(dst, "skill-sets", set, safeName(sk.Name()))); err != nil {
					return nil, err
				}
			}
			counts[set]++
		}
		if err := writePoolSkill(opts.From, filepath.Join(dst, "skill-sets", "pool", "finding-skills")); err != nil {
			return nil, err
		}
		res.Tasks = append(res.Tasks, t)
	}
	if err := writeService(opts.From, filepath.Join(suiteDir, "services", ServiceID)); err != nil {
		return nil, err
	}
	desc := fmt.Sprintf("Skill-Usage setting (arXiv 2604.04323, %s @03446d1, data %s) on the tasks of %s. "+
		"Each task keeps its curated skills and adds skill-sets/{%s,pool}; the agent config's skills.set picks one. "+
		"Service %s (http://%s:%d) serves BM25 keyword search and /detail over the 34k-skill pool; semantic and hybrid search are not served (Qwen3-Embedding-4B). "+
		"Licence: not stated upstream.", sourceURL, hfURL, baseID, strings.Join(setNames, ","), ServiceID, ServiceID, ServicePort)
	suite["id"] = opts.SuiteID
	suite["description"] = desc
	suite["tasks"] = res.Tasks
	suite["services"] = []map[string]any{{
		"id": ServiceID, "role": "data", "image": ServiceImage, "port": ServicePort, "proxy": false,
		"ready": map[string]any{"http": "/health", "timeout_s": 120},
	}}
	out, err := yaml.Marshal(suite)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(suiteDir, "suite.yaml"), out, 0o644); err != nil {
		return nil, err
	}
	version, _ := suite["version"].(string)
	if err := os.WriteFile(filepath.Join(suiteDir, "experiment-skills-pool.yaml"), []byte(experiment(opts.SuiteID, version, bench)), 0o644); err != nil {
		return nil, err
	}
	for _, s := range setNames {
		res.Notes = append(res.Notes, fmt.Sprintf("skill set %s: %d of %d tasks", s, counts[s], len(res.Tasks)))
	}
	res.Notes = append(res.Notes, "docker build -t "+ServiceImage+" "+filepath.Join(suiteDir, "services", ServiceID),
		"bifrost-bench suite lock "+opts.SuiteID, "bifrost-bench run "+filepath.Join(suiteDir, "experiment-skills-pool.yaml"))
	return res, importers.Finish(suiteDir)
}

func experiment(suite, version, bench string) string {
	arms := "  A: {label: no skills, set: {skills.enabled: false}}\n"
	if bench == "skillsbench" {
		arms += "  B: {label: curated skills, set: {skills.set: curated}}\n" +
			"  C: {label: top-5 retrieved from the 34k pool (curated in pool), set: {skills.set: retrieved}}\n" +
			"  D: {label: top-5 retrieved from the 34k pool (curated removed), set: {skills.set: retrieved-wo-curated}}\n" +
			"  E: {label: agentic retrieval through the pool service, set: {skills.set: pool}}\n"
	} else {
		arms += "  C: {label: top-5 retrieved from the 34k pool, set: {skills.set: retrieved}}\n" +
			"  E: {label: agentic retrieval through the pool service, set: {skills.set: pool}}\n"
	}
	if version == "" {
		version = "1.0.0"
	}
	return fmt.Sprintf(`# Skill-Usage arms (arXiv 2604.04323) on %[1]s, written by bifrost-bench
# import skillpool. Arm A installs no skills; the others pick a task skill set
# (skill-sets/<set>). Run after suite lock and suite admit:
#   bifrost-bench run benchmarks/%[1]s/experiment-skills-pool.yaml
name: %[1]s-arms
suite: %[1]s@%[2]s
base: agents/codex.yaml
arms:
%[3]sprimary: {condition: clean, metric: verified_success}
guards:
  - {rule: no_new_integrity_violation}
conditions:
  - {id: clean, tasks: all, trials: 1}
budget: {total_usd: 10}
verdict: {policy: paired_signflip, alpha: 0.05}
seed: 20261122
concurrency: 2
manifest_mode: agent_exposure
`, suite, version, arms)
}

// writePoolSkill writes upstream's find-skills skill pointed at the pool
// service, with a note that only keyword search and /detail are served.
func writePoolSkill(from, dst string) error {
	b, err := os.ReadFile(filepath.Join(from, "repo", "data", "find-skills", "SKILL.md"))
	if err != nil {
		return fmt.Errorf("skillpool: find-skills skill: %w", err)
	}
	s := strings.ReplaceAll(string(b), "http://localhost:8742", fmt.Sprintf("http://%s:%d", ServiceID, ServicePort))
	if i := strings.Index(s, "\n---\n"); i >= 0 {
		s = s[:i+5] + fmt.Sprintf("\n> In this environment the pool service at http://%s:%d serves `/keyword` and `/detail/{id}` only; `/semantic` and `/hybrid` are not available.\n", ServiceID, ServicePort) + s[i+5:]
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte(s), 0o644)
}

// writeService writes the pool service build context: upstream's
// search_server, the BM25 index (hf/unz/skills.db) and every SKILL.md of the
// pool (hf/unz/<owner>--<name>/SKILL.md).
func writeService(from, dst string) error {
	for _, f := range []string{"http_server.py", "bm25_search.py", "semantic_search.py"} {
		if err := copyFile(filepath.Join(from, "repo", "search_server", f), filepath.Join(dst, "search_server", f)); err != nil {
			return err
		}
	}
	unz := filepath.Join(from, "hf", "unz")
	if err := copyFile(filepath.Join(unz, "skills.db"), filepath.Join(dst, "search_server", "index", "skills.db")); err != nil {
		return err
	}
	ents, err := os.ReadDir(unz)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		src := filepath.Join(unz, e.Name(), "SKILL.md")
		if _, err := os.Stat(src); err == nil {
			if err := copyFile(src, filepath.Join(dst, "skills", e.Name(), "SKILL.md")); err != nil {
				return err
			}
		}
	}
	df := `# Skill-Usage pool search (BM25 over 34k skills), upstream search_server.
FROM python:3.12-slim
RUN pip install --no-cache-dir fastapi==0.115.6 uvicorn==0.34.0 numpy==2.2.1
COPY search_server /srv/search_server
COPY skills /srv/skills
WORKDIR /srv/search_server
EXPOSE 8742
CMD ["python", "http_server.py", "--port", "8742"]
`
	return os.WriteFile(filepath.Join(dst, "Dockerfile"), []byte(df), 0o644)
}

func safeName(n string) string {
	b := []byte(n)
	for i, c := range b {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || (i > 0 && (c == '.' || c == '_' || c == '-'))
		if !ok {
			b[i] = '-'
		}
	}
	return string(b)
}

func asList(v any) []any { l, _ := v.([]any); return l }

func sortedKeys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// copyTree hard-links regular files where it can and copies otherwise;
// symlinks are refused.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		t := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(t, 0o755)
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("skillpool: symlink %s refused", p)
		default:
			return copyFile(p, t)
		}
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	_ = os.Remove(dst)
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
