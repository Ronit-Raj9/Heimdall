package main

import (
	"bytes"
	"context"
	"encoding/json"
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
