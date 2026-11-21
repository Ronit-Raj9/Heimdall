package tau2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Upstream reward basis names (tau2 RewardType).
const (
	basisDB           = "DB"
	basisCommunicate  = "COMMUNICATE"
	basisNLAssertion  = "NL_ASSERTION"
	basisAction       = "ACTION"
	basisEnvAssertion = "ENV_ASSERTION"
)

// upTask is one task of a tau2 tasks file. Raw keeps the whole document: the
// hidden copy given to the verifier is upstream's own text.
type upTask struct {
	ID           string          `json:"id"`
	UserScenario json.RawMessage `json:"user_scenario"`
	InitialState *upInitialState `json:"initial_state"`
	Criteria     *upCriteria     `json:"evaluation_criteria"`
	Raw          json.RawMessage `json:"-"`
}

type upInitialState struct {
	InitializationData    json.RawMessage   `json:"initialization_data"`
	InitializationActions json.RawMessage   `json:"initialization_actions"`
	MessageHistory        []json.RawMessage `json:"message_history"`
}

// upCriteria keeps Actions raw too, so a null list (upstream: "no expected
// actions", the database always matches) stays apart from an empty one (the
// database must not change).
type upCriteria struct {
	Actions         json.RawMessage `json:"actions"`
	EnvAssertions   json.RawMessage `json:"env_assertions"`
	CommunicateInfo []string        `json:"communicate_info"`
	NLAssertions    []string        `json:"nl_assertions"`
	RewardBasis     []string        `json:"reward_basis"`
}

type upAction struct {
	ActionID  string          `json:"action_id"`
	Requestor string          `json:"requestor"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// actions decodes the expected actions; nil when upstream gives none.
func (c *upCriteria) actions() ([]upAction, error) {
	if c == nil || isNull(c.Actions) {
		return nil, nil
	}
	var out []upAction
	if err := json.Unmarshal(c.Actions, &out); err != nil {
		return nil, fmt.Errorf("evaluation_criteria.actions: %w", err)
	}
	return out, nil
}

func (c *upCriteria) has(basis string) bool {
	if c == nil {
		return false
	}
	for _, b := range c.RewardBasis {
		if b == basis {
			return true
		}
	}
	return false
}

func isNull(raw json.RawMessage) bool {
	s := bytes.TrimSpace(raw)
	return len(s) == 0 || string(s) == "null"
}

// upstream is what the importer reads from a local clone of tau2-bench.
type upstream struct {
	root     string
	version  string // package version in pyproject.toml
	revision string // commit of the clone
}

// openUpstream checks that root looks like a tau2-bench clone and reads its
// version and commit. ref, when set, overrides the commit read from .git.
func openUpstream(root, ref string) (*upstream, error) {
	if _, err := os.Stat(filepath.Join(root, "data", "tau2", "domains")); err != nil {
		return nil, fmt.Errorf("%s is not a tau2-bench clone (no data/tau2/domains): %w", root, err)
	}
	u := &upstream{root: root}
	if b, err := os.ReadFile(filepath.Join(root, "pyproject.toml")); err == nil {
		if m := versionRe.FindSubmatch(b); m != nil {
			u.version = string(m[1])
		}
	}
	u.revision = ref
	if u.revision == "" {
		rev, err := gitHead(root)
		if err != nil {
			return nil, fmt.Errorf("read the commit of %s (pass -set ref=<commit> when it is not a git clone): %w", root, err)
		}
		u.revision = rev
	}
	if !shaRe.MatchString(u.revision) {
		return nil, fmt.Errorf("upstream commit %q is not a 40-hex commit id", u.revision)
	}
	return u, nil
}

var (
	versionRe = regexp.MustCompile(`(?m)^version\s*=\s*"([^"]+)"`)
	shaRe     = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// gitHead reads the commit HEAD points at from root/.git without running git
// (a loose ref, or packed-refs for a fresh clone).
func gitHead(root string) (string, error) {
	gitDir := filepath.Join(root, ".git")
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", err
	}
	h := strings.TrimSpace(string(head))
	ref, ok := strings.CutPrefix(h, "ref: ")
	if !ok {
		return h, nil
	}
	if b, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(ref))); err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	packed, err := os.ReadFile(filepath.Join(gitDir, "packed-refs"))
	if err != nil {
		return "", fmt.Errorf("ref %s not found", ref)
	}
	for _, line := range strings.Split(string(packed), "\n") {
		if sha, name, ok := strings.Cut(strings.TrimSpace(line), " "); ok && name == ref {
			return sha, nil
		}
	}
	return "", fmt.Errorf("ref %s not found", ref)
}

func (u *upstream) domainDir(domain string) string {
	return filepath.Join(u.root, "data", "tau2", "domains", domain)
}

// policy is the domain's agent policy, given to the agent verbatim.
func (u *upstream) policy(domain string) (string, error) {
	b, err := os.ReadFile(filepath.Join(u.domainDir(domain), "policy.md"))
	if err != nil {
		return "", fmt.Errorf("read %s policy: %w", domain, err)
	}
	return string(b), nil
}

// baseTasks reads the domain's registry tasks (tasks.json).
func (u *upstream) baseTasks(domain string) ([]upTask, error) {
	return readTasks(filepath.Join(u.domainDir(domain), "tasks.json"))
}

// publishedTasks is the domain's task set of the released text benchmark
// (data/tau2/benchmarks/text.json): its task subset, with the English language
// pack's reviewed replacements put in place of the registry tasks of the same
// id, as upstream runs them.
func (u *upstream) publishedTasks(domain string) ([]upTask, string, error) {
	var manifest struct {
		Version string `json:"benchmark_version"`
		Subsets map[string]struct {
			Name string `json:"name"`
		} `json:"task_subsets"`
	}
	if err := readJSON(filepath.Join(u.root, "data", "tau2", "benchmarks", "text.json"), &manifest); err != nil {
		return nil, "", fmt.Errorf("read the text benchmark manifest (use -set task_set=all for a clone without one): %w", err)
	}
	sub, ok := manifest.Subsets[domain]
	if !ok {
		return nil, "", fmt.Errorf("the text benchmark %s has no %s subset", manifest.Version, domain)
	}
	var subset struct {
		TaskIDs []string `json:"task_ids"`
	}
	if err := readJSON(filepath.Join(u.root, "data", "tau2", "task_subsets", sub.Name+".json"), &subset); err != nil {
		return nil, "", fmt.Errorf("read task subset %s: %w", sub.Name, err)
	}
	tasks, err := u.baseTasks(domain)
	if err != nil {
		return nil, "", err
	}
	repl, err := u.englishReplacements(domain)
	if err != nil {
		return nil, "", err
	}
	byID := map[string]upTask{}
	for _, t := range tasks {
		byID[t.ID] = t
	}
	for _, t := range repl {
		byID[t.ID] = t
	}
	out := make([]upTask, 0, len(subset.TaskIDs))
	for _, id := range subset.TaskIDs {
		t, ok := byID[id]
		if !ok {
			return nil, "", fmt.Errorf("task subset %s names task %s, which the %s tasks do not have", sub.Name, id, domain)
		}
		out = append(out, t)
	}
	return out, manifest.Version + "/" + sub.Name, nil
}

// englishReplacements reads the tasks file the English language pack names
// for domain, or none when the pack uses the registry tasks unchanged.
func (u *upstream) englishReplacements(domain string) ([]upTask, error) {
	dir := filepath.Join(u.root, "data", "tau2", "multilingual", "en")
	b, err := os.ReadFile(filepath.Join(dir, "pack.yaml"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read English pack: %w", err)
	}
	var pack struct {
		Domains map[string]struct {
			TasksFile string `yaml:"tasks_file"`
		} `yaml:"domains"`
	}
	if err := yaml.Unmarshal(b, &pack); err != nil {
		return nil, fmt.Errorf("parse English pack: %w", err)
	}
	d, ok := pack.Domains[domain]
	if !ok || d.TasksFile == "" {
		return nil, nil
	}
	if strings.Contains(d.TasksFile, "/") || strings.Contains(d.TasksFile, `\`) {
		return nil, fmt.Errorf("English pack: tasks_file %q is not a plain file name", d.TasksFile)
	}
	return readTasks(filepath.Join(dir, d.TasksFile))
}

// mutatingTools lists the domain's tools that change the database, read from
// the @is_tool decorators of src/tau2/domains/<domain>/tools.py: WRITE tools
// unless they say mutates_state=False, and any tool that says
// mutates_state=True.
func (u *upstream) mutatingTools(domain string) (map[string]bool, error) {
	b, err := os.ReadFile(filepath.Join(u.root, "src", "tau2", "domains", domain, "tools.py"))
	if err != nil {
		return nil, fmt.Errorf("read %s tools: %w", domain, err)
	}
	return parseMutatingTools(string(b)), nil
}

var toolDeclRe = regexp.MustCompile(`(?m)^[ \t]*@is_tool\(([^)]*)\)\s*\n[ \t]*def[ \t]+(\w+)[ \t]*\(`)

func parseMutatingTools(src string) map[string]bool {
	out := map[string]bool{}
	for _, m := range toolDeclRe.FindAllStringSubmatch(src, -1) {
		args := strings.ReplaceAll(m[1], " ", "")
		switch {
		case strings.Contains(args, "mutates_state=True"):
			out[m[2]] = true
		case strings.Contains(args, "mutates_state=False"):
		case strings.Contains(args, "ToolType.WRITE"):
			out[m[2]] = true
		}
	}
	return out
}

func readTasks(path string) ([]upTask, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tasks: %w", err)
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(b, &raws); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	out := make([]upTask, 0, len(raws))
	for i, r := range raws {
		var t upTask
		if err := json.Unmarshal(r, &t); err != nil {
			return nil, fmt.Errorf("parse %s task %d: %w", filepath.Base(path), i, err)
		}
		if t.ID == "" {
			return nil, fmt.Errorf("%s task %d has no id", filepath.Base(path), i)
		}
		t.Raw = r
		out = append(out, t)
	}
	return out, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// exported is one domain (and language pack) as `tau2svc.py export` writes
// it from the upstream package: the tasks upstream builds (the localized
// regional tasks of a tau-i18n language pack, or banking_knowledge's task
// files), the policy the agent gets (localized, or with the retrieval
// variant's prompt), and the database-changing tools.
type exported struct {
	Format        string            `json:"format"`
	Domain        string            `json:"domain"`
	Language      string            `json:"language"`
	Retrieval     *string           `json:"retrieval"`
	Subset        string            `json:"subset"`
	Policy        string            `json:"policy"`
	MutatingTools []string          `json:"mutating_tools"`
	Tasks         []json.RawMessage `json:"tasks"`
}

// exportFile is the export of domain in language under dir.
func exportFile(dir, language, domain string) string {
	return filepath.Join(dir, language+"-"+domain+".json")
}

// readExport reads and checks one export file.
func readExport(dir, language, domain string) (*exported, []upTask, error) {
	path := exportFile(dir, language, domain)
	var e exported
	if err := readJSON(path, &e); err != nil {
		return nil, nil, fmt.Errorf("read export %s (write it with `tau2svc.py export --domain %s --language %s --out %s` in the service image; docs/importing-tau2.md): %w", path, domain, language, path, err)
	}
	if e.Format != "tau2-bench-export/1" || e.Domain != domain {
		return nil, nil, fmt.Errorf("export %s: format %q domain %q, want tau2-bench-export/1 and %s", path, e.Format, e.Domain, domain)
	}
	out := make([]upTask, 0, len(e.Tasks))
	for i, r := range e.Tasks {
		var t upTask
		if err := json.Unmarshal(r, &t); err != nil {
			return nil, nil, fmt.Errorf("export %s task %d: %w", path, i, err)
		}
		if t.ID == "" {
			return nil, nil, fmt.Errorf("export %s task %d has no id", path, i)
		}
		t.Raw = r
		out = append(out, t)
	}
	return &e, out, nil
}
