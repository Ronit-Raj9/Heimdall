// Package emit turns validated issue candidates into one private benchmark
// per repository: SWE-bench rows imported through the swebench importer,
// a scrubbed repository with fresh history, three image layers, the newest
// issues held out by date, every task pending review, and the readiness
// check queued.
package emit

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
)

// Row is one SWE-bench row as the swebench importer reads it.
type Row struct {
	InstanceID       string   `json:"instance_id"`
	Repo             string   `json:"repo"`
	BaseCommit       string   `json:"base_commit"`
	Patch            string   `json:"patch"`
	TestPatch        string   `json:"test_patch"`
	ProblemStatement string   `json:"problem_statement"`
	Version          string   `json:"version"`
	FailToPass       []string `json:"FAIL_TO_PASS"`
	PassToPass       []string `json:"PASS_TO_PASS"`
	Image            string   `json:"image"`
	TestCmds         []string `json:"test_cmds,omitempty"`
	LicenseName      string   `json:"license_name"`
	CreatedAt        string   `json:"created_at,omitempty"`
	Language         string   `json:"heimdall_language,omitempty"`
	Restore          []string `json:"heimdall_restore,omitempty"`
}

var idUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// InstanceID is the SWE-bench style id of a candidate: owner__name-<pr>.
func InstanceID(c issuebench.Candidate) string {
	return idUnsafe.ReplaceAllString(strings.ReplaceAll(c.Repo, "/", "__"), "-") + fmt.Sprintf("-%d", c.PR)
}

// SuiteID is the private benchmark of one repository: issues-<owner>-<name>,
// lowercased and limited to the characters a suite id allows.
func SuiteID(repo string) string {
	s := strings.ToLower(idUnsafe.ReplaceAllString(strings.ReplaceAll(repo, "/", "-"), "-"))
	s = strings.Trim(s, "-._")
	if s == "" {
		s = "repo"
	}
	return "issues-" + s
}

// ProblemStatement is the instruction text: the issue title and body only.
// The fix, the tests and the pull request text stay out.
func ProblemStatement(c issuebench.Candidate) string {
	title := strings.TrimSpace(c.IssueTitle)
	body := strings.TrimSpace(c.IssueBody)
	switch {
	case title == "":
		return body
	case body == "":
		return title
	}
	return title + "\n\n" + body
}

// RowOf builds the row for one validated candidate. base is the commit of
// the scrubbed repository (fresh history), image the thin per-task image.
func RowOf(v issuebench.Validation, env issuebench.EnvSpec, base, image, license string) Row {
	if license == "" {
		license = LicenseUnknown
	}
	c := v.Candidate
	r := Row{
		InstanceID:       InstanceID(c),
		Repo:             c.Repo,
		BaseCommit:       base,
		Patch:            c.FixPatch,
		TestPatch:        c.TestPatch,
		ProblemStatement: ProblemStatement(c),
		Version:          env.Version,
		FailToPass:       append([]string{}, v.FailToPass...),
		PassToPass:       append([]string{}, v.PassToPass...),
		Image:            image,
		LicenseName:      license,
	}
	if cmd := TestCmd(env, c); cmd != "" {
		r.TestCmds = []string{cmd}
	}
	r.Language = env.Language
	if r.Language == "" {
		r.Language = c.Language
	}
	r.Restore = issuebench.ToolchainOf(issuebench.LanguageFor(r.Language)).ProtectedFiles()
	if !c.IssueCreated.IsZero() {
		r.CreatedAt = c.IssueCreated.UTC().Format(time.RFC3339)
	}
	return r
}

// Usable says why a validation cannot be emitted, or "".
func Usable(v issuebench.Validation) string {
	switch {
	case v.Stop != "":
		return "validation stopped: " + v.Stop
	case v.Candidate.Skip != "":
		return "candidate skipped: " + v.Candidate.Skip
	case len(v.FailToPass) == 0:
		return "no test fails before the fix and passes after it"
	case v.Checks.Leak:
		return "the issue text gives the fix away"
	case strings.TrimSpace(v.Candidate.FixPatch) == "" || strings.TrimSpace(v.Candidate.TestPatch) == "":
		return "fix or test patch is empty"
	}
	return ""
}

// JUnitFlag makes pytest write the JUnit XML the swebench grader reads
// (BENCH_JUNIT), so grading uses JUnit, not the log only.
const JUnitFlag = `--junitxml="${BENCH_JUNIT:-/tmp/bench-junit.xml}"`

// TestCmd is the task's test command: the registered Language's
// TestCommand when there is one, else the EnvSpec's; a pytest command
// without a JUnit report gets JUnitFlag.
func TestCmd(env issuebench.EnvSpec, c issuebench.Candidate) string {
	lang := env.Language
	if lang == "" {
		lang = c.Language
	}
	cmd := ""
	for _, l := range issuebench.Languages() {
		if l.Name() == lang {
			cmd = l.TestCommand(nil)
			break
		}
	}
	if strings.TrimSpace(cmd) == "" {
		cmd = env.TestCmd
	}
	cmd = strings.TrimSpace(cmd)
	if strings.Contains(cmd, "pytest") && !strings.Contains(cmd, "--junitxml") {
		cmd += " " + JUnitFlag
	}
	return cmd
}
