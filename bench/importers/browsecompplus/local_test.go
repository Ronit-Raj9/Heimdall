package browsecompplus

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/research"
)

// These tests run the suite's own Python files (the corpus service, the
// research track's websearch tool, the reference and control scripts and
// the verifier) on the host, wired as a run wires them minus the tool
// proxy: no Docker, no network beyond loopback, no model.

func needPython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// startPython runs a server script and waits for its /healthz.
func startPython(t *testing.T, script string, port int, env ...string) {
	t.Helper()
	cmd := exec.Command("python3", "-I", "-B", script)
	cmd.Env = append(os.Environ(), append(env, fmt.Sprintf("PORT=%d", port))...)
	out := &strings.Builder{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s did not start: %s", script, out.String())
}

// grade runs a control script in a fresh workspace and the verifier on what
// it wrote, and returns the reward and the verifier's stdout.
func grade(t *testing.T, taskDir, control string, mcp string) (int, string) {
	t.Helper()
	ws := t.TempDir()
	cmd := exec.Command("sh", filepath.Join(taskDir, control))
	cmd.Dir = ws
	cmd.Env = append(os.Environ(), "BENCH_MCP_SERVERS="+mcp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", control, err, out)
	}
	art := t.TempDir()
	for id, name := range map[string]string{"answer": "answer.txt", "sources": "sources.json"} {
		b, err := os.ReadFile(filepath.Join(ws, name))
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Join(art, id), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(art, id, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outDir := t.TempDir()
	v := exec.Command("python3", "-I", "-B", filepath.Join(taskDir, "verifier", "tests", "verify.py"))
	v.Env = append(os.Environ(), "GRADER_ARTIFACTS="+art, "GRADER_OUT="+outDir)
	stdout, err := v.Output()
	if err != nil {
		t.Fatalf("verify.py: %v", err)
	}
	var r struct {
		Reward int `json:"reward"`
	}
	b, err := os.ReadFile(filepath.Join(outDir, "reward.json"))
	if err != nil || json.Unmarshal(b, &r) != nil {
		t.Fatalf("reward.json: %v %s", err, b)
	}
	return r.Reward, string(stdout)
}

// TestControlsEndToEnd imports the fixture, starts the corpus service and
// the websearch tool on loopback, and runs every control and the verifier:
// the reference must pass (searching, fetching and reading the answer from
// the gold page, or decrypting it), every other control must fail, and the
// verifier must print neither answer.
func TestControlsEndToEnd(t *testing.T) {
	needPython(t)
	root := t.TempDir()
	mustImport(t, root, writeFixture(t, t.TempDir(), true), nil, false)
	dir := filepath.Join(root, "benchmarks", "bcp-fx")
	cport, wport := freePort(t), freePort(t)
	startPython(t, filepath.Join(dir, "services", "corpus", "server.py"), cport, "CORPUS="+filepath.Join(dir, corpusPath))
	startPython(t, filepath.Join(dir, "services", "websearch", "server.py"), wport, fmt.Sprintf("TAVILY_URL=http://127.0.0.1:%d", cport))
	mcp := fmt.Sprintf(`{"websearch": {"url": "http://127.0.0.1:%d/mcp"}}`, wport)
	for _, task := range []string{"bcp-9001", "bcp-9002"} {
		td := filepath.Join(dir, "tasks", task)
		for control, want := range map[string]int{"solution/reference.sh": 1, "controls/empty.sh": 0, "controls/mutant-wrong-answer.sh": 0, "controls/exploit.sh": 0} {
			got, out := grade(t, td, control, mcp)
			if got != want {
				t.Errorf("%s %s: reward %d, want %d (%s)", task, control, got, want, out)
			}
			for _, q := range fixtureQueries {
				if strings.Contains(strings.ToLower(out), strings.ToLower(q.answer)) {
					t.Errorf("%s %s: the verifier printed an answer", task, control)
				}
			}
		}
	}
}

// TestRankingParity checks that the corpus service ranks exactly as the
// importer predicts (research/standin's BM25 in Go), on the fixture and,
// when BENCH_BCP_FROM names a queries JSONL, on the first real queries.
func TestRankingParity(t *testing.T) {
	needPython(t)
	queries := []string{"Brindle Cove keeper cat ship", "cod landed week", "harrowgate rail society founder", "gulls 1,250 kilograms", "zzz nothing"}
	var rows []query
	from := os.Getenv("BENCH_BCP_FROM")
	if from != "" {
		files, err := findQueries(from)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		rows, _, err = readQueries(context.Background(), files, nil, "", func(query) (bool, bool) { n++; return true, n >= 6 })
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range rows {
			queries = append(queries, q.Question, research.NormalizeQuery(q.Question))
		}
	} else {
		root := t.TempDir()
		files, _ := findQueries(writeFixture(t, t.TempDir(), true))
		rows, _, _ = readQueries(context.Background(), files, nil, "", func(query) (bool, bool) { return true, false })
		_ = root
	}
	ix, err := buildCorpus(rows)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files, _, _, err := suiteFiles(ix)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "corpus.jsonl"), files[corpusPath], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.py"), files["services/corpus/server.py"], 0o644); err != nil {
		t.Fatal(err)
	}
	qs, _ := json.Marshal(queries)
	prog := `import json, sys, importlib.util
spec = importlib.util.spec_from_file_location("corpus_server", sys.argv[1])
m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
ix = m.Index(sys.argv[2])
print(json.dumps([[[d["url"], s] for s, d in ix.search(q, 8)] for q in json.loads(sys.argv[3])]))`
	out, err := exec.Command("python3", "-I", "-B", "-c", prog, filepath.Join(dir, "server.py"), filepath.Join(dir, "corpus.jsonl"), string(qs)).Output()
	if err != nil {
		t.Fatalf("python ranking: %v", err)
	}
	var py [][][2]any
	if err := json.Unmarshal(out, &py); err != nil {
		t.Fatal(err)
	}
	for i, q := range queries {
		hits := ix.bm25.Search(q, 8)
		if len(hits) != len(py[i]) {
			t.Errorf("query %d: Go %d hits, Python %d", i, len(hits), len(py[i]))
			continue
		}
		for j, h := range hits {
			if h.Doc.URL != py[i][j][0] || h.Score != py[i][j][1].(float64) {
				t.Errorf("query %d hit %d: Go %s %v, Python %v %v", i, j, h.Doc.URL, h.Score, py[i][j][0], py[i][j][1])
			}
		}
	}
}

// TestMatchParity runs verify.py's answer_matches and normalize on the same
// pairs as answersMatch and normalizeAnswer.
func TestMatchParity(t *testing.T) {
	needPython(t)
	pairs := [][2]string{
		{"Marrow Gull", "Marrow Gull"}, {"marrow gull.", "Marrow Gull"}, {"The Marrow Gull", "Marrow Gull"}, {"Marrow-Gull", "Marrow Gull"},
		{"MarrowGull", "Marrow Gull"}, {"1,250", "1250"}, {"1250 kg", "1250"}, {"$1250", "1250"}, {"1250.0", "1250"}, {"Ｍarrow Ｇull", "Marrow Gull"},
		{"A", "A"}, {"the", "The"}, {"Zoë", "zoë"}, {"Zoe", "Zoë"}, {"AC-130", "AC 130"}, {"", "x"}, {"50%", "50"}, {"Gull Marrow", "Marrow Gull"},
		{"Jean\u2013Luc", "Jean-Luc"}, {"  x  ", "x"}, {"x\ty", "x y"}, {"An Apple", "apple"},
	}
	in, _ := json.Marshal(pairs)
	v, err := assets.ReadFile("assets/verifier/verify.py")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "verify.py")
	if err := os.WriteFile(p, v, 0o644); err != nil {
		t.Fatal(err)
	}
	prog := `import json, sys, importlib.util
spec = importlib.util.spec_from_file_location("v", sys.argv[1])
m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
print(json.dumps([[m.answer_matches(a, b), m.normalize(a)] for a, b in json.loads(sys.argv[2])]))`
	out, err := exec.Command("python3", "-I", "-B", "-c", prog, p, string(in)).Output()
	if err != nil {
		t.Fatalf("verify.py: %v", err)
	}
	var py [][2]any
	if err := json.Unmarshal(out, &py); err != nil {
		t.Fatal(err)
	}
	for i, pr := range pairs {
		if got := answersMatch(pr[0], pr[1]); got != py[i][0].(bool) {
			t.Errorf("answersMatch(%q, %q): Go %v, Python %v", pr[0], pr[1], got, py[i][0])
		}
		if got := normalizeAnswer(pr[0]); got != py[i][1].(string) {
			t.Errorf("normalize(%q): Go %q, Python %q", pr[0], got, py[i][1])
		}
	}
}

// TestRealSlice imports the first real queries when BENCH_BCP_FROM names a
// queries JSONL (the published rows), and checks the rule that no suite file
// but the corpus holds an answer in plain text.
func TestRealSlice(t *testing.T) {
	from := os.Getenv("BENCH_BCP_FROM")
	if from == "" {
		t.Skip("BENCH_BCP_FROM not set (a BrowseComp-Plus queries JSONL)")
	}
	root := t.TempDir()
	res, err := (&importer{now: fixedNow}).Import(context.Background(), importers.Options{BenchRoot: root, From: from, SuiteID: "bcp-real", Limit: 6})
	if err != nil {
		t.Fatal(err)
	}
	files, _ := findQueries(from)
	want := map[string]bool{}
	for _, id := range res.Tasks {
		want[strings.TrimPrefix(id, "bcp-")] = true
	}
	rows, _, err := readQueries(context.Background(), files, want, "", func(query) (bool, bool) { return true, false })
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "benchmarks", "bcp-real")
	for _, q := range rows {
		td := filepath.Join(dir, "tasks", taskID(q.ID))
		err := filepath.Walk(td, func(p string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return err
			}
			b, _ := os.ReadFile(p)
			if strings.Contains(strings.ToLower(string(b)), strings.ToLower(q.Answer)) && len(q.Answer) > 3 {
				t.Errorf("%s holds the answer of query %s in plain text", p, q.ID)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestRealClassification reports, for every query of BENCH_BCP_FROM, how
// many answers are short and exact (graded here without a judge) and why
// the others need one. It decrypts questions and answers in memory only and
// logs counts, never an answer.
func TestRealClassification(t *testing.T) {
	from := os.Getenv("BENCH_BCP_FROM")
	if from == "" {
		t.Skip("BENCH_BCP_FROM not set (a BrowseComp-Plus queries JSONL)")
	}
	files, err := findQueries(from)
	if err != nil {
		t.Fatal(err)
	}
	total, exact := 0, 0
	why := map[string]int{}
	_, kind, err := readQueries(context.Background(), files, nil, "", func(q query) (bool, bool) {
		total++
		ok, reason := exactClass(q.Answer)
		if ok {
			exact++
		} else {
			why[reason]++
		}
		return false, false
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d queries (%s): %d short and exact, %d need a judge: %v", total, kind, exact, total-exact, why)
}
