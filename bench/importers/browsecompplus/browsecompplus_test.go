package browsecompplus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/metrics"
	"github.com/maximhq/bifrost/bench/research"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

func fixedNow() time.Time { return time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC) }

func doImport(t *testing.T, root, from string, extra map[string]string, force bool) (*importers.Result, error) {
	t.Helper()
	return (&importer{now: fixedNow}).Import(context.Background(), importers.Options{BenchRoot: root, From: from, SuiteID: "bcp-fx", Extra: extra, Force: force})
}

func mustImport(t *testing.T, root, from string, extra map[string]string, force bool) *importers.Result {
	t.Helper()
	res, err := doImport(t, root, from, extra, force)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return res
}

func TestImportObfuscatedFixture(t *testing.T) {
	root := t.TempDir()
	res := mustImport(t, root, writeFixture(t, t.TempDir(), true), nil, false)
	if strings.Join(res.Tasks, ",") != "bcp-9001,bcp-9002" {
		t.Fatalf("tasks %v", res.Tasks)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].ID != "9003" || !strings.Contains(res.Skipped[0].Reason, "needs a judge") {
		t.Errorf("skipped %+v", res.Skipped)
	}
	joined := strings.Join(res.Notes, "\n")
	for _, s := range []string{"read 3 queries (obfuscated input): 2 short and exact, 1 need a judge; imported 2 into a match suite", "corpus: 7 pages", "not comparable"} {
		if !strings.Contains(joined, s) {
			t.Errorf("notes lack %q:\n%s", s, joined)
		}
	}
	dir := filepath.Join(root, "benchmarks", "bcp-fx")
	for _, f := range []string{".gitignore", "LICENSE-NOTICE.md", "corpus/corpus.jsonl", "services/corpus/server.py", "services/corpus/Dockerfile",
		"services/websearch/server.py", "tasks/bcp-9001/verifier/tests/verify.py", "tasks/bcp-9001/verifier/tests/answer.enc.json",
		"tasks/bcp-9001/verifier/tests/research.json", "tasks/bcp-9001/verifier/tests/corpus_urls.json", "tasks/bcp-9001/solution/reference.sh",
		"tasks/bcp-9001/controls/mutant-wrong-answer.sh", "tasks/bcp-9001/controls/exploit.sh", "tasks/bcp-9001/controls/empty.sh"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	suiteYAML := readFile(t, dir, "suite.yaml")
	for _, s := range []string{"track: research", "grader: verifier_command", "id: corpus", "role: tool", "TAVILY_URL: http://toolproxy:9200/s/corpus", "/srv/corpus/corpus.jsonl: corpus/corpus.jsonl", "importer: browsecomp-plus"} {
		if !strings.Contains(suiteYAML, s) {
			t.Errorf("suite.yaml lacks %q", s)
		}
	}
	spec, err := research.LoadSpec(mustTask(t, root, "bcp-9001"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(spec.EvidenceURLs, ",") != "https://brindle-cove.example/history/light,https://brindle-cove.example/keepers/first-ships" || len(spec.Expected) != 0 {
		t.Errorf("research spec %+v", spec)
	}
	if CorpusService != metrics.CorpusService {
		t.Errorf("corpus service id %q differs from the metric's %q", CorpusService, metrics.CorpusService)
	}
	// 9001's reference reads the answer from the gold page; 9002's gold
	// page writes 1,250, so its reference decrypts the published answer.
	if ref := readFile(t, dir, "tasks", "bcp-9001", "solution", "reference.sh"); !strings.Contains(ref, `"span":{"page":"`) || strings.Contains(ref, `"cipher":"`) {
		t.Errorf("9001 reference is not a gold-page span:\n%s", ref)
	}
	if ref := readFile(t, dir, "tasks", "bcp-9002", "solution", "reference.sh"); !strings.Contains(ref, `"cipher":"`) {
		t.Error("9002 reference does not fall back to the ciphertext")
	}
}

// TestNoPlainTextAnswerInSuite is the rule of this importer: no file of an
// exact-match suite holds an answer in plain text.
func TestNoPlainTextAnswerInSuite(t *testing.T) {
	for _, obf := range []bool{true, false} {
		root := t.TempDir()
		mustImport(t, root, writeFixture(t, t.TempDir(), obf), nil, false)
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			for _, q := range fixtureQueries {
				// The corpus pages are public plain text and may hold an
				// answer (that is what a gold page is); nothing else may.
				if strings.HasSuffix(p, "corpus.jsonl") {
					continue
				}
				if bytes.Contains(bytes.ToLower(b), []byte(strings.ToLower(q.answer))) {
					t.Errorf("%s holds the answer of query %s in plain text", rel, q.id)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestDecryptedInputWritesTheSameSuite(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	mustImport(t, a, writeFixture(t, t.TempDir(), true), nil, false)
	mustImport(t, b, writeFixture(t, t.TempDir(), false), nil, false)
	if treeHash(t, a) != treeHash(t, b) {
		t.Error("the decrypted input wrote a different suite than the published one")
	}
}

func TestSuiteLintsClean(t *testing.T) {
	root := t.TempDir()
	mustImport(t, root, writeFixture(t, t.TempDir(), true), nil, false)
	s, err := suite.Builtin(filepath.Join(root, "benchmarks"))[0].Load(context.Background(), "bcp-fx")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range suite.Lint(s) {
		if p.Severity == suite.SeverityError {
			t.Errorf("lint error: %s", p.Message)
		}
	}
}

func TestReimport(t *testing.T) {
	root, from := t.TempDir(), writeFixture(t, t.TempDir(), true)
	mustImport(t, root, from, nil, false)
	before := treeHash(t, root)
	res := mustImport(t, root, from, nil, false)
	if len(res.Tasks) != 0 || treeHash(t, root) != before {
		t.Error("a second import without -force changed the suite")
	}
	mustImport(t, root, from, nil, true)
	if treeHash(t, root) != before {
		t.Error("a forced re-import of the same queries changed the suite")
	}
	if _, err := (&importer{now: fixedNow}).Import(context.Background(), importers.Options{BenchRoot: root, From: from, SuiteID: "bcp-fx", IDs: []string{"9001"}}); err == nil || !strings.Contains(err.Error(), "share one corpus") {
		t.Errorf("a different selection into the same suite: %v", err)
	}
}

func TestJudgedImport(t *testing.T) {
	from := writeFixture(t, t.TempDir(), true)
	if _, err := doImport(t, t.TempDir(), from, map[string]string{setGrading: gradingJudged}, false); err == nil || !strings.Contains(err.Error(), "datasets/") {
		t.Fatalf("judged import outside datasets/: %v", err)
	}
	root := filepath.Join(t.TempDir(), "datasets", "project")
	res := mustImport(t, root, from, map[string]string{setGrading: gradingJudged}, false)
	if strings.Join(res.Tasks, ",") != "bcp-9003" {
		t.Fatalf("judged tasks %v", res.Tasks)
	}
	dir := filepath.Join(root, "benchmarks", "bcp-fx")
	sy := readFile(t, dir, "suite.yaml")
	for _, s := range []string{"grading: judged", "grader: judge", "provider: pin-me", "prompt_version: qa-v1", "kind: answer"} {
		if !strings.Contains(sy, s) {
			t.Errorf("judged suite.yaml lacks %q", s)
		}
	}
	if !strings.Contains(readFile(t, dir, "tasks", "bcp-9003", "verifier", "answer.yaml"), "Ada Fenwick") {
		t.Error("judged answer.yaml lacks the expected answer the judge needs")
	}
	s, err := suite.Builtin(filepath.Join(root, "benchmarks"))[0].Load(context.Background(), "bcp-fx")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range suite.Lint(s) {
		if p.Severity == suite.SeverityError && !strings.Contains(p.Message, "pin-me") {
			t.Errorf("lint error: %s", p.Message)
		}
	}
}

func TestRefusals(t *testing.T) {
	from := writeFixture(t, t.TempDir(), true)
	for name, c := range map[string]struct {
		extra map[string]string
		want  string
	}{
		"unknown setting": {map[string]string{"colour": "red"}, "unknown setting"},
		"bad grading":     {map[string]string{setGrading: "vibes"}, "want match or judged"},
		"bad input":       {map[string]string{setInput: "zip"}, "want obfuscated, decrypted or auto"},
		"wrong input":     {map[string]string{setInput: inputObfuscated}, ""},
	} {
		_, err := doImport(t, t.TempDir(), from, c.extra, false)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	plain := writeFixture(t, t.TempDir(), false)
	if _, err := doImport(t, t.TempDir(), plain, map[string]string{setInput: inputObfuscated}, false); err == nil || !strings.Contains(err.Error(), "-set input=decrypted") {
		t.Errorf("decrypted file read as obfuscated: %v", err)
	}
	pq := t.TempDir()
	if err := os.WriteFile(filepath.Join(pq, "test-00000-of-00006.parquet"), []byte("PAR1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := doImport(t, t.TempDir(), pq, nil, false); err == nil || !strings.Contains(err.Error(), "tojsonl.py") {
		t.Errorf("parquet folder: %v", err)
	}
	if _, err := (&importer{now: fixedNow}).Import(context.Background(), importers.Options{BenchRoot: t.TempDir(), From: from, SuiteID: "x", IDs: []string{"777"}}); !errors.Is(err, errNoQueries) {
		t.Errorf("unknown id: %v", err)
	}
}

func TestExactClass(t *testing.T) {
	for ans, want := range map[string]bool{
		"Marrow Gull": true, "1250": true, "AC-130": true, "Jean-Luc Picard": true,
		"The lighthouse keeper's daughter, Ada Fenwick": false, "March 3, 2010": false, "2010-03-03": false,
		"cats or dogs": false, "approximately 40": false, "Paris (France)": false, "": false, "a/b": false,
	} {
		if got, _ := exactClass(ans); got != want {
			t.Errorf("exactClass(%q) = %v, want %v", ans, got, want)
		}
	}
}

func TestSplitFrontMatter(t *testing.T) {
	title, date, off := splitFrontMatter(fxShipPage.text, fxShipPage.url)
	if title != "First ships of the Brindle Cove keepers" || date != "2024-05-02" || !strings.HasPrefix(string([]rune(fxShipPage.text)[off:]), "Every keeper") {
		t.Errorf("front matter: %q %q %d", title, date, off)
	}
	title, date, off = splitFrontMatter(fxNoFront.text, fxNoFront.url)
	if title != "Fleet notes without front matter." || date != "" || off != 0 {
		t.Errorf("no front matter: %q %q %d", title, date, off)
	}
	title, _, _ = splitFrontMatter("", "https://x.example/")
	if title != "https://x.example/" {
		t.Errorf("empty page title %q", title)
	}
}

func TestFindFold(t *testing.T) {
	if i := findFold("Zoë met MARROW gull.", "marrow Gull"); i != 8 {
		t.Errorf("findFold = %d", i)
	}
	if findFold("abc", "abcd") != -1 || findFold("abc", "") != -1 {
		t.Error("findFold edge cases")
	}
}

func mustTask(t *testing.T, root, id string) *schemas.Task {
	t.Helper()
	_, task, err := suite.LoadTask(filepath.Join(root, "benchmarks"), "bcp-fx", id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func readFile(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func treeHash(t *testing.T, root string) string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		names = append(names, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, _ := os.ReadFile(n)
		rel, _ := filepath.Rel(root, n)
		h.Write([]byte(rel))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}
