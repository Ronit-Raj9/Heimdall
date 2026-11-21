package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestCheckScriptedProxy(t *testing.T) {
	health := func(body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/health" {
				http.NotFound(w, r)
				return
			}
			if _, err := w.Write([]byte(body)); err != nil {
				t.Errorf("write: %v", err)
			}
		}))
	}
	current := health(`{"status":"ok","plan_modes":["none","persistent","once","scripted"],"scripted_providers":["anthropic","openai","groq"]}`)
	defer current.Close()
	old := health(`{"status":"ok","upstream":{"anthropic":"scripted"},"slots":8}`)
	defer old.Close()
	tests := []struct {
		name, url, token, provider, wantErr string
	}{
		{name: "current proxy, anthropic", url: current.URL, token: "tok", provider: "anthropic"},
		{name: "empty provider means anthropic", url: current.URL, token: "tok"},
		{name: "openai-compatible provider", url: current.URL, token: "tok", provider: "groq"},
		{name: "no token refuses", url: current.URL, provider: "anthropic", wantErr: "BENCH_FAULTPROXY_TOKEN"},
		{name: "old proxy refuses", url: old.URL, token: "tok", provider: "anthropic", wantErr: "too old for plan mode scripted"},
		{name: "provider without a scripted model refuses", url: current.URL, token: "tok", provider: "gemini", wantErr: "provider gemini has no scripted model"},
		{name: "unreachable proxy refuses", url: "http://127.0.0.1:1", token: "tok", provider: "anthropic", wantErr: "refuses to run"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkScriptedProxy(context.Background(), tt.url, tt.token, tt.provider)
			if (err != nil) != (tt.wantErr != "") || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCheckReportResult(t *testing.T) {
	row := func(id, status string) checkRow {
		return checkRow{ID: id, Title: rowTitles[id], Status: status, Detail: "d", Fix: "f"}
	}
	tests := []struct {
		name string
		rows []checkRow
		want string
		exit int
	}{
		{"all pass", []checkRow{row(rowImage, checkPass), row(rowUser, checkPass)}, checkPass, exitOK},
		{"warn", []checkRow{row(rowImage, checkPass), row(rowClaim, checkWarn)}, checkWarn, exitOK},
		{"fail beats warn", []checkRow{row(rowClaim, checkWarn), row(rowExit, checkFail), row(rowImage, checkPass)}, checkFail, 1},
		{"skips only", append([]checkRow{row(rowImage, checkFail)}, skippedRows(rowOrder[1:], "no image")...), checkFail, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := (&checkReport{Rows: tt.rows}).finish()
			if rep.Result != tt.want || checkReportExit(rep) != tt.exit {
				t.Fatalf("result %s exit %d, want %s %d", rep.Result, checkReportExit(rep), tt.want, tt.exit)
			}
		})
	}
	rep := (&checkReport{}).fail(os.ErrNotExist)
	if rep.Result != checkError || checkReportExit(rep) != exitInfra || rep.Error == "" {
		t.Fatalf("error report %+v", rep)
	}
}

func TestPrintCheckReport(t *testing.T) {
	rep := (&checkReport{Agent: "mine", Suite: "coding-core", Task: "t01", Image: "acme/x:1-coding-core", ImageID: "sha256:0123456789abcdef",
		RunID: "r_1", RunDir: "/p/checks/e/r_1", Outcome: "fail", Rows: []checkRow{
			{ID: rowImage, Title: rowTitles[rowImage], Status: checkPass, Detail: "built", Fix: "never shown"},
			{ID: rowClaim, Title: rowTitles[rowClaim], Status: checkWarn, Detail: "no final message", Fix: "print it last"},
		}}).finish()
	var b bytes.Buffer
	printCheckReport(&b, rep)
	out := b.String()
	for _, want := range []string{"agent check mine on coding-core/t01", "image acme/x:1-coding-core 0123456789ab", "PASS  image builds",
		"WARN  completion claim captured", "      fix: print it last", "run r_1 (task outcome fail) in /p/checks/e/r_1", "result: WARN"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "never shown") {
		t.Errorf("fix hint printed for a PASS row:\n%s", out)
	}
	j, err := json.Marshal(rep)
	if err != nil || !strings.Contains(string(j), `"result":"WARN"`) || !strings.Contains(string(j), `"fix":"print it last"`) {
		t.Errorf("json %s %v", j, err)
	}
}

func TestReadCheckRun(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("evidence.json", `{"diff":{"path":"x","sha256":"y","files":[{"path":"a.py","status":"added"}]},"ledger":[{"upstream":"scripted","action":"pass"}]}`)
	write("gates.json", `[{"gate":"egress","fired":false}]`)
	write("plan.json", `{"wall_s":300}`)
	write(environments.NetWatchFile, `{"t":"dns","name":"bifrost"}`+"\n")
	write("agent.stderr.txt", strings.Repeat("x", 3000)+"\nlast line\n")
	cfg := &schemas.AgentConfig{ID: "mine", Budgets: schemas.Budgets{WallS: 900}}
	in := readCheckRun(dir, &schemas.Run{RunID: "r_1"}, cfg)
	if in.ev.Diff == nil || len(in.ev.Ledger) != 1 || len(in.gates) != 1 || in.wallS != 300 || len(in.net) != 1 || in.netErr != "" {
		t.Fatalf("input = %+v", in)
	}
	if len(in.stderr) != stderrTail || !strings.HasSuffix(in.stderr, "last line\n") {
		t.Fatalf("stderr tail %d bytes", len(in.stderr))
	}
	empty := readCheckRun(t.TempDir(), &schemas.Run{}, cfg)
	if empty.wallS != 900 || !strings.Contains(empty.netErr, "wrote nothing") {
		t.Fatalf("empty run input = %+v", empty)
	}
	write(environments.NetWatchFile, `{"t":"error","error":"exec: python3: not found"}`+"\n")
	if _, why := readNetWatch(filepath.Join(dir, environments.NetWatchFile)); !strings.Contains(why, "did not start: exec: python3") {
		t.Fatalf("observer failure reason %q", why)
	}
}

func TestGatewayHostPort(t *testing.T) {
	for raw, want := range map[string]struct {
		host string
		port int
	}{
		"http://bifrost:8080":   {"bifrost", 8080},
		"http://Gateway.local/": {"gateway.local", 80},
		"https://gw.example":    {"gw.example", 443},
		"::bad":                 {"", 0},
	} {
		h, p := gatewayHostPort(raw)
		if h != want.host || p != want.port {
			t.Errorf("gatewayHostPort(%q) = %s %d, want %s %d", raw, h, p, want.host, want.port)
		}
	}
}

func TestReadCheckScenario(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &schemas.Suite{ID: "desk"}
	task := &schemas.Task{ID: "t1", Services: []schemas.ServiceSpec{{ID: "desk", Role: schemas.ServiceRoleMCP}}}
	sc := readCheckScenario(dir, &schemas.Evidence{}, s, task)
	if sc.tools != nil || !strings.Contains(sc.toolsErr, "tools.jsonl missing") || sc.services != nil || sc.answer != nil {
		t.Fatalf("empty run folder: %+v", sc)
	}
	write("services.json", `[{"id":"desk","image":"acme/desk:1","ready":true,"proxied":true}]`)
	write("sessions.json", `[{"id":"s1","index":1,"exit_code":0,"timed_out":false,"started":"2026-11-15T10:00:00Z","ended":"2026-11-15T10:00:01Z","stdout_path":"agent.stream.1.jsonl"}]`)
	write("tools.jsonl", `{"seq":1,"server":"desk","tool":"tools/list","protocol":"mcp","args_sha256":"a","result_sha256":"b","is_error":false,"started":"2026-11-15T10:00:00Z","latency_ms":3}`+"\n\n")
	write("answer.txt", "42\n")
	sc = readCheckScenario(dir, &schemas.Evidence{}, s, task)
	if len(sc.services) != 1 || !sc.services[0].Ready || len(sc.sessions) != 1 || len(sc.tools) != 1 || sc.toolsErr != "" ||
		sc.answer == nil || sc.answer.Text != "42\n" || sc.answer.Source != "file" {
		t.Fatalf("run folder: %+v", sc)
	}
	ev := &schemas.Evidence{Answer: &schemas.Answer{Text: "x", Source: "final_message"}, ToolCalls: []schemas.ToolCall{}}
	if sc := readCheckScenario(dir, ev, s, task); sc.answer.Source != "final_message" || len(sc.tools) != 0 || sc.toolsErr != "" {
		t.Errorf("evidence first: %+v", sc)
	}
	write("tools.jsonl", "{not json\n")
	if _, why := readToolLedger(filepath.Join(dir, "tools.jsonl")); !strings.Contains(why, "line 1 does not parse") {
		t.Errorf("bad ledger: %q", why)
	}
	if !isScenarioTask(s, task) || isScenarioTask(&schemas.Suite{}, &schemas.Task{}) {
		t.Error("isScenarioTask")
	}
}

func TestOwnImageRow(t *testing.T) {
	pass := checkRow{ID: rowImage, Status: checkPass, Detail: "built x"}
	own := &schemas.Task{ID: "tb1", Environment: &schemas.TaskEnvironment{Dockerfile: "environment/Dockerfile"}}
	plain := &schemas.Task{ID: "t1"}
	cmdAgent := &schemas.AgentConfig{Adapter: "command"}
	perSuite := &schemas.AgentConfig{Adapter: "command", Build: &schemas.ImageSpec{BaseArg: "BASE_IMAGE"}}
	if r := ownImageRow(pass, cmdAgent, own); r.Status != checkWarn || !strings.Contains(r.Detail, "task tb1 has its own image") || r.Fix == "" {
		t.Errorf("own image, no base_arg: %+v", r)
	}
	for name, r := range map[string]checkRow{
		"base_arg":      ownImageRow(pass, perSuite, own),
		"no task image": ownImageRow(pass, cmdAgent, plain),
		"scripted":      ownImageRow(pass, &schemas.AgentConfig{Adapter: "scripted"}, own),
		"failed row":    ownImageRow(checkRow{Status: checkFail}, cmdAgent, own),
	} {
		if r.Status == checkWarn {
			t.Errorf("%s: warned: %+v", name, r)
		}
	}
}

// TestCheckGatewayFailure: a check run whose agent never started because the
// gateway could not mint a key (all key slots held by other bench processes,
// seen live on 21 Nov 2026) is a check that could not run, not an agent that
// failed; any other run is left to the rows.
func TestCheckGatewayFailure(t *testing.T) {
	mint := schemas.PartFailure{Stage: schemas.StagePrepare, Part: "gateway/bifrost", Error: "mint key: no free key slot for openai"}
	code := 0
	cases := []struct {
		name string
		run  *schemas.Run
		want bool
	}{
		{"no run", nil, false},
		{"key mint failed", &schemas.Run{Failures: []schemas.PartFailure{mint}}, true},
		{"agent ran", &schemas.Run{Agent: schemas.RunAgentInfo{ExitCode: &code}, Failures: []schemas.PartFailure{mint}}, false},
		{"environment failed", &schemas.Run{Failures: []schemas.PartFailure{{Stage: schemas.StagePrepare, Part: "environment/docker", Error: "x"}}}, false},
		{"run stage failed", &schemas.Run{Failures: []schemas.PartFailure{{Stage: schemas.StageRun, Part: "gateway/bifrost", Error: "x"}}}, false},
	}
	for _, c := range cases {
		got := checkGatewayFailure(c.run)
		if (got != "") != c.want {
			t.Errorf("%s: checkGatewayFailure = %q, want failure %v", c.name, got, c.want)
		}
	}
	if got := checkGatewayFailure(&schemas.Run{Failures: []schemas.PartFailure{mint}}); !strings.Contains(got, "no free key slot") {
		t.Errorf("failure text %q lacks the cause", got)
	}
	rep := (&checkReport{}).fail(errors.New("the check could not run"))
	if checkReportExit(rep) != exitInfra {
		t.Errorf("exit = %d, want %d", checkReportExit(rep), exitInfra)
	}
}
