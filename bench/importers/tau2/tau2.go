package tau2

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// Name is the import format name.
const Name = "tau2"

// Importer settings (-set key=value).
const (
	SetDomains   = "domains"         // comma-separated domains; default airline,retail
	SetTaskSet   = "task_set"        // published (default) or all
	SetNL        = "nl_assertions"   // skip (default) or unscored
	SetNoop      = "noop_tasks"      // skip (default) or include
	SetUser      = "user"            // scripted (default) or llm (a pinned model plays the user, W3)
	SetRef       = "ref"             // upstream commit, when -from is not a git clone
	SetMaxTurns  = "max_user_turns"  // replies before the user leaves; default 30
	SetRetrieval = "retrieval"       // banking_knowledge retrieval variant; default bm25
	SetUserTools = "user_tool_tasks" // skip (default) or draft: tasks whose expected actions include the customer's own tool calls
	SetLanguage  = "language"        // tau-i18n text-v1 language pack (en, es, pt, hi, zh, ko); unset: the registry tasks
	SetExportDir = "export_dir"      // where `tau2svc.py export` files are; default <from>/.bench-export
)

// Values of the settings above.
const (
	TaskSetPublished = "published"
	TaskSetAll       = "all"
	NLSkip           = "skip"
	NLUnscored       = "unscored"
	NoopSkip         = "skip"
	NoopInclude      = "include"
	UserScripted     = "scripted"
	UserLLM          = "llm" // W3 customer support: upstream's model-played user through bench/simuser
	UserToolsSkip    = "skip"
	UserToolsDraft   = "draft" // imported, tagged draft-user-tools: no bench user simulator can call the customer's tools, so admission refuses them
)

// retrievalTemplates maps the banking_knowledge retrieval variants this
// importer supports (offline ones: no embedding API, no shell sandbox) to
// their upstream prompt template in data/tau2/domains/banking_knowledge/prompts.
var retrievalTemplates = map[string]string{
	"bm25":         "classic_rag_bm25_no_grep.md",
	"grep_only":    "grep_only.md",
	"no_knowledge": "no_knowledge.md",
}

// languages are the tau-i18n language packs of tau2-bench 2.0.
var languages = []string{"en", "es", "pt", "hi", "zh", "ko"}

// draftUserToolsTag marks a task kept as a draft: its expected actions
// include tool calls the customer makes, which no bench user simulator does.
const draftUserToolsTag = "draft-user-tools"

// supportedDomains are the domains this importer can run. airline and retail
// need only agent tools and a customer who talks; banking_knowledge (tau3,
// tau2-bench 1.0+) also needs a retrieval backend, which the service runs
// in-process (offline BM25 by default). telecom needs user-side device tools.
var supportedDomains = []string{"airline", "retail", "banking_knowledge"}

// defaultDomains are imported when -set domains is not given.
var defaultDomains = []string{"airline", "retail"}

const (
	upstreamRepo    = "https://github.com/sierra-research/tau2-bench"
	upstreamLicense = "MIT"
	serviceID       = "tau2"
	servicePort     = 8000
	suiteVersion    = "1.0.0"
	taskVersion     = "1.0.0"
	agentTimeoutS   = 1800
	verifierTimeout = 600
	dbExportMax     = 64 << 20
)

//go:embed image/Dockerfile
var serviceDockerfile string

//go:embed image/tau2svc.py
var serviceScript string

//go:embed image/base.Dockerfile
var baseDockerfile string

//go:embed image/control.py
var controlScript string

func init() {
	importers.Register(&importer{now: time.Now})
}

// importer converts tau2-bench domains into a tool_use suite whose tasks run
// the domain as an MCP service and are graded on the final database.
type importer struct {
	now func() time.Time
}

// Info implements importers.Importer.
func (im *importer) Info() importers.Info {
	return importers.Info{
		Name:       Name,
		Benchmarks: []string{"tau2-bench 2.x text: airline (airline_50), retail (retail_50)", "tau3 banking_knowledge (banking_knowledge_97)", "tau-i18n text-v1 (en, es, pt, hi, zh, ko)"},
		Source:     "git clone --depth 1 " + upstreamRepo + ".git datasets/tau2-bench",
		License:    upstreamLicense,
		Track:      schemas.TrackToolUse,
		Grading:    schemas.GradingVerified,
		Needs: []string{
			"network while the service image builds (it installs the upstream package at the pinned commit)",
			"services, state export, script controls and the tool proxy in the run",
		},
		Summary: "tau2-bench customer service domains as MCP tasks with a scripted customer; graded on the final database (scores are not comparable with the upstream leaderboard)",
	}
}

// settings are the parsed -set values of one import.
type settings struct {
	domains   []string
	taskSet   string
	nl        string
	noop      string
	user      string
	ref       string
	maxTurns  int
	retrieval string
	userTools string
	language  string
	langSet   bool   // -set language given: tasks come from the language pack export
	exportDir string // "" means <from>/.bench-export
}

// usesExport reports whether domain's tasks and policy come from an export
// file: a language pack's tasks are built by upstream code (regional
// database, localized names), and banking_knowledge's policy depends on the
// retrieval variant.
func (s settings) usesExport(domain string) bool {
	return s.langSet || domain == "banking_knowledge"
}

// exportLanguage is the language part of domain's export file name.
func (s settings) exportLanguage() string {
	if s.langSet {
		return s.language
	}
	return "en"
}

func parseSettings(extra map[string]string) (settings, error) {
	s := settings{domains: defaultDomains, taskSet: TaskSetPublished, nl: NLSkip, noop: NoopSkip, user: UserScripted, maxTurns: 30,
		retrieval: "bm25", userTools: UserToolsSkip, language: "en"}
	known := []string{SetDomains, SetTaskSet, SetNL, SetNoop, SetUser, SetRef, SetMaxTurns, SetRetrieval, SetUserTools, SetLanguage, SetExportDir}
	for k := range extra {
		if !slices.Contains(known, k) {
			return s, fmt.Errorf("unknown setting %q (known: %s)", k, strings.Join(known, ", "))
		}
	}
	if v := extra[SetDomains]; v != "" {
		s.domains = nil
		for _, d := range strings.Split(v, ",") {
			d = strings.TrimSpace(d)
			if !slices.Contains(supportedDomains, d) {
				return s, fmt.Errorf("domain %q is not supported (supported: %s)", d, strings.Join(supportedDomains, ", "))
			}
			if !slices.Contains(s.domains, d) {
				s.domains = append(s.domains, d)
			}
		}
	}
	pick := func(key, def string, allowed ...string) (string, error) {
		v := extra[key]
		if v == "" {
			return def, nil
		}
		if !slices.Contains(allowed, v) {
			return "", fmt.Errorf("%s=%q: want one of %s", key, v, strings.Join(allowed, ", "))
		}
		return v, nil
	}
	var err error
	if s.taskSet, err = pick(SetTaskSet, TaskSetPublished, TaskSetPublished, TaskSetAll); err != nil {
		return s, err
	}
	if s.nl, err = pick(SetNL, NLSkip, NLSkip, NLUnscored); err != nil {
		return s, err
	}
	if s.noop, err = pick(SetNoop, NoopSkip, NoopSkip, NoopInclude); err != nil {
		return s, err
	}
	if s.user, err = pick(SetUser, UserScripted, UserScripted, UserLLM); err != nil {
		return s, err
	}
	if s.userTools, err = pick(SetUserTools, UserToolsSkip, UserToolsSkip, UserToolsDraft); err != nil {
		return s, err
	}
	if s.language, err = pick(SetLanguage, "en", languages...); err != nil {
		return s, err
	}
	if v := extra[SetRetrieval]; v != "" {
		if _, ok := retrievalTemplates[v]; !ok {
			return s, fmt.Errorf("%s=%q: want one of bm25, grep_only, no_knowledge (the offline variants)", SetRetrieval, v)
		}
		s.retrieval = v
	}
	s.langSet = extra[SetLanguage] != ""
	s.exportDir = extra[SetExportDir]
	if s.langSet {
		if s.taskSet != TaskSetPublished {
			return s, fmt.Errorf("%s=%s: only task_set=published (the text-v1 tasks of the language pack)", SetLanguage, s.language)
		}
		for _, d := range s.domains {
			if d == "banking_knowledge" {
				return s, fmt.Errorf("%s=%s: banking_knowledge has no text-v1 language pack tasks", SetLanguage, s.language)
			}
		}
	}
	s.ref = extra[SetRef]
	if v := extra[SetMaxTurns]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 2 || n > 200 {
			return s, fmt.Errorf("%s=%q: want a number from 2 to 200", SetMaxTurns, v)
		}
		s.maxTurns = n
	}
	return s, nil
}

// candidate is one upstream task chosen for import.
type candidate struct {
	domain string
	task   upTask
	id     string // bench task id
	draft  string // why the task is kept as a draft (user_tool_tasks=draft), "" otherwise
}

// Import implements importers.Importer.
func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	if opts.SuiteID == "" || opts.From == "" || opts.BenchRoot == "" {
		return nil, errors.New("tau2: bench root, -from and -suite are required")
	}
	set, err := parseSettings(opts.Extra)
	if err != nil {
		return nil, fmt.Errorf("tau2: %w", err)
	}
	up, err := openUpstream(opts.From, set.ref)
	if err != nil {
		return nil, fmt.Errorf("tau2: %w", err)
	}
	if set.exportDir == "" {
		set.exportDir = filepath.Join(opts.From, ".bench-export")
	}
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: opts.SuiteDir()}
	var picked []candidate
	sets := map[string]string{}
	for _, domain := range set.domains {
		cands, setName, skips, err := selectTasks(up, domain, set, opts.IDs)
		if err != nil {
			return nil, fmt.Errorf("tau2: %w", err)
		}
		sets[domain] = setName
		res.Skipped = append(res.Skipped, skips...)
		picked = append(picked, cands...)
	}
	if opts.Limit > 0 && len(picked) > opts.Limit {
		for _, c := range picked[opts.Limit:] {
			res.Skipped = append(res.Skipped, importers.Skip{ID: c.id, Reason: fmt.Sprintf("over -limit %d", opts.Limit)})
		}
		picked = picked[:opts.Limit]
	}
	w := &writer{up: up, set: set, suiteID: opts.SuiteID, suiteDir: res.SuiteDir, policies: map[string]string{}}
	if err := w.prepare(set.domains); err != nil {
		return nil, fmt.Errorf("tau2: %w", err)
	}
	for _, c := range picked {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("tau2: %w", err)
		}
		dir := filepath.Join(res.SuiteDir, "tasks", c.id)
		if _, err := os.Stat(dir); err == nil && !opts.Force {
			res.Skipped = append(res.Skipped, importers.Skip{ID: c.id, Reason: "task folder exists (pass -force to replace it)"})
			continue
		}
		if err := w.writeTask(dir, c); err != nil {
			return nil, fmt.Errorf("tau2: task %s: %w", c.id, err)
		}
		res.Tasks = append(res.Tasks, c.id)
	}
	rec := schemas.ImportRecord{
		Importer:  Name,
		Benchmark: benchmarkName(up, set, sets),
		Source:    upstreamRepo,
		Revision:  up.revision,
		License:   upstreamLicense,
		At:        im.now().UTC().Truncate(time.Second),
	}
	if err := w.writeSuite(opts.SuiteID, res.Tasks, rec); err != nil {
		return nil, fmt.Errorf("tau2: %w", err)
	}
	if err := importers.Finish(res.SuiteDir); err != nil {
		return nil, err
	}
	if w.serviceTag != "" {
		res.Notes = append(res.Notes, "the first run builds the service image "+w.serviceTag+" with network (it installs tau2 at commit "+up.revision[:12]+")")
	}
	res.Notes = append(res.Notes,
		"bifrost-bench suite lint "+opts.SuiteID,
		"bifrost-bench suite lock "+opts.SuiteID,
		"bifrost-bench suite admit "+opts.SuiteID,
		"the customer is scripted, not a model: scores are not comparable with the upstream leaderboard (docs/importing-tau2.md)",
	)
	return res, nil
}

func benchmarkName(up *upstream, set settings, sets map[string]string) string {
	name := "tau2-bench"
	if up.version != "" {
		name += "@" + up.version
	}
	parts := make([]string, 0, len(set.domains))
	for _, d := range set.domains {
		if set.taskSet == TaskSetPublished {
			parts = append(parts, d+":"+sets[d])
		} else {
			parts = append(parts, d+":all")
		}
	}
	return name + " " + strings.Join(parts, ",")
}

// selectTasks reads one domain's task set and keeps the tasks this importer
// can grade without a model, listing every other one with its reason.
func selectTasks(up *upstream, domain string, set settings, ids []string) ([]candidate, string, []importers.Skip, error) {
	var tasks []upTask
	var setName string
	var err error
	var writes map[string]bool
	if set.usesExport(domain) {
		var e *exported
		e, tasks, err = readExport(set.exportDir, set.exportLanguage(), domain)
		if err != nil {
			return nil, "", nil, err
		}
		setName = e.Subset
		writes = map[string]bool{}
		for _, n := range e.MutatingTools {
			writes[n] = true
		}
		if set.exportLanguage() == "en" && domain != "banking_knowledge" && set.taskSet == TaskSetPublished {
			// The English pack binds the whole registry; text-v1 runs its subset.
			pub, name, err := up.publishedTasks(domain)
			if err != nil {
				return nil, "", nil, err
			}
			keep := map[string]bool{}
			for _, t := range pub {
				keep[t.ID] = true
			}
			tasks = slices.DeleteFunc(tasks, func(t upTask) bool { return !keep[t.ID] })
			setName = "text-v1/en/" + name
		}
	} else if set.taskSet == TaskSetPublished {
		tasks, setName, err = up.publishedTasks(domain)
	} else {
		tasks, err = up.baseTasks(domain)
		setName = "all"
	}
	if err != nil {
		return nil, "", nil, err
	}
	if writes == nil {
		if writes, err = up.mutatingTools(domain); err != nil {
			return nil, "", nil, err
		}
	}
	var out []candidate
	var skips []importers.Skip
	for _, t := range tasks {
		id := taskID(domain, t.ID)
		if set.langSet {
			id = set.language + "-" + id
		}
		if len(ids) > 0 && !slices.Contains(ids, id) && !slices.Contains(ids, t.ID) {
			continue
		}
		reason := skipReason(t, writes, set)
		if reason == userToolsReason && set.userTools == UserToolsDraft {
			out = append(out, candidate{domain: domain, task: t, id: id, draft: reason})
			skips = append(skips, importers.Skip{ID: id, Reason: "imported as a draft (tag " + draftUserToolsTag + "): " + reason})
			continue
		}
		if reason != "" {
			skips = append(skips, importers.Skip{ID: id, Reason: reason})
			continue
		}
		out = append(out, candidate{domain: domain, task: t, id: id})
	}
	return out, setName, skips, nil
}

// skipReason says why a task cannot be imported as a verified state task,
// or "" when it can.
func skipReason(t upTask, writes map[string]bool, set settings) string {
	c := t.Criteria
	if c == nil {
		return "no evaluation criteria"
	}
	if t.InitialState != nil && len(t.InitialState.MessageHistory) > 0 {
		return "starts mid-conversation (initial message history), which this importer does not replay"
	}
	for _, b := range c.RewardBasis {
		switch b {
		case basisDB, basisCommunicate, basisNLAssertion, basisAction:
		default:
			return fmt.Sprintf("reward basis %s is not supported (only DB, ACTION, COMMUNICATE and NL_ASSERTION)", b)
		}
	}
	if !c.has(basisDB) && !c.has(basisAction) {
		return "reward basis has no DB or ACTION check"
	}
	acts, err := c.actions()
	if err != nil {
		return "unreadable expected actions: " + err.Error()
	}
	mutates := false
	for _, a := range acts {
		if a.Requestor == "user" {
			return userToolsReason
		}
		if writes[a.Name] {
			mutates = true
		}
	}
	if c.has(basisNLAssertion) && len(c.NLAssertions) > 0 && set.nl == NLSkip {
		return fmt.Sprintf("needs a judge model for %d natural-language assertion(s) (-set %s=%s scores the rest)", len(c.NLAssertions), SetNL, NLUnscored)
	}
	communicates := c.has(basisCommunicate) && len(c.CommunicateInfo) > 0
	if !mutates && !communicates && !(c.has(basisAction) && len(acts) > 0) && set.noop == NoopSkip {
		return "the correct outcome is no database change and nothing to communicate, so doing nothing passes and the empty control cannot fail"
	}
	return ""
}

// userToolsReason is the skip reason of tasks the customer must act in.
const userToolsReason = "expected actions include user-side tool calls (the customer runs tools), which the bench's user simulators cannot do"

var badIDChars = regexp.MustCompile(`[^a-z0-9._-]+`)

// taskID is the bench task id of upstream task id in domain.
func taskID(domain, id string) string {
	return domain + "-" + strings.Trim(badIDChars.ReplaceAllString(strings.ToLower(id), "-"), "-")
}

// shortHash is the first 12 hex digits of the SHA-256 of parts.
func shortHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// existingTasks reads the task ids of a suite.yaml already in dir, so a
// second import extends the suite instead of dropping its tasks.
func existingTasks(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "suite.yaml"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s schemas.Suite
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("read existing suite.yaml: %w", err)
	}
	return s.TaskIDs, nil
}
