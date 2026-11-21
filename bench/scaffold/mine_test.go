package scaffold

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/suite"
)

// mineRepo is a Python repository whose history holds two good fix commits
// in different folders and one commit of every kind the miner must skip.
func mineRepo(t *testing.T) (*testRepo, map[string]string) {
	t.Helper()
	r := newTestRepo(t)
	ids := map[string]string{}
	r.write("calc/__init__.py", "")
	r.write("calc/core.py", "def add(a, b):\n    return abs(a) + abs(b)\n")
	r.write("text/__init__.py", "")
	r.write("text/slug.py", "def slug(s):\n    return s.lower().replace(' ', '-').strip()\n")
	r.write("tests/test_core.py", "from calc.core import add\n\n\ndef test_pos():\n    assert add(2, 3) == 5\n")
	r.write("tests/test_old.py", "def test_old():\n    assert True\n")
	ids["root"] = r.commit("Initial")

	r.write("calc/core.py", "def add(a, b):\n    return a + b\n")
	r.write("tests/test_core.py", "from calc.core import add\n\n\ndef test_pos():\n    assert add(2, 3) == 5\n\n\ndef test_keeps_sign():\n    assert add(-2, 3) == 1\n")
	ids["calc"] = r.commit("fix(calc): add drops the sign\n\nadd(-2, 3) returned 5. Use plain addition instead of abs.")

	r.write("README.md", "docs\n")
	ids["docs"] = r.commit("Docs only")

	r.write("tests/test_core.py", "from calc.core import add\n\n\ndef test_pos():\n    assert add(2, 3) == 5\n\n\ndef test_keeps_sign():\n    assert add(-2, 3) == 1\n\n\ndef test_zero():\n    assert add(0, 0) == 0\n")
	ids["testsonly"] = r.commit("More tests")

	r.write("text/slug.py", "def slug(s):\n    return '-'.join(s.lower().split())\n")
	r.write("tests/test_slug.py", "from text.slug import slug\n\n\ndef test_spaces_collapse():\n    assert slug('a  b') == 'a-b'\n")
	ids["text"] = r.commit("slug keeps double hyphens")

	r.write("calc/net.py", "import urllib.request\n\ndef get(u):\n    return urllib.request.urlopen(u).read()\n")
	r.write("tests/test_net.py", "import urllib.request\n\ndef test_get():\n    assert urllib.request.urlopen('https://example.com').status == 200\n")
	ids["network"] = r.commit("Add get")

	r.write("calc/keys.py", "import os\n\ndef key():\n    return os.environ['SERVICE_API_KEY']\n")
	r.write("tests/test_keys.py", "import os\nfrom calc.keys import key\n\ndef test_key():\n    assert key() == os.environ.get('SERVICE_API_KEY')\n")
	ids["secrets"] = r.commit("Add key")

	if err := os.WriteFile(filepath.Join(r.dir, "calc", "logo.png"), []byte{0x89, 'P', 'N', 'G', 0, 0, 0, 1, 2, 0, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	r.write("tests/test_logo.py", "def test_logo():\n    assert True\n")
	ids["binary"] = r.commit("Ship a logo")

	var big strings.Builder
	for i := 0; i < 500; i++ {
		big.WriteString("X = 1\n")
	}
	r.write("calc/big.py", big.String())
	r.write("tests/test_big.py", "def test_big():\n    assert True\n")
	ids["huge"] = r.commit("Generated table")

	r.remove("tests/test_old.py")
	r.write("calc/core.py", "def add(a, b):\n    return a + b  # simple\n")
	ids["deleted"] = r.commit("Drop an old test")

	r.git("checkout", "-q", "-b", "side")
	r.write("text/upper.py", "def up(s):\n    return s.upper()\n")
	r.write("tests/test_upper.py", "from text.upper import up\n\n\ndef test_up():\n    assert up('a') == 'A'\n")
	ids["side"] = r.commit("Add up")
	r.git("checkout", "-q", "main")
	r.git("merge", "-q", "--no-ff", "-m", "Merge side", "side")
	ids["merge"] = r.git("rev-parse", "HEAD")
	return r, ids
}

func TestMineCandidates(t *testing.T) {
	r, ids := mineRepo(t)
	good, skipped, err := MineCandidates(context.Background(), MineOptions{Repo: r.dir, TestGlobs: DefaultTestGlobs(TemplatePythonPytest)})
	if err != nil {
		t.Fatal(err)
	}
	skip := map[string]Candidate{}
	for _, c := range skipped {
		skip[c.Commit] = c
	}
	for name, want := range map[string]string{
		"root": SkipRoot, "docs": SkipNoTests, "testsonly": SkipOnlyTests, "network": SkipNetwork, "secrets": SkipSecrets,
		"binary": SkipBinary, "huge": SkipHuge, "deleted": SkipDeleted, "merge": SkipMerge,
	} {
		c, ok := skip[ids[name]]
		if !ok || c.Skip != want {
			t.Errorf("%s: skip %q (detail %q), want %q", name, c.Skip, c.SkipDetail, want)
		}
	}
	if d := skip[ids["secrets"]].SkipDetail; !strings.Contains(d, "SERVICE_API_KEY") {
		t.Errorf("secrets detail %q does not name the variable", d)
	}
	var got []string
	for _, c := range good {
		got = append(got, c.Commit)
		if c.Parent == "" || len(c.Tests) == 0 || len(c.Source) == 0 {
			t.Errorf("candidate %s incomplete: %+v", c.Subject, c)
		}
	}
	// Three candidates; ranking interleaves areas: the smallest change first,
	// then the best of the other folder.
	if len(good) != 3 || !slices.Contains(got, ids["calc"]) || !slices.Contains(got, ids["text"]) || !slices.Contains(got, ids["side"]) {
		t.Fatalf("candidates = %v", got)
	}
	if good[0].Area == good[1].Area {
		t.Errorf("first two picks share area %q: %+v", good[0].Area, good[:2])
	}
	calc := good[slices.Index(got, ids["calc"])]
	if !slices.Equal(calc.TestNames, []string{"test_pos", "test_keeps_sign"}) || calc.Area != "calc" {
		t.Errorf("calc candidate: names %v area %q", calc.TestNames, calc.Area)
	}
	// live test l13: the instruction lists the test the commit added, not
	// test_pos, which the parent already had.
	if !slices.Equal(calc.NewTestNames, []string{"test_keeps_sign"}) || !slices.Equal(calc.InstructionTests(), []string{"test_keeps_sign"}) {
		t.Errorf("calc candidate: new tests %v, instruction tests %v", calc.NewTestNames, calc.InstructionTests())
	}
	text := good[slices.Index(got, ids["text"])]
	if !slices.Equal(text.NewTestNames, []string{"test_spaces_collapse"}) {
		t.Errorf("text candidate: new tests %v (a new test file: every test is new)", text.NewTestNames)
	}
	onlyChanged := Candidate{TestNames: []string{"test_a", "test_b"}}
	if !slices.Equal(onlyChanged.InstructionTests(), onlyChanged.TestNames) {
		t.Errorf("no added test: instruction tests %v, want every hidden test", onlyChanged.InstructionTests())
	}
}

func TestMineSinceAndPaths(t *testing.T) {
	r, ids := mineRepo(t)
	globs := DefaultTestGlobs(TemplatePythonPytest)
	good, _, err := MineCandidates(context.Background(), MineOptions{Repo: r.dir, TestGlobs: globs, Since: ids["text"]})
	if err != nil {
		t.Fatal(err)
	}
	if len(good) != 1 || good[0].Commit != ids["side"] {
		t.Errorf("-since %s: %+v", ids["text"][:8], good)
	}
	good, skipped, err := MineCandidates(context.Background(), MineOptions{Repo: r.dir, TestGlobs: globs, Paths: []string{"text"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range good {
		if c.Area != "text" {
			t.Errorf("-paths text kept %s (%s)", c.Subject, c.Area)
		}
	}
	found := false
	for _, c := range skipped {
		if c.Commit == ids["calc"] && c.Skip == SkipPaths {
			found = true
		}
	}
	if !found {
		t.Error("calc fix not skipped by -paths text")
	}
	if _, _, err := MineCandidates(context.Background(), MineOptions{Repo: r.dir, TestGlobs: globs, MinTests: 2}); err != nil {
		t.Fatal(err)
	}
	good, _, _ = MineCandidates(context.Background(), MineOptions{Repo: r.dir, TestGlobs: globs, MinTests: 2})
	if len(good) != 0 {
		t.Errorf("-min-tests 2 kept %d candidates", len(good))
	}
	good, _, _ = MineCandidates(context.Background(), MineOptions{Repo: r.dir, TestGlobs: globs, Since: "2999-01-01"})
	if len(good) != 0 {
		t.Errorf("-since a future date kept %d candidates", len(good))
	}
}

func TestRankPrefersSmallAndSpreadsAreas(t *testing.T) {
	in := []Candidate{
		{Commit: "a", Area: "x", Lines: 100, Source: []string{"x/a"}, Tests: []string{"t"}},
		{Commit: "b", Area: "x", Lines: 5, Source: []string{"x/b"}, Tests: []string{"t"}},
		{Commit: "c", Area: "y", Lines: 50, Source: []string{"y/c"}, Tests: []string{"t"}},
		{Commit: "d", Area: "x", Lines: 6, Source: []string{"x/d"}, Tests: []string{"t"}},
	}
	var got []string
	for _, c := range Rank(in) {
		got = append(got, c.Commit)
	}
	if strings.Join(got, "") != "bcda" {
		t.Errorf("rank = %v, want b c d a", got)
	}
}

func TestMinedInstructionDoesNotLeakTheFix(t *testing.T) {
	got := string(MinedInstruction("fix(calc): add drops the sign",
		"add(-2, 3) returned 5. Use plain addition instead of abs.\n\nThe fix replaces abs with a plain sum.",
		[]string{"test_keeps_sign", "TestParseHours", "clamps above", "test_keeps_sign"}))
	for _, want := range []string{"# Add drops the sign", "add(-2, 3) returned 5.", "- keeps sign", "- parse hours", "- clamps above"} {
		if !strings.Contains(got, want) {
			t.Errorf("instruction lacks %q:\n%s", want, got)
		}
	}
	for _, leak := range []string{"plain addition", "abs", "replaces", EditMeMarker, "tests/", "fix("} {
		if strings.Contains(got, leak) {
			t.Errorf("instruction leaks %q:\n%s", leak, got)
		}
	}
	semi := string(MinedInstruction("dedent treats a tab as one character", "A tab block was not dedented; expand tabs to four spaces first.", nil))
	if !strings.Contains(semi, "A tab block was not dedented.\n") || strings.Contains(semi, "expand") {
		t.Errorf("open clause:\n%s", semi)
	}
	if strings.Count(got, "keeps sign") != 1 {
		t.Errorf("duplicate test phrase:\n%s", got)
	}
}

func TestMineMarkAndReview(t *testing.T) {
	r, base, ref := pythonRepo(t)
	root, dir := newSuiteDir(t, "s", TemplatePythonPytest)
	res, err := InitTask(context.Background(), TaskInitOptions{SuiteDir: dir, ID: "m-1", Repo: r.dir, Base: base, Reference: ref})
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkMined(res.Dir, MinedInstruction("Keep signs in add", "", []string{"test_neg"}), "python -m pytest"); err != nil {
		t.Fatal(err)
	}
	s, task, err := suite.LoadTask(root, "s", "m-1")
	if err != nil {
		t.Fatal(err)
	}
	p := task.Provenance
	if p == nil || p.CreatedBy != MinedBy || p.InstructionReviewed == nil || *p.InstructionReviewed {
		t.Fatalf("provenance = %+v", p)
	}
	for _, pr := range suite.ValidateTask(context.Background(), s, task) {
		if pr.Severity == suite.SeverityError {
			t.Errorf("mined task does not validate: %s: %s", pr.Path, pr.Message)
		}
	}
	raw := readFile(t, filepath.Join(res.Dir, "task.yaml"))
	out, changed, err := SetInstructionReviewed([]byte(raw))
	if err != nil || !changed || !strings.Contains(string(out), "instruction_reviewed: true") || strings.Contains(string(out), "instruction_reviewed: false") {
		t.Fatalf("review: changed %v err %v\n%s", changed, err, out)
	}
	if _, changed, _ := SetInstructionReviewed(out); changed {
		t.Error("second review changed the file")
	}
	// A task init task (no field) gets the line added under provenance.
	plain := strings.Replace(raw, "  instruction_reviewed: false\n", "", 1)
	out, changed, err = SetInstructionReviewed([]byte(plain))
	if err != nil || !changed || !strings.Contains(string(out), "provenance:\n  instruction_reviewed: true\n") {
		t.Fatalf("review of an unmarked task: %v %v\n%s", changed, err, out)
	}
}

func TestMarkMinedGenericReward(t *testing.T) {
	r, base, ref := pythonRepo(t)
	_, dir := newSuiteDir(t, "g", TemplateGenericReward)
	res, err := InitTask(context.Background(), TaskInitOptions{SuiteDir: dir, ID: "m-1", Repo: r.dir, Base: base, Reference: ref})
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkMined(res.Dir, []byte("# x\n"), ""); err == nil {
		t.Fatal("generic-reward task without a test command was marked")
	}
	if err := MarkMined(res.Dir, []byte("# x\n"), "cargo test"); err != nil {
		t.Fatal(err)
	}
	sh := readFile(t, filepath.Join(res.Dir, "verifier", "tests", "test.sh"))
	if strings.Contains(sh, "EDIT ME") || !strings.Contains(sh, "if cargo test; then") {
		t.Errorf("test.sh:\n%s", sh)
	}
}

func TestNeedsNetworkAndSecrets(t *testing.T) {
	for src, want := range map[string]bool{
		"import requests\nrequests.get('https://x.io')": true,
		"resp = urlopen(url)":                           true,
		"assert parse('https://example.com/a') == 1":    false,
		"connect to https://api.example.com first":      true,
		"r = http.Get(\"http://localhost:8080\")":       true,
		"assert url == 'http://127.0.0.1/x'":            false,
		"def test_add():\n    assert add(1, 2) == 3":    false,
	} {
		if got := NeedsNetwork([]byte(src)) != ""; got != want {
			t.Errorf("NeedsNetwork(%q) = %v, want %v", src, got, want)
		}
	}
	for src, want := range map[string]string{
		"os.environ['OPENAI_API_KEY']":           "OPENAI_API_KEY",
		`os.getenv("GITHUB_TOKEN")`:              "GITHUB_TOKEN",
		"process.env.STRIPE_SECRET":              "STRIPE_SECRET",
		"os.environ.get('HOME')":                 "",
		"password = 'x'  # not from the env var": "",
	} {
		if got := NeedsSecrets([]byte(src)); got != want {
			t.Errorf("NeedsSecrets(%q) = %q, want %q", src, got, want)
		}
	}
}

func TestExportAgentFiles(t *testing.T) {
	r := newTestRepo(t)
	r.write("AGENTS.md", "v1\n")
	r.write(".claude/skills/review/SKILL.md", "skill\n")
	r.write("src/app.py", "x = 1\n")
	one := r.commit("one")
	r.write("AGENTS.md", "v2\n")
	two := r.commit("two")
	dst1, dst2 := t.TempDir(), t.TempDir()
	a, err := ExportAgentFiles(context.Background(), r.dir, one, DefaultAgentPaths, dst1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ExportAgentFiles(context.Background(), r.dir, two, DefaultAgentPaths, dst2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.Files, []string{".claude/skills/review/SKILL.md", "AGENTS.md"}) || a.Hash == b.Hash {
		t.Fatalf("exported %v / %v, hashes equal %v", a.Files, b.Files, a.Hash == b.Hash)
	}
	if got := readFile(t, filepath.Join(dst2, "AGENTS.md")); got != "v2\n" {
		t.Errorf("AGENTS.md at two = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dst1, "src")); err == nil {
		t.Error("exported a path outside the agent paths")
	}
	if st := r.git("status", "--porcelain"); st != "" {
		t.Errorf("export changed the repository: %s", st)
	}
	again, err := ExportAgentFiles(context.Background(), r.dir, "HEAD", DefaultAgentPaths, t.TempDir())
	if err != nil || again.Hash != b.Hash {
		t.Errorf("same tree, other hash: %v", err)
	}
}

// A fix that adds the repository's first test is skipped for a hidden_tests
// suite, whose static validation needs a visible test in the starting repo
// (audit 5-B-16); without the option it stays a candidate.
func TestMineSkipsFirstTestForHiddenTests(t *testing.T) {
	r := newTestRepo(t)
	r.write("calc/__init__.py", "")
	r.write("calc/core.py", "def add(a, b):\n    return abs(a) + abs(b)\n")
	r.commit("Initial")
	r.write("calc/core.py", "def add(a, b):\n    return a + b\n")
	r.write("tests/test_core.py", "from calc.core import add\n\n\ndef test_keeps_sign():\n    assert add(-2, 3) == 1\n")
	first := r.commit("fix: add keeps the sign")
	opts := MineOptions{Repo: r.dir, TestGlobs: DefaultTestGlobs(TemplatePythonPytest), NeedBaseTests: true}
	good, skipped, err := MineCandidates(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(good) != 0 || len(skipped) != 2 || skipped[0].Commit != first || skipped[0].Skip != SkipNoBaseTests {
		t.Fatalf("good %v skipped %+v", good, skipped)
	}
	opts.NeedBaseTests = false
	if good, _, err := MineCandidates(context.Background(), opts); err != nil || len(good) != 1 {
		t.Fatalf("without the option: %v %v", good, err)
	}
}
