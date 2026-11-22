// Package issuebench turns merged pull requests that close GitHub issues into
// benchmark tasks: scan, build an environment, validate, emit.
package issuebench

import (
	"sort"
	"sync"
	"time"
)

// Candidate is one merged pull request that closes one issue.
type Candidate struct {
	Repo         string    `json:"repo"`
	PR           int       `json:"pr"`
	Issue        int       `json:"issue"`
	IssueTitle   string    `json:"issue_title"`
	IssueBody    string    `json:"issue_body"`
	IssueCreated time.Time `json:"issue_created"`
	PRMerged     time.Time `json:"pr_merged"`
	BaseSHA      string    `json:"base_sha"`
	MergeSHA     string    `json:"merge_sha"`
	TestFiles    []string  `json:"test_files"`
	CodeFiles    []string  `json:"code_files"`
	TestPatch    string    `json:"test_patch"`
	FixPatch     string    `json:"fix_patch"`
	Language     string    `json:"language"`
	Skip         string    `json:"skip,omitempty"`
}

// Funnel counts how many pull requests survived each selection step.
type Funnel struct {
	MergedPRs int            `json:"merged_prs"`
	Linked    int            `json:"linked"`
	Qualified int            `json:"qualified"`
	Reasons   map[string]int `json:"reasons"`
}

// TestStatus is the result of one test.
type TestStatus string

const (
	TestPass  TestStatus = "pass"
	TestFail  TestStatus = "fail"
	TestError TestStatus = "error"
	TestSkip  TestStatus = "skip"
)

// Language knows how to recognise, install and test repositories of one
// ecosystem.
type Language interface {
	Name() string
	Detect(repoDir string) bool
	IsTestFile(path string) bool
	BaseImages() []string
	Hints(repoDir string) []string
	PinEnv(at time.Time) []string
	TestCommand(tests []string) string
	ParseResults(output []byte) (map[string]TestStatus, error)
}

var (
	langMu sync.Mutex
	langs  = map[string]Language{}
)

// Register adds a language; packages call it from init.
func Register(l Language) {
	langMu.Lock()
	defer langMu.Unlock()
	langs[l.Name()] = l
}

// Languages returns the registered languages sorted by name.
func Languages() []Language {
	langMu.Lock()
	defer langMu.Unlock()
	out := make([]Language, 0, len(langs))
	for _, l := range langs {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// StopReason says why a build ended.
type StopReason string

const (
	StopOK              StopReason = "ok"
	StopNeedsGPU        StopReason = "needs_gpu"
	StopNeedsService    StopReason = "needs_service"
	StopNeedsSecret     StopReason = "needs_secret"
	StopPrivateRegistry StopReason = "private_registry"
	StopLoop            StopReason = "loop"
	StopBudget          StopReason = "budget"
	StopTimeout         StopReason = "timeout"
	StopUnsupported     StopReason = "unsupported_language"
)

// EnvSpec is a built, reusable environment for one repository version.
type EnvSpec struct {
	Repo       string   `json:"repo"`
	Version    string   `json:"version"`
	Language   string   `json:"language"`
	BaseImage  string   `json:"base_image"`
	Commands   []string `json:"commands"`
	Dockerfile string   `json:"dockerfile"`
	TestCmd    string   `json:"test_cmd"`
	ImageRef   string   `json:"image_ref,omitempty"`
}

// BuildResult is the outcome of one builder run.
type BuildResult struct {
	Env     *EnvSpec   `json:"env,omitempty"`
	Stop    StopReason `json:"stop"`
	Detail  string     `json:"detail,omitempty"`
	Steps   int        `json:"steps"`
	CostUSD float64    `json:"cost_usd"`
	LogPath string     `json:"log_path,omitempty"`
}

// LLMChecks are the model's judgements about one candidate.
type LLMChecks struct {
	Leak          bool     `json:"leak"`
	Clear         bool     `json:"clear"`
	InterfaceNote string   `json:"interface_note,omitempty"`
	WrongPatches  []string `json:"wrong_patches,omitempty"`
}

// Validation is what validation found for one candidate.
type Validation struct {
	Candidate  Candidate `json:"candidate"`
	FailToPass []string  `json:"fail_to_pass"`
	PassToPass []string  `json:"pass_to_pass"`
	Flaky      []string  `json:"flaky,omitempty"`
	Checks     LLMChecks `json:"checks"`
	Stop       string    `json:"stop,omitempty"`
	// Ablations are the fix's hunk ablations the worker ran (hosted): only
	// the verified wrong ones become near-miss controls.
	Ablations *AblationReport `json:"ablations,omitempty"`
}

// AblationReport is what running the fix's hunk ablations found: Patches
// are the kept ones (at least one FAIL_TO_PASS test fails, or a
// PASS_TO_PASS test breaks); a dropped one passed (a behaviour-neutral hunk
// such as a refactor) or did not apply.
type AblationReport struct {
	Tried   int      `json:"tried"`
	Kept    int      `json:"kept"`
	Dropped int      `json:"dropped"`
	Patches []string `json:"patches,omitempty"`
}
