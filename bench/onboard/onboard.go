// Package onboard is the one front door for adding a benchmark (lane Z20,
// audit/INFRA_I3 items 1-3): Detect, Inspect, Draft, Check, Publish.
//
//   - Detect sniffs what an input is (file type, host, marker files) with no
//     model call and at most one guarded fetch.
//   - Inspect describes it: units, up to five samples, columns, licence,
//     warnings and an estimate, and suggests a plan. Nothing is written.
//   - Draft writes task folders into a staging area, drafts/<set>/, that is
//     a full suite folder outside benchmarks/: the planner never sees it,
//     and the readiness checks run on it unchanged.
//   - The cheap gates (static validation, duplicates, split) run in process
//     on every draft; the readiness checks (reference passes, empty, wrong
//     answers and exploit fail) run as the onboard.ready job.
//   - Publish copies the chosen tasks into benchmarks/<suite>, stamps the
//     canary when tasks are held out, and writes the recipe onboard.yaml so
//     the benchmark can be refreshed from the same sources later.
//
// The rule that keeps trust: a source may write words, only the sandbox
// decides a task is ready.
package onboard

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/scaffold"
)

// Kind is what an input is.
type Kind string

// Input kinds. Every kind is detected; the ones a source can draft from say
// so in their Match (CanDraft).
const (
	KindRows      Kind = "rows"       // CSV, JSONL, JSON, tickets
	KindHF        Kind = "hf_dataset" // huggingface.co/datasets/<org>/<name>
	KindGitHub    Kind = "github"     // a GitHub repository
	KindHarbor    Kind = "harbor"     // a Harbor registry id name@version, or a Harbor task folder
	KindImporter  Kind = "benchmark"  // a folder or zip in a format a registered importer reads
	KindMigration Kind = "migration"  // OpenAI Evals export, promptfoo config
	KindOpenAPI   Kind = "openapi"    // an OpenAPI or Swagger file
	KindTraces    Kind = "traces"     // OTLP or trace exports
	KindMCP       Kind = "mcp_server" // an MCP server URL
	KindWebsite   Kind = "website"    // any other web page
)

// Input is what the person gave: exactly one of URL, Upload or Data.
type Input struct {
	// URL is a public https URL, a Hugging Face or GitHub link, or a Harbor
	// registry id (name@version).
	URL string `json:"url,omitempty"`
	// Upload is a project path under uploads/ (POST /onboard/upload).
	Upload string `json:"upload,omitempty"`
	// Data is pasted text (rows); Name is its file name, for the format.
	Data string `json:"data,omitempty"`
	Name string `json:"name,omitempty"`
	// Connection is a keystore secret id holding a token for the source (a
	// Hugging Face token for a gated dataset). Never stored in the set.
	Connection string `json:"connection,omitempty"`
	// Hint picks a kind when two match.
	Hint Kind `json:"hint,omitempty"`
}

// Validate checks the input shape.
func (in Input) Validate() error {
	n := 0
	for _, v := range []string{in.URL, in.Upload, in.Data} {
		if strings.TrimSpace(v) != "" {
			n++
		}
	}
	if n != 1 {
		return errors.New("give exactly one of url, upload or data")
	}
	if len(in.URL) > 2048 || len(in.Upload) > 512 || len(in.Name) > 200 || strings.ContainsAny(in.Name+in.URL+in.Upload, "\n\r") {
		return errors.New("url, upload and name are one line each (url at most 2048 characters)")
	}
	if len(in.Data) > MaxDataBytes {
		return fmt.Errorf("pasted data is over %d MiB; upload the file instead", MaxDataBytes>>20)
	}
	return nil
}

// Label is a short description of the input, for provenance and lists.
func (in Input) Label() string {
	switch {
	case in.URL != "":
		return in.URL
	case in.Upload != "":
		return in.Upload
	case in.Name != "":
		return "pasted " + in.Name
	}
	return "pasted data"
}

// MaxDataBytes caps pasted or fetched row data.
const MaxDataBytes = 8 << 20

// Match is one source that recognised an input.
type Match struct {
	Kind   Kind   `json:"kind"`
	Source string `json:"source"` // the source name, passed back as plan.source
	Label  string `json:"label"`  // plain words, for the chip: "Spreadsheet (CSV)"
	// Score orders matches: 100 is certain (a marker file), 50 a guess
	// from a file extension or a host name.
	Score int `json:"score"`
	// CanDraft is false for an input that is recognised but has no draft
	// path yet; Note then says what to do instead.
	CanDraft bool   `json:"can_draft"`
	Note     string `json:"note,omitempty"`
}

// Sample is one unit shown before anything is written.
type Sample struct {
	Ref    string            `json:"ref"` // "row 3", "task hello-world"
	Fields map[string]string `json:"fields"`
}

// Estimate is what drafting and readiness will cost, before they start.
type Estimate struct {
	Tasks int `json:"tasks"`
	// ReadinessRuns is the control runs readiness makes: the reference
	// three times, then the empty answer, each wrong answer and the exploit.
	ReadinessRuns int `json:"readiness_runs"`
	// JudgeCalls is the judged control runs (rubric-graded tasks only).
	JudgeCalls int `json:"judge_calls"`
	// AssistUSD is the model spend of drafting; 0: no model call is made.
	AssistUSD float64 `json:"assist_usd"`
}

// Inventory describes an input. No model call made it.
type Inventory struct {
	Kind     Kind     `json:"kind"`
	Source   string   `json:"source"`
	Title    string   `json:"title"`
	License  string   `json:"license"` // from the dataset card, repo, importer, or "your data"
	Unit     string   `json:"unit"`    // row, task, operation, commit
	Units    int      `json:"units"`
	Columns  []string `json:"columns,omitempty"`
	Samples  []Sample `json:"samples"`
	Suggest  Plan     `json:"suggest"`
	Needs    []string `json:"needs,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Estimate Estimate `json:"estimate"`
	CanDraft bool     `json:"can_draft"`
	Next     string   `json:"next"`
	// Importers are the benchmark formats that can read a folder input,
	// best guess first (plan.importer picks one).
	Importers []string `json:"importers,omitempty"`
}

// Plan is how a draft is made. Inspect suggests one; the person edits it.
type Plan struct {
	Source string `json:"source"` // the source name from Match
	Suite  string `json:"suite"`  // target benchmark id (new or existing)
	Title  string `json:"title,omitempty"`
	// Grader of a rows draft: answer_match (exact answer) or judge (rubric).
	Grader string                `json:"grader,omitempty"`
	Judge  *scaffold.JudgeConfig `json:"judge,omitempty"`
	// Match is the default answer_match mode of rows that name none.
	Match string `json:"match,omitempty"`
	// Mapping names the column each row field is read from (question,
	// expected, rubric, reference, wrong_answers, id, title, category,
	// split); the same field names as the row mapper. Empty: auto-detect.
	Mapping map[string]string `json:"mapping,omitempty"`
	// Format of rows: csv, jsonl or tickets; empty: from the file.
	Format string `json:"format,omitempty"`
	// Importer and Settings of a benchmark-format draft.
	Importer string            `json:"importer,omitempty"`
	Settings map[string]string `json:"settings,omitempty"`
	Limit    int               `json:"limit,omitempty"`
	// Split (HF) or subset the source reads.
	Split string    `json:"split,omitempty"`
	Hold  SplitPlan `json:"held_out"`
}

// SplitPlan assigns held-out tasks.
type SplitPlan struct {
	Pct  int   `json:"pct"`  // percent of tasks held out: default 25 for own data, 0 for public data
	Seed int64 `json:"seed"` // the same seed always picks the same tasks
}

// Origin is where a draft task came from.
type Origin struct {
	Source string `json:"source"`
	Ref    string `json:"ref"` // "row 12", "task hello-world"
}

// ControlInfo is one negative or positive control of a task.
type ControlInfo struct {
	Kind   string `json:"kind"`   // reference, empty, wrong_answer, exploit
	Path   string `json:"path"`   // relative to the task folder
	Origin string `json:"origin"` // source (from the data), rule (written by a fixed rule), author, assist
}

// GeneratedField names a field a model wrote (assist ops). Empty for every
// source in this package: no source here calls a model.
type GeneratedField struct {
	Field    string  `json:"field"`
	Model    string  `json:"model"`
	PromptSH string  `json:"prompt_sha"`
	CostUSD  float64 `json:"cost_usd"`
}

// GateResult is one cheap gate's verdict on a task.
type GateResult struct {
	Gate     string `json:"gate"`
	OK       bool   `json:"ok"`
	Blocking bool   `json:"blocking"`
	Message  string `json:"message,omitempty"`
}

// Review states of a draft task. Tasks drafted from the person's own data
// start accepted; tasks with a generated field start pending and never
// publish until a person accepts them.
const (
	ReviewPending  = "pending"
	ReviewAccepted = "accepted"
	ReviewRejected = "rejected"
)

// Source turns one kind of input into draft tasks.
type Source interface {
	Name() string
	Kind() Kind
	// Detect is cheap: file sniffing or a host match; no model call.
	Detect(p Peek) (Match, bool)
	// Inspect describes the input and suggests a plan; no model call and
	// nothing is written.
	Inspect(ctx context.Context, env *Env, in Input) (*Inventory, error)
	// Draft writes task folders into set's suite folder.
	Draft(ctx context.Context, env *Env, in Input, plan Plan, set *DraftSet) error
}

// ErrNoDraft marks a source that recognises an input but cannot draft it.
var ErrNoDraft = errors.New("this input cannot be drafted yet")

var (
	regMu   sync.RWMutex
	sources = map[string]Source{}
)

// Register adds a source; a duplicate name panics (a programming error).
func Register(s Source) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := sources[s.Name()]; dup {
		panic("onboard: duplicate source " + s.Name())
	}
	sources[s.Name()] = s
}

// Get returns the source named name.
func Get(name string) (Source, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	s, ok := sources[name]
	return s, ok
}

// Sources lists the registered sources by name.
func Sources() []Source {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Source, 0, len(sources))
	for _, s := range sources {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Detect peeks at in and returns every match, best first. A hint moves the
// matching kind to the front.
func Detect(ctx context.Context, env *Env, in Input) ([]Match, Peek, error) {
	if err := in.Validate(); err != nil {
		return nil, Peek{}, err
	}
	p, err := env.Peek(ctx, in)
	if err != nil {
		return nil, p, err
	}
	var out []Match
	for _, s := range Sources() {
		if m, ok := s.Detect(p); ok {
			m.Source = s.Name()
			if m.Kind == "" {
				m.Kind = s.Kind()
			}
			if in.Hint != "" && m.Kind == in.Hint {
				m.Score += 1000
			}
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Source < out[j].Source
	})
	return out, p, nil
}

// pick is the source plan names, or the best match.
func pick(ctx context.Context, env *Env, in Input, name string) (Source, error) {
	if name != "" {
		s, ok := Get(name)
		if !ok {
			return nil, fmt.Errorf("unknown source %q", name)
		}
		return s, nil
	}
	ms, _, err := Detect(ctx, env, in)
	if err != nil {
		return nil, err
	}
	if len(ms) == 0 {
		return nil, errors.New("nothing here is a benchmark Heimdall can read: drop a CSV or JSONL file, a zip of a benchmark folder, or paste a Hugging Face, GitHub or Harbor link")
	}
	s, _ := Get(ms[0].Source)
	return s, nil
}

// Inspect describes in with the source plan names (empty: the best match).
func Inspect(ctx context.Context, env *Env, in Input, source string) (*Inventory, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	s, err := pick(ctx, env, in, source)
	if err != nil {
		return nil, err
	}
	inv, err := s.Inspect(ctx, env, in)
	if err != nil {
		return nil, err
	}
	inv.Source, inv.Suggest.Source = s.Name(), s.Name()
	if inv.Kind == "" {
		inv.Kind = s.Kind()
	}
	if inv.Samples == nil {
		inv.Samples = []Sample{}
	}
	for i := range inv.Samples {
		for k, v := range inv.Samples[i].Fields {
			inv.Samples[i].Fields[k] = Redact(cut(v, 400))
		}
	}
	return inv, nil
}

var suiteIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidSuiteID reports whether id can be a benchmark id.
func ValidSuiteID(id string) bool { return suiteIDRE.MatchString(id) }

// slug is a benchmark id made from free text.
func slug(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	if s == "" || !ValidSuiteID(s) {
		s = "my-benchmark"
	}
	return s
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

var (
	emailRE = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	phoneRE = regexp.MustCompile(`\+\d[\d\s().-]{7,}\d|\(\d{3}\)\s?\d{3}[-.\s]\d{4}|\b\d{3}[-.]\d{3}[-.]\d{4}\b`)
)

// Redact masks e-mail addresses and phone numbers in sample text shown
// before anything is written. The task files keep the data as given.
func Redact(s string) string {
	s = emailRE.ReplaceAllString(s, "[email]")
	return phoneRE.ReplaceAllString(s, "[phone]")
}

// now is the clock; tests replace it.
var now = func() time.Time { return time.Now().UTC() }
