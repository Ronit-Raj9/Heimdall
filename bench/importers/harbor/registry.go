package harbor

// Harbor registry resolver. Harbor's registry.json lists every published
// dataset version as name, version and its task folders, each one a
// git_url, git_commit_id and path. Resolve turns "name@version" into that
// list; Fetch downloads exactly those folders (a sparse, shallow git fetch
// per repository and commit, or the Hugging Face file API for datasets kept
// on the Hub) into a folder the importer then reads like any local clone.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// RegistryURL is Harbor's published registry.
const RegistryURL = "https://raw.githubusercontent.com/harbor-framework/harbor/main/registry.json"

// ManifestFile is written by Fetch next to the fetched tasks. The importer
// reads it to fill the suite's benchmark, source, revision and licence.
const ManifestFile = "harbor-dataset.json"

// Dataset kinds. Model tests ask a single question with a fixed answer and
// measure the model, not an agent; they are listed after agent datasets and
// labelled so.
const (
	KindAgent   = "agent"
	KindModel   = "model_test"
	KindExample = "example"
)

// RegistryTask is one task folder of a dataset version.
type RegistryTask struct {
	Name   string `json:"name"`
	GitURL string `json:"git_url"`
	Commit string `json:"git_commit_id"`
	Path   string `json:"path"`
}

// RegistryDataset is one dataset version of the registry.
type RegistryDataset struct {
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	Description string         `json:"description,omitempty"`
	Tasks       []RegistryTask `json:"tasks"`
}

// Ref is the dataset's name@version.
func (d RegistryDataset) Ref() string { return d.Name + "@" + d.Version }

// Kind says whether the dataset tests agents, models, or is an example.
func (d RegistryDataset) Kind() string {
	if k, ok := datasetKinds[d.Name]; ok {
		return k
	}
	return KindAgent
}

// License is the upstream benchmark's licence when known, else NOASSERTION.
// The registry carries no licence; these were checked on the upstream
// repositories (GitHub licence field) and must be re-checked for new names.
func (d RegistryDataset) License() string {
	if l, ok := datasetLicenses[d.Name]; ok {
		return l
	}
	return "NOASSERTION"
}

// datasetKinds tags registry names that are not agent benchmarks.
var datasetKinds = map[string]string{
	"hello-world":        KindExample,
	"aime":               KindModel,
	"gpqa-diamond":       KindModel,
	"mmmlu":              KindModel,
	"simpleqa":           KindModel,
	"lawbench":           KindModel,
	"ineqmath":           KindModel,
	"reasoning-gym-easy": KindModel,
	"reasoning-gym-hard": KindModel,
	"satbench":           KindModel,
	"strongreject":       KindModel,
	"kumo":               KindModel,
	"mmau":               KindModel,
	"pixiu":              KindModel,
	"arc_agi_2":          KindModel,
}

// datasetLicenses holds licences checked upstream (gh api repos/<owner>/<repo>).
var datasetLicenses = map[string]string{
	"terminal-bench":        "Apache-2.0",
	"terminal-bench-sample": "Apache-2.0",
	"bfcl":                  "Apache-2.0", // ShishirPatil/gorilla
	"bfcl_parity":           "Apache-2.0",
	"algotune":              "MIT", // oripress/AlgoTune
	"spider2-dbt":           "MIT", // xlang-ai/Spider2
	"hello-world":           "Apache-2.0",
}

// Registry is a parsed registry.json.
type Registry struct {
	Datasets []RegistryDataset
}

// ParseRegistry reads registry.json content.
func ParseRegistry(b []byte) (*Registry, error) {
	var ds []RegistryDataset
	if err := json.Unmarshal(b, &ds); err != nil {
		return nil, fmt.Errorf("harbor registry: %w", err)
	}
	return &Registry{Datasets: ds}, nil
}

// LoadRegistry reads the registry from a local file or an http(s) URL; ""
// means RegistryURL.
func LoadRegistry(ctx context.Context, src string, client *http.Client) (*Registry, error) {
	if src == "" {
		src = RegistryURL
	}
	if !strings.HasPrefix(src, "http://") && !strings.HasPrefix(src, "https://") {
		b, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("harbor registry: %w", err)
		}
		return ParseRegistry(b)
	}
	b, err := httpGet(ctx, client, src, "")
	if err != nil {
		return nil, fmt.Errorf("harbor registry: %w", err)
	}
	return ParseRegistry(b)
}

// Sorted lists the datasets, agent datasets first, then by name and version.
func (r *Registry) Sorted() []RegistryDataset {
	out := append([]RegistryDataset(nil), r.Datasets...)
	rank := map[string]int{KindAgent: 0, KindModel: 1, KindExample: 2}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := rank[out[i].Kind()], rank[out[j].Kind()]; a != b {
			return a < b
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Version < out[j].Version
	})
	return out
}

// Resolve finds "name@version". A bare name resolves when the registry has
// exactly one version of it; otherwise the error lists the versions.
func (r *Registry) Resolve(ref string) (*RegistryDataset, error) {
	name, version, hasVersion := strings.Cut(strings.TrimSpace(ref), "@")
	if name == "" {
		return nil, fmt.Errorf("harbor registry: empty dataset name")
	}
	var versions []string
	for i := range r.Datasets {
		d := &r.Datasets[i]
		if d.Name != name {
			continue
		}
		if hasVersion && d.Version == version {
			return d, nil
		}
		versions = append(versions, d.Version)
	}
	switch {
	case len(versions) == 0:
		return nil, fmt.Errorf("harbor registry: no dataset named %q", name)
	case !hasVersion && len(versions) == 1:
		return r.Resolve(name + "@" + versions[0])
	case !hasVersion:
		return nil, fmt.Errorf("harbor registry: %s has versions %s; name one as %s@<version>", name, strings.Join(versions, ", "), name)
	}
	return nil, fmt.Errorf("harbor registry: %s has no version %q (versions: %s)", name, version, strings.Join(versions, ", "))
}

// LooksLikeRef reports whether s reads as a registry name@version rather
// than a local path.
func LooksLikeRef(s string) bool {
	name, version, ok := strings.Cut(s, "@")
	return ok && name != "" && version != "" && !strings.ContainsAny(s, `\`) && !strings.HasPrefix(s, ".") && !strings.HasPrefix(s, "/") &&
		!strings.Contains(version, "/")
}

// Manifest is what Fetch records about the download.
type Manifest struct {
	Benchmark string `json:"benchmark"` // name@version
	Source    string `json:"source"`    // repository URL (the first, when tasks span several)
	Revision  string `json:"revision"`  // commit (the first, when tasks span several)
	License   string `json:"license"`
	Kind      string `json:"kind"`
	Registry  string `json:"registry"`
	FetchedAt string `json:"fetched_at"`
	Tasks     int    `json:"tasks"`
}

// FetchOptions select what Fetch downloads.
type FetchOptions struct {
	IDs      []string // task names; empty means all
	Limit    int      // at most this many tasks; 0 means all
	Registry string   // where the registry came from, recorded in the manifest
	HFToken  string   // bearer token for gated Hugging Face datasets
	HTTP     *http.Client
	// Git runs git with args in dir; nil runs the git binary.
	Git func(ctx context.Context, dir string, args ...string) error
	Now func() time.Time
}

// Fetch downloads the dataset's task folders into dest/tasks/<task> and
// writes dest/harbor-dataset.json and dest/registry.json (this dataset's
// entry). Task folders already present are kept, so a repeated fetch
// resumes.
func Fetch(ctx context.Context, d *RegistryDataset, dest string, opts FetchOptions) (*Manifest, error) {
	tasks, err := pickTasks(d, opts.IDs, opts.Limit)
	if err != nil {
		return nil, err
	}
	if opts.Git == nil {
		opts.Git = runGit
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	tasksDir := filepath.Join(dest, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		return nil, err
	}
	type group struct{ url, commit string }
	groups := map[group][]RegistryTask{}
	var order []group
	for _, t := range tasks {
		if isDir(filepath.Join(tasksDir, fetchedName(t))) {
			continue
		}
		g := group{t.GitURL, t.Commit}
		if _, ok := groups[g]; !ok {
			order = append(order, g)
		}
		groups[g] = append(groups[g], t)
	}
	for _, g := range order {
		var err error
		if hfRepo(g.url) != "" {
			err = fetchHF(ctx, g.url, g.commit, groups[g], tasksDir, opts)
		} else {
			err = fetchGit(ctx, g.url, g.commit, groups[g], dest, tasksDir, opts)
		}
		if err != nil {
			return nil, err
		}
	}
	m := &Manifest{Benchmark: d.Ref(), License: d.License(), Kind: d.Kind(), Registry: opts.Registry,
		FetchedAt: opts.Now().UTC().Format(time.RFC3339), Tasks: len(tasks)}
	if len(tasks) > 0 {
		m.Source = strings.TrimSuffix(tasks[0].GitURL, ".git")
		m.Revision = tasks[0].Commit
	}
	if m.Registry == "" {
		m.Registry = RegistryURL
	}
	entry := *d
	entry.Tasks = tasks
	for _, f := range []struct {
		name string
		v    any
	}{{ManifestFile, m}, {"registry.json", []RegistryDataset{entry}}} {
		b, err := json.MarshalIndent(f.v, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(dest, f.name), append(b, '\n'), 0o644); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// pickTasks applies ids and limit, keeping registry order.
func pickTasks(d *RegistryDataset, ids []string, limit int) ([]RegistryTask, error) {
	var out []RegistryTask
	if len(ids) == 0 {
		out = append(out, d.Tasks...)
	} else {
		byName := map[string]RegistryTask{}
		for _, t := range d.Tasks {
			byName[t.Name] = t
			byName[fetchedName(t)] = t
		}
		var missing []string
		for _, id := range ids {
			t, ok := byName[strings.TrimSpace(id)]
			if !ok {
				missing = append(missing, id)
				continue
			}
			out = append(out, t)
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("harbor registry: %s has no task %s", d.Ref(), strings.Join(missing, ", "))
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	for _, t := range out {
		if err := checkTask(t); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// checkTask refuses entries that could write outside dest or run options.
func checkTask(t RegistryTask) error {
	clean := path.Clean(t.Path)
	switch {
	case t.GitURL == "" || t.Commit == "" || t.Path == "":
		return fmt.Errorf("harbor registry: task %q lacks git_url, git_commit_id or path", t.Name)
	case !hexRE.MatchString(t.Commit):
		return fmt.Errorf("harbor registry: task %q: commit %q is not a full hash", t.Name, t.Commit)
	case strings.HasPrefix(t.GitURL, "-") || strings.HasPrefix(clean, "-") || strings.HasPrefix(clean, "/") || clean == "." || strings.HasPrefix(clean, ".."):
		return fmt.Errorf("harbor registry: task %q: unsafe url or path", t.Name)
	}
	u, err := url.Parse(t.GitURL)
	if err != nil || u.Scheme != "https" {
		return fmt.Errorf("harbor registry: task %q: git_url must be https", t.Name)
	}
	if n := fetchedName(t); n == "" || n == "." || n == ".." || strings.HasPrefix(n, ".") {
		return fmt.Errorf("harbor registry: task %q: bad folder name", t.Name)
	}
	return nil
}

// fetchedName is the task's folder name under dest/tasks: the last path
// element, which is what upstream calls the task folder.
func fetchedName(t RegistryTask) string { return path.Base(path.Clean(t.Path)) }

// fetchGit does a shallow, blob-filtered, sparse fetch of commit and moves
// each task folder into tasksDir.
func fetchGit(ctx context.Context, repoURL, commit string, tasks []RegistryTask, dest, tasksDir string, opts FetchOptions) error {
	work := filepath.Join(dest, ".git-"+commit[:12])
	if err := os.RemoveAll(work); err != nil {
		return err
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	paths := make([]string, 0, len(tasks))
	for _, t := range tasks {
		paths = append(paths, path.Clean(t.Path))
	}
	steps := [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", repoURL},
		append([]string{"sparse-checkout", "set", "--no-cone", "--"}, sparsePatterns(paths)...),
		{"fetch", "-q", "--depth", "1", "--filter=blob:none", "origin", commit},
		{"checkout", "-q", "FETCH_HEAD"},
	}
	for _, s := range steps {
		if err := opts.Git(ctx, work, s...); err != nil {
			return fmt.Errorf("harbor fetch %s@%s: git %s: %w", repoURL, commit[:12], s[0], err)
		}
	}
	for i, t := range tasks {
		src := filepath.Join(work, filepath.FromSlash(paths[i]))
		if !isDir(src) {
			return fmt.Errorf("harbor fetch: %s has no folder %s at %s", repoURL, t.Path, commit[:12])
		}
		if err := os.Rename(src, filepath.Join(tasksDir, fetchedName(t))); err != nil {
			return err
		}
	}
	return os.RemoveAll(work)
}

// sparsePatterns turns folders into non-cone sparse patterns ("/a/b/").
func sparsePatterns(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, "/"+strings.Trim(p, "/")+"/")
	}
	return out
}

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 400 {
			msg = msg[len(msg)-400:]
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}

// hfBase is the Hugging Face host; tests point it at a fake.
var hfBase = "https://huggingface.co"

// hfWorkers is how many files of one task download at once.
const hfWorkers = 16

// hfRepo returns "org/name" for a Hugging Face dataset git URL, else "".
func hfRepo(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host != "huggingface.co" {
		return ""
	}
	p := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	parts := strings.Split(p, "/")
	if len(parts) != 3 || parts[0] != "datasets" {
		return ""
	}
	return parts[1] + "/" + parts[2]
}

// hfEntry is one item of the Hub tree API.
type hfEntry struct {
	Type string `json:"type"` // file or directory
	Path string `json:"path"`
}

// fetchHF lists each task folder with the Hub tree API at commit and
// downloads its files through resolve/ (which serves LFS content too).
func fetchHF(ctx context.Context, repoURL, commit string, tasks []RegistryTask, tasksDir string, opts FetchOptions) error {
	repo := hfRepo(repoURL)
	for _, t := range tasks {
		root := path.Clean(t.Path)
		var files []string
		next := fmt.Sprintf("%s/api/datasets/%s/tree/%s/%s?recursive=true", hfBase, repo, commit, escapePath(root))
		for next != "" {
			b, link, err := httpGetLink(ctx, opts.HTTP, next, opts.HFToken)
			if err != nil {
				return fmt.Errorf("harbor fetch %s: list %s: %w", repo, root, err)
			}
			var entries []hfEntry
			if err := json.Unmarshal(b, &entries); err != nil {
				return fmt.Errorf("harbor fetch %s: list %s: %w", repo, root, err)
			}
			for _, e := range entries {
				if e.Type == "file" {
					files = append(files, e.Path)
				}
			}
			next = link
		}
		if len(files) == 0 {
			return fmt.Errorf("harbor fetch: %s has no files under %s at %s", repo, root, commit[:12])
		}
		stage := filepath.Join(tasksDir, ".part-"+fetchedName(t))
		if err := os.RemoveAll(stage); err != nil {
			return err
		}
		for _, f := range files {
			rel := strings.TrimPrefix(path.Clean(f), root+"/")
			if rel == f || strings.HasPrefix(rel, "..") || path.IsAbs(rel) {
				return fmt.Errorf("harbor fetch: %s: file %s is outside %s", repo, f, root)
			}
		}
		// Task folders hold many small files: download hfWorkers at a time.
		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			firstEr error
			sem     = make(chan struct{}, hfWorkers)
		)
		for _, f := range files {
			rel := strings.TrimPrefix(path.Clean(f), root+"/")
			out := filepath.Join(stage, filepath.FromSlash(rel))
			u := fmt.Sprintf("%s/datasets/%s/resolve/%s/%s", hfBase, repo, commit, escapePath(f))
			wg.Add(1)
			sem <- struct{}{}
			go func(f, u, out string) {
				defer func() { <-sem; wg.Done() }()
				if err := download(ctx, opts.HTTP, u, opts.HFToken, out); err != nil {
					mu.Lock()
					if firstEr == nil {
						firstEr = fmt.Errorf("harbor fetch %s: %s: %w", repo, f, err)
					}
					mu.Unlock()
				}
			}(f, u, out)
		}
		wg.Wait()
		if firstEr != nil {
			return firstEr
		}
		if err := os.Rename(stage, filepath.Join(tasksDir, fetchedName(t))); err != nil {
			return err
		}
	}
	return nil
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func httpClient(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func newRequest(ctx context.Context, u, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("User-Agent", "heimdall-bench")
	return req, nil
}

func httpGet(ctx context.Context, c *http.Client, u, token string) ([]byte, error) {
	b, _, err := httpGetLink(ctx, c, u, token)
	return b, err
}

// httpGetLink GETs u and returns the body and the rel="next" link, if any.
func httpGetLink(ctx context.Context, c *http.Client, u, token string) ([]byte, string, error) {
	req, err := newRequest(ctx, u, token)
	if err != nil {
		return nil, "", err
	}
	resp, err := httpClient(c).Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("GET %s: %s", redactURL(u), resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	return b, nextLink(resp.Header.Get("Link")), err
}

// nextLink parses `<url>; rel="next"` from a Link header.
func nextLink(h string) string {
	for _, part := range strings.Split(h, ",") {
		segs := strings.Split(part, ";")
		if len(segs) < 2 {
			continue
		}
		for _, s := range segs[1:] {
			if strings.TrimSpace(s) == `rel="next"` {
				return strings.Trim(strings.TrimSpace(segs[0]), "<>")
			}
		}
	}
	return ""
}

// download fetches u into out, retrying rate limits and server errors.
func download(ctx context.Context, c *http.Client, u, token, out string) error {
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if err = downloadOnce(ctx, c, u, token, out); err == nil || !retryable(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 2 * time.Second):
		}
	}
	return err
}

// statusError is a non-200 reply.
type statusError struct {
	url  string
	code int
	text string
}

func (e *statusError) Error() string { return "GET " + redactURL(e.url) + ": " + e.text }

func retryable(err error) bool {
	var se *statusError
	if errors.As(err, &se) {
		return se.code == http.StatusTooManyRequests || se.code >= 500
	}
	return true // network errors
}

func downloadOnce(ctx context.Context, c *http.Client, u, token, out string) error {
	req, err := newRequest(ctx, u, token)
	if err != nil {
		return err
	}
	resp, err := httpClient(c).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &statusError{url: u, code: resp.StatusCode, text: resp.Status}
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func redactURL(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return "(url)"
	}
	p.User = nil
	p.RawQuery = ""
	return p.String()
}

// readManifest reads ManifestFile from dir, or nil.
func readManifest(dir string) *Manifest {
	b, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return nil
	}
	var m Manifest
	if json.Unmarshal(b, &m) != nil || m.Benchmark == "" {
		return nil
	}
	return &m
}
