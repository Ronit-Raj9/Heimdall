package scaffold

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// First workflow: `tasks mine` (docs/tasks-from-your-repo.md). Mining reads
// git history with the same read-only plumbing as task init, picks commits
// that change both code and tests, and drafts each as a task: base = the
// parent, reference = the commit, hidden tests = the test files the commit
// added or changed. Proof (hidden tests fail at base, pass at the reference)
// is the existing admission, run by the command; this file never runs Docker
// and never writes to the repository.

// Mining defaults.
const (
	MineDefaultLimit    = 10
	MineDefaultScan     = 300 // commits looked at, newest first
	MineDefaultMaxFiles = 12  // changed files above this make a huge diff
	MineDefaultMaxLines = 400 // changed lines above this make a huge diff
	MinedBy             = "tasks mine"
)

// Skip reasons a candidate can carry. Each says why the commit is not a task.
const (
	SkipMerge     = "merge commit"
	SkipRoot      = "root commit (no parent to start from)"
	SkipNoTests   = "changes no test file"
	SkipOnlyTests = "changes only test files (no reference solution)"
	SkipFewTests  = "changes fewer test files than -min-tests"
	SkipPaths     = "changes nothing under -paths"
	SkipBinary    = "changes a binary file"
	SkipHuge      = "huge diff"
	SkipNetwork   = "tests need the network (the verifier has none)"
	SkipSecrets   = "tests need secrets"
	SkipOutside   = "changes paths outside the mined folder"
	SkipDeleted   = "deletes a test file"
)

// MineOptions configures MineCandidates.
type MineOptions struct {
	Repo      string   // git work tree, or a folder inside one
	Since     string   // a commit, tag or branch (mine commits after it) or a date (YYYY-MM-DD)
	Paths     []string // repository folders; a commit must change something under one of them
	MinTests  int      // hidden test files a commit must change; default 1
	TestGlobs []string // which files are tests
	Scan      int      // commits to look at; default MineDefaultScan
	MaxFiles  int      // default MineDefaultMaxFiles
	MaxLines  int      // default MineDefaultMaxLines
	// Exclude is a prefix-relative folder left out of the change (the
	// .heimdall project inside the repository).
	Exclude string
}

// Candidate is one commit looked at, with a skip reason or a rank score.
type Candidate struct {
	Commit, Parent string
	Subject, Body  string
	When           time.Time
	Source, Tests  []string // changed non-test files and hidden test files (prefix-relative)
	Lines          int      // added plus deleted lines over the whole change
	TestNames      []string // test functions defined in the hidden test files
	Area           string   // top folder of the first source file ("." at the root)
	Score          float64  // lower is better
	Skip           string   // "" for a candidate
	SkipDetail     string
}

// ShortCommit is the commit's first 12 hex digits.
func (c Candidate) ShortCommit() string { return shortSHA(c.Commit) }

// TaskID is the id a mined task gets: "m-" and the first 10 hex digits.
func (c Candidate) TaskID() string {
	n := min(10, len(c.Commit))
	return "m-" + c.Commit[:n]
}

// MineCandidates walks history newest first and classifies each commit.
// The candidates (Skip == "") come back ranked best first by Rank; the
// skipped ones follow in history order. Nothing is written.
func MineCandidates(ctx context.Context, opts MineOptions) ([]Candidate, []Candidate, error) {
	repo, err := openRepo(ctx, opts.Repo)
	if err != nil {
		return nil, nil, err
	}
	if opts.MinTests <= 0 {
		opts.MinTests = 1
	}
	if opts.Scan <= 0 {
		opts.Scan = MineDefaultScan
	}
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = MineDefaultMaxFiles
	}
	if opts.MaxLines <= 0 {
		opts.MaxLines = MineDefaultMaxLines
	}
	commits, err := repo.history(ctx, opts.Since, opts.Scan)
	if err != nil {
		return nil, nil, err
	}
	var good, skipped []Candidate
	for _, hc := range commits {
		c, err := repo.classifyCommit(ctx, hc, opts)
		if err != nil {
			return nil, nil, err
		}
		if c.Skip != "" {
			skipped = append(skipped, c)
			continue
		}
		good = append(good, c)
	}
	return Rank(good), skipped, nil
}

// historyCommit is one line of git log.
type historyCommit struct {
	sha     string
	parents []string
	when    time.Time
}

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

// history lists commits reachable from HEAD, newest first, at most n. since
// is a date (commits after it) or a revision (commits not reachable from it).
func (r *gitRepo) history(ctx context.Context, since string, n int) ([]historyCommit, error) {
	args := []string{"log", "--format=%H%x00%P%x00%ct", "--topo-order", "-n", strconv.Itoa(n)}
	rng := "HEAD"
	var after time.Time
	switch {
	case since == "":
	case dateRE.MatchString(since):
		// Filtered here, not with git's --since, whose date parser
		// silently ignores some dates.
		t, err := time.Parse("2006-01-02", since[:10])
		if err != nil {
			return nil, fmt.Errorf("-since %q: %w", since, err)
		}
		after = t
	default:
		sha, err := r.resolve(ctx, since)
		if err != nil {
			return nil, fmt.Errorf("-since: %w", err)
		}
		rng = sha + "..HEAD"
	}
	args = append(args, rng, "--")
	out, err := r.run(ctx, nil, args...)
	if err != nil {
		return nil, err
	}
	var commits []historyCommit
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\x00")
		if len(f) != 3 {
			continue
		}
		secs, _ := strconv.ParseInt(f[2], 10, 64)
		when := time.Unix(secs, 0).UTC()
		if !after.IsZero() && when.Before(after) {
			continue
		}
		commits = append(commits, historyCommit{sha: f[0], parents: strings.Fields(f[1]), when: when})
	}
	return commits, nil
}

// numstat returns lines added plus deleted per changed path, and the paths
// git calls binary.
func (r *gitRepo) numstat(ctx context.Context, base, ref string) (int, []string, error) {
	out, err := r.run(ctx, nil, "diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", base, ref, "--")
	if err != nil {
		return 0, nil, err
	}
	total := 0
	var bins []string
	for _, rec := range bytes.Split(out, []byte{0}) {
		f := strings.SplitN(string(rec), "\t", 3)
		if len(f) != 3 {
			continue
		}
		if f[0] == "-" && f[1] == "-" {
			bins = append(bins, f[2])
			continue
		}
		a, _ := strconv.Atoi(f[0])
		d, _ := strconv.Atoi(f[1])
		total += a + d
	}
	return total, bins, nil
}

func (r *gitRepo) classifyCommit(ctx context.Context, hc historyCommit, opts MineOptions) (Candidate, error) {
	c := Candidate{Commit: hc.sha, When: hc.when}
	msg, err := r.message(ctx, hc.sha)
	if err != nil {
		return c, err
	}
	c.Subject, c.Body = SplitMessage(msg)
	switch len(hc.parents) {
	case 0:
		c.Skip = SkipRoot
		return c, nil
	case 1:
		c.Parent = hc.parents[0]
	default:
		c.Skip = SkipMerge
		return c, nil
	}
	changes, outside, err := r.changes(ctx, c.Parent, c.Commit)
	if err != nil {
		return c, err
	}
	if len(outside) > 0 {
		c.Skip, c.SkipDetail = SkipOutside, listSome(outside)
		return c, nil
	}
	var deleted []string
	for _, ch := range changes {
		if opts.Exclude != "" && (ch.Path == opts.Exclude || strings.HasPrefix(ch.Path, opts.Exclude+"/")) {
			continue
		}
		test := MatchAny(opts.TestGlobs, ch.Path)
		switch {
		case test && ch.Status == "D":
			deleted = append(deleted, ch.Path)
		case test:
			c.Tests = append(c.Tests, ch.Path)
		default:
			c.Source = append(c.Source, ch.Path)
		}
	}
	sort.Strings(c.Tests)
	sort.Strings(c.Source)
	if len(opts.Paths) > 0 && !touchesAny(append(append([]string{}, c.Source...), c.Tests...), opts.Paths) {
		c.Skip, c.SkipDetail = SkipPaths, strings.Join(opts.Paths, ",")
		return c, nil
	}
	switch {
	case len(deleted) > 0:
		c.Skip, c.SkipDetail = SkipDeleted, listSome(deleted)
		return c, nil
	case len(c.Tests) == 0:
		c.Skip = SkipNoTests
		return c, nil
	case len(c.Source) == 0:
		c.Skip = SkipOnlyTests
		return c, nil
	case len(c.Tests) < opts.MinTests:
		c.Skip, c.SkipDetail = SkipFewTests, fmt.Sprintf("%d < %d", len(c.Tests), opts.MinTests)
		return c, nil
	}
	lines, bins, err := r.numstat(ctx, c.Parent, c.Commit)
	if err != nil {
		return c, err
	}
	c.Lines = lines
	if len(bins) > 0 {
		c.Skip, c.SkipDetail = SkipBinary, listSome(bins)
		return c, nil
	}
	if files := len(c.Source) + len(c.Tests); files > opts.MaxFiles || lines > opts.MaxLines {
		c.Skip, c.SkipDetail = SkipHuge, fmt.Sprintf("%d files, %d lines (limits %d files, %d lines)", files, lines, opts.MaxFiles, opts.MaxLines)
		return c, nil
	}
	tree, err := r.tree(ctx, c.Commit)
	if err != nil {
		return c, err
	}
	isTest := map[string]bool{}
	for _, p := range c.Tests {
		isTest[p] = true
	}
	isSource := map[string]bool{}
	for _, p := range c.Source {
		isSource[p] = true
	}
	var entries []treeEntry
	for _, e := range tree {
		if (isTest[e.Path] || isSource[e.Path]) && e.Type == "blob" {
			entries = append(entries, e)
		}
	}
	var testSrc [][]byte
	var testPaths []string
	sourceNet := ""
	err = r.catBlobs(ctx, shasOf(entries), func(sha string, data []byte) error {
		for _, e := range entries {
			if e.SHA != sha {
				continue
			}
			if isSource[e.Path] {
				if why := networkCall(data); why != "" && sourceNet == "" {
					sourceNet = e.Path + ": " + why
				}
				continue
			}
			testSrc, testPaths = append(testSrc, data), append(testPaths, e.Path)
			c.TestNames = append(c.TestNames, TestNames(e.Path, data)...)
		}
		return nil
	})
	if err != nil {
		return c, err
	}
	for i, data := range testSrc {
		if why := NeedsNetwork(data); why != "" {
			c.Skip, c.SkipDetail = SkipNetwork, testPaths[i]+": "+why
		} else if url := remoteURL(data); url != "" && sourceNet != "" {
			c.Skip, c.SkipDetail = SkipNetwork, testPaths[i]+" calls "+url+" through "+sourceNet
		} else if why := NeedsSecrets(data); why != "" {
			c.Skip, c.SkipDetail = SkipSecrets, testPaths[i]+": "+why
		}
		if c.Skip != "" {
			break
		}
	}
	c.TestNames = dedupe(c.TestNames)
	c.Area = area(c.Source)
	return c, nil
}

func touchesAny(paths, prefixes []string) bool {
	for _, p := range paths {
		for _, pre := range prefixes {
			pre = strings.Trim(filepath.ToSlash(pre), "/")
			if pre == "" || pre == "." || p == pre || strings.HasPrefix(p, pre+"/") {
				return true
			}
		}
	}
	return false
}

func area(source []string) string {
	if len(source) == 0 {
		return "."
	}
	first, _, found := strings.Cut(source[0], "/")
	if !found {
		return "."
	}
	return first
}

var (
	// networkRE flags test code that reaches out: HTTP clients, sockets and
	// absolute URLs that are not loopback.
	networkRE = regexp.MustCompile(`\b(urllib\.request|urlopen|requests\.(get|post|put|delete|head|Session)|httpx\.|aiohttp|socket\.create_connection|http\.client|fetch\(\s*["'` + "`" + `]https?://|net/http"|http\.Get\(|reqwest::|Net::HTTP|HttpClient)`)
	urlRE     = regexp.MustCompile(`https?://([a-zA-Z0-9.-]+)`)
	// secretRE flags tests that read credentials from the environment.
	secretRE = regexp.MustCompile(`(?i)(getenv|environ(\.get)?|process\.env|ENV\[|System\.getenv)\W{0,4}["'\[]?\.?([A-Z0-9_]*(API_KEY|TOKEN|SECRET|PASSWORD|CREDENTIALS)[A-Z0-9_]*)`)
)

// NeedsNetwork names what in a test file needs the network, or "": an HTTP
// client or socket call, or a remote URL next to "connect" or "download". The
// verifier runs with no network, so such a test cannot be proved. A remote
// URL alone is not enough (a URL parser's tests are full of them); the miner
// also flags one when the commit's code makes a network call (remoteURL,
// networkCall).
func NeedsNetwork(src []byte) string {
	if why := networkCall(src); why != "" {
		return why
	}
	if url := remoteURL(src); url != "" && (bytes.Contains(src, []byte("connect")) || bytes.Contains(src, []byte("download"))) {
		return url
	}
	return ""
}

// networkCall names the first HTTP client or socket call in src, or "".
func networkCall(src []byte) string {
	if m := networkRE.Find(src); m != nil {
		return string(m)
	}
	return ""
}

// remoteURL returns the first http(s) URL in src whose host is not
// loopback or a reserved test domain, or "".
func remoteURL(src []byte) string {
	for _, m := range urlRE.FindAllSubmatch(src, -1) {
		host := strings.ToLower(string(m[1]))
		if host == "localhost" || strings.HasPrefix(host, "127.") || host == "0.0.0.0" || strings.HasSuffix(host, ".invalid") || strings.HasSuffix(host, ".test") {
			continue
		}
		return string(m[0])
	}
	return ""
}

// NeedsSecrets names the credential a test file reads from its environment,
// or "".
func NeedsSecrets(src []byte) string {
	if m := secretRE.FindSubmatch(src); m != nil {
		return string(m[3])
	}
	return ""
}

var (
	pyTestNameRE = regexp.MustCompile(`(?m)^\s*(?:async\s+)?def\s+(test\w*)\s*\(`)
	goTestNameRE = regexp.MustCompile(`(?m)^func\s+(Test\w*)\s*\(`)
	jsTestNameRE = regexp.MustCompile(`(?m)\b(?:test|it)\s*\(\s*(['"` + "`" + `])([^'"` + "`" + `]+)['"` + "`" + `]`)
	rsTestNameRE = regexp.MustCompile(`(?m)#\[test\]\s*\n\s*(?:pub\s+)?fn\s+(\w+)`)
	rbTestNameRE = regexp.MustCompile(`(?m)\bit\s+['"]([^'"]+)['"]`)
)

// TestNames lists the names of the tests a test file defines, in order.
func TestNames(p string, src []byte) []string {
	var out []string
	add := func(re *regexp.Regexp, group int) {
		for _, m := range re.FindAllSubmatch(src, -1) {
			out = append(out, string(m[group]))
		}
	}
	switch ext := path.Ext(p); ext {
	case ".py":
		add(pyTestNameRE, 1)
	case ".go":
		add(goTestNameRE, 1)
	case ".js", ".mjs", ".cjs", ".ts", ".mts", ".cts", ".jsx", ".tsx":
		add(jsTestNameRE, 2)
	case ".rs":
		add(rsTestNameRE, 1)
	case ".rb":
		add(rbTestNameRE, 1)
	}
	return out
}

// Rank orders candidates best first: small diffs, few files and few test
// functions (fast to verify) score low, and areas are interleaved so the
// first picks come from different folders.
func Rank(cands []Candidate) []Candidate {
	for i := range cands {
		c := &cands[i]
		c.Score = float64(c.Lines) + 15*float64(len(c.Source)+len(c.Tests)) + 5*float64(len(c.TestNames))
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Score != cands[j].Score {
			return cands[i].Score < cands[j].Score
		}
		return cands[i].When.After(cands[j].When)
	})
	// Round-robin over areas, each area's best first.
	byArea := map[string][]Candidate{}
	var order []string
	for _, c := range cands {
		if _, ok := byArea[c.Area]; !ok {
			order = append(order, c.Area)
		}
		byArea[c.Area] = append(byArea[c.Area], c)
	}
	out := make([]Candidate, 0, len(cands))
	for len(out) < len(cands) {
		for _, a := range order {
			if len(byArea[a]) > 0 {
				out = append(out, byArea[a][0])
				byArea[a] = byArea[a][1:]
			}
		}
	}
	return out
}

// conventionalRE strips a conventional-commit type ("fix(parser): ").
var conventionalRE = regexp.MustCompile(`^(?i)(fix|bug|bugfix|feat|feature|perf|refactor|chore)(\([^)]*\))?!?:\s*`)

// fixHowRE matches body lines that say how the change was made rather than
// what was wrong; they are left out of a mined instruction.
var fixHowRE = regexp.MustCompile(`(?i)\b(use|uses|using|instead|switch(ed|es)? to|replac(e|ed|es|ing)|fix(ed)? by|by (adding|using|calling|moving|changing)|the fix|now (uses|calls|does|returns)|first;|expand|patch(ed)?|refactor(ed)?|implement(ed|s)? (it )?(with|via))\b`)

// MinedInstruction drafts instruction.md for a mined task from the commit
// message and the names of the hidden tests, without the diff and without
// sentences that describe how the fix was made. It carries no EDIT ME marker:
// the task is marked instruction_reviewed: false instead, so it can be proved
// and run at once while the report counts it as unreviewed.
func MinedInstruction(subject, body string, testNames []string) []byte {
	title := strings.TrimSpace(conventionalRE.ReplaceAllString(subject, ""))
	if title == "" {
		title = "Fix the failing behaviour"
	}
	title = upperFirst(title)
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", title)
	var kept []string
	for _, para := range strings.Split(body, "\n\n") {
		var sentences []string
		for _, s := range splitSentences(strings.Join(strings.Fields(para), " ")) {
			if s != "" && !fixHowRE.MatchString(s) {
				sentences = append(sentences, s)
			}
		}
		if len(sentences) > 0 {
			para := strings.Join(sentences, " ")
			// A dropped "how" clause after a semicolon leaves the sentence open.
			if strings.HasSuffix(para, ";") || strings.HasSuffix(para, ",") {
				para = para[:len(para)-1] + "."
			}
			kept = append(kept, para)
		}
	}
	if len(kept) > 0 {
		b.WriteString("\n" + strings.Join(kept, "\n\n") + "\n")
	}
	if len(testNames) > 0 {
		b.WriteString("\nWhen you are done, this behaviour must hold:\n\n")
		seen := map[string]bool{}
		for _, n := range testNames {
			phrase := testPhrase(n)
			if phrase == "" || seen[phrase] {
				continue
			}
			seen[phrase] = true
			fmt.Fprintf(&b, "- %s\n", phrase)
		}
	}
	b.WriteString("\nKeep the existing behaviour and tests passing.\n")
	return []byte(b.String())
}

var sentenceEndRE = regexp.MustCompile(`([.!?;])\s+`)

func splitSentences(s string) []string {
	s = sentenceEndRE.ReplaceAllString(s, "$1\n")
	return strings.Split(s, "\n")
}

// testPhrase turns a test name into words: "test_no_edge_hyphens" -> "no
// edge hyphens", "TestParseHours" -> "parse hours"; a JS or RSpec
// description is kept as written.
func testPhrase(name string) string {
	if strings.ContainsRune(name, ' ') {
		return strings.TrimSpace(name)
	}
	n := strings.TrimPrefix(strings.TrimPrefix(name, "test_"), "Test")
	n = strings.TrimPrefix(n, "test")
	var words []string
	for _, part := range strings.Split(n, "_") {
		words = append(words, splitCamel(part)...)
	}
	return strings.ToLower(strings.TrimSpace(strings.Join(words, " ")))
}

func splitCamel(s string) []string {
	var out []string
	start := 0
	r := []rune(s)
	for i := 1; i < len(r); i++ {
		if unicode.IsUpper(r[i]) && (unicode.IsLower(r[i-1]) || (i+1 < len(r) && unicode.IsLower(r[i+1]))) {
			out = append(out, string(r[start:i]))
			start = i
		}
	}
	if start < len(r) {
		out = append(out, string(r[start:]))
	}
	return out
}

func upperFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// MarkMined rewrites a task folder task init wrote for a mined commit: the
// drafted instruction replaces the EDIT ME draft, task.yaml says
// created_by: tasks mine and instruction_reviewed: false, and a
// generic-reward test.sh runs the repository's test command instead of its
// EDIT ME placeholder.
func MarkMined(taskDir string, instruction []byte, testCmd string) error {
	testSh := filepath.Join(taskDir, "verifier", "tests", "test.sh")
	_, err := os.Stat(testSh)
	hasTestSh := err == nil
	if hasTestSh && strings.TrimSpace(testCmd) == "" {
		return fmt.Errorf("%s needs the repository's test command; set tests.command in .heimdall/heimdall.yaml", testSh)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "instruction.md"), instruction, 0o644); err != nil {
		return err
	}
	p := filepath.Join(taskDir, "task.yaml")
	raw, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	out := bytes.Replace(raw, []byte("# Written by `bifrost-bench task init`"), []byte("# Written by `heimdall tasks mine` (task init machinery)"), 1)
	out = bytes.Replace(out, []byte("  created_by: task init"), []byte("  created_by: "+MinedBy+"\n  instruction_reviewed: false"), 1)
	if bytes.Equal(out, raw) || !bytes.Contains(out, []byte("instruction_reviewed: false")) {
		return fmt.Errorf("%s: could not mark the task as mined", p)
	}
	if err := os.WriteFile(p, out, 0o644); err != nil {
		return err
	}
	if hasTestSh {
		if err := os.WriteFile(testSh, []byte(RewardScript(testCmd)), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// RewardScript is a generic-reward verifier that runs the repository's own
// test command over the workspace with the hidden test files laid over it.
func RewardScript(testCmd string) string {
	return `#!/bin/sh
# Verifier written by ` + "`heimdall tasks mine`" + `: the repository's own test command,
# run with no network on a copy of the workspace with this task's hidden test
# files laid over it. Reward 1 when the command exits 0.
set -u
w=/tmp/bench-verify
mkdir -p "$w"
cp -R /workspace/. "$w"/
cp -R /grader/tests/. "$w"/
rm -f "$w/test.sh"
cd "$w"
if ` + testCmd + `; then
	reward=1
else
	reward=0
fi
printf '{"reward": %s}\n' "$reward" > /grader/out/reward.json
`
}

// SetInstructionReviewed sets provenance.instruction_reviewed in a task.yaml
// to true with the smallest textual edit: the line is rewritten when present
// and added under provenance: otherwise. It reports whether anything changed.
func SetInstructionReviewed(taskYAML []byte) ([]byte, bool, error) {
	if bytes.Contains(taskYAML, []byte("instruction_reviewed: true")) {
		return taskYAML, false, nil
	}
	if bytes.Contains(taskYAML, []byte("instruction_reviewed: false")) {
		return bytes.Replace(taskYAML, []byte("instruction_reviewed: false"), []byte("instruction_reviewed: true"), 1), true, nil
	}
	idx := bytes.Index(taskYAML, []byte("\nprovenance:\n"))
	if idx < 0 {
		return nil, false, fmt.Errorf("task.yaml has no provenance block to record the review in")
	}
	at := idx + len("\nprovenance:\n")
	return splice(taskYAML, at, at, "  instruction_reviewed: true\n"), true, nil
}
