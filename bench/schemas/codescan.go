package schemas

// Code scan (X4, docs/code-scan.md): security findings on the lines a run's
// agent added, read from the sealed diff by the rules in bench/codescan, plus
// the results of a suite's optional external scanners on the same lines.
// Every field here is optional and omitted when empty, so manifests, locks
// and seals written before the code scan keep their hashes.

// Code scan severities, lowest first. A finding's severity is the rule's.
const (
	CodeScanLow      = "low"
	CodeScanMedium   = "medium"
	CodeScanHigh     = "high"
	CodeScanCritical = "critical"
)

// CodeScanSeverities lists the severities in increasing order.
var CodeScanSeverities = []string{CodeScanLow, CodeScanMedium, CodeScanHigh, CodeScanCritical}

// CodeScanSeverityRank orders a severity: 1 for low up to 4 for critical, 0
// for anything else.
func CodeScanSeverityRank(s string) int {
	for i, v := range CodeScanSeverities {
		if v == s {
			return i + 1
		}
	}
	return 0
}

// CodeScanPolicy is an experiment's code scan setting (Experiment.CodeScan).
// With no setting the insecure_code_added gate only reports. VetoAt makes a
// run with a counted finding at or above that severity an integrity
// violation. Ignore names rules (builtin ids, or "<scanner>:<rule>" for an
// external scanner) whose findings are listed but never counted. The setting
// is frozen in the manifest with the rest of the experiment, so a regate
// reaches the same result.
type CodeScanPolicy struct {
	VetoAt string   `json:"veto_at,omitempty" yaml:"veto_at,omitempty"` // low | medium | high | critical
	Ignore []string `json:"ignore,omitempty" yaml:"ignore,omitempty"`
}

// SuiteCodeScan is a suite's code scan setting (Suite.CodeScan): optional
// external scanners run on the post-run tree in the clean room. None runs
// unless the suite declares it.
type SuiteCodeScan struct {
	External []CodeScanner `json:"external,omitempty" yaml:"external,omitempty"`
}

// CodeScanner is one external scanner (semgrep, bandit, gosec, ...). It runs
// after the agent has exited, in a fresh container with no network: the
// pristine repository plus the agent's diff, read-only at /workspace, and an
// empty /grader/out. It must write SARIF 2.1.0 to SARIF, a path under
// /grader/out. Image is "base" (the suite's base image, pinned by the suite
// lock) or a reference pinned by digest (name@sha256:<64 hex>).
type CodeScanner struct {
	ID       string   `json:"id" yaml:"id"`
	Image    string   `json:"image" yaml:"image"`
	Command  []string `json:"command" yaml:"command"`
	SARIF    string   `json:"sarif" yaml:"sarif"`
	TimeoutS int      `json:"timeout_s,omitempty" yaml:"timeout_s,omitempty"`
}

// CodeScanEvidence is one run's code scan (Evidence.CodeScan, also in
// evidence.json). Scanned is false when the diff could not be read, with the
// reason; a run with an empty diff is scanned and has no findings.
type CodeScanEvidence struct {
	RulesVersion string             `json:"rules_version"`
	Scanned      bool               `json:"scanned"`
	Reason       string             `json:"reason,omitempty"`
	DiffSHA256   string             `json:"diff_sha256,omitempty"`
	Files        int                `json:"files"`       // files with added lines that were scanned
	AddedLines   int                `json:"added_lines"` // added lines that were scanned
	Skipped      []CodeScanSkip     `json:"skipped,omitempty"`
	Findings     []CodeScanFinding  `json:"findings"`
	External     []CodeScanExternal `json:"external,omitempty"`
}

// CodeScanSkip is a changed file the scan did not read, with the reason
// (binary, too_large, deleted).
type CodeScanSkip struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// CodeScanFinding is one security finding on a line the agent added (or, for
// a rule on removed lines, a protection the agent removed). Line is the line
// number in the post-run file (in the pre-run file when Removed), PatchLine
// the 1-based line of workspace.diff that holds it. Snippet is the line with
// secrets redacted, at most 160 characters. Source is "builtin" or the
// external scanner's id.
type CodeScanFinding struct {
	Rule      string `json:"rule"`
	Title     string `json:"title"`
	CWE       string `json:"cwe,omitempty"`
	Severity  string `json:"severity"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Removed   bool   `json:"removed,omitempty"`
	PatchLine int    `json:"patch_line,omitempty"`
	Snippet   string `json:"snippet,omitempty"`
	Source    string `json:"source"`
	InTest    bool   `json:"in_test,omitempty"`
}

// CodeScanExternal records one external scanner's run: its exit code, the
// sha256 of the SARIF it wrote, how many results it reported and how many
// fell on lines the agent added. Error is set when it did not run or its
// SARIF could not be read; its findings are then unknown, never zero.
type CodeScanExternal struct {
	Scanner     string `json:"scanner"`
	Image       string `json:"image"`
	ExitCode    int    `json:"exit_code"`
	SARIFSHA256 string `json:"sarif_sha256,omitempty"`
	Results     int    `json:"results"`
	OnAdded     int    `json:"on_added_lines"`
	Error       string `json:"error,omitempty"`
}

// CodeScanReport is GET /runs/{id}/codescan: the run's findings with where
// they came from. Sealed is true when they are the run's sealed code scan
// evidence at the current rules version; otherwise the builtin rules were run
// again now on the run's sealed diff and RulesVersion says which rules. VetoAt
// is the experiment's veto level, and Counted the findings that count (not
// ignored); Vetoed says whether the gate vetoes the run under that level.
type CodeScanReport struct {
	RunID   string            `json:"run_id"`
	Sealed  bool              `json:"sealed"`
	VetoAt  string            `json:"veto_at,omitempty"`
	Ignored []string          `json:"ignored_rules,omitempty"`
	Counted int               `json:"counted"`
	Vetoed  bool              `json:"vetoed"`
	Scan    *CodeScanEvidence `json:"scan"`
}

// CodeScanRule describes one builtin rule for `codescan rules` and the API.
type CodeScanRule struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	CWE       string   `json:"cwe"`
	Severity  string   `json:"severity"`
	Languages []string `json:"languages"`
	Removed   bool     `json:"removed_lines,omitempty"` // the rule reads removed lines
	SkipTests bool     `json:"skip_tests,omitempty"`    // not applied to test files
	Summary   string   `json:"summary"`
}
