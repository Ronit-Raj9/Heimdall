package evidence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func toolTask(services ...schemas.ServiceSpec) *schemas.RunContext {
	return &schemas.RunContext{Run: &schemas.Run{RunID: "r_tool"}, Task: &schemas.Task{ID: "t", Services: services}, Suite: &schemas.Suite{ID: "s"}}
}

func TestToolLedgerCollect(t *testing.T) {
	mcp := schemas.ServiceSpec{ID: "fs", Role: schemas.ServiceRoleMCP}
	row := `{"seq":1,"server":"fs","tool":"read_file","protocol":"mcp","args_sha256":"a","result_sha256":"b","is_error":false,"started":"2026-11-15T10:00:00Z","latency_ms":3}`
	tests := []struct {
		name      string
		rc        *schemas.RunContext
		file      *string
		wantErr   error
		wantNil   bool
		wantCalls int
	}{
		{name: "no proxied service: nothing to do", rc: toolTask(schemas.ServiceSpec{ID: "db", Role: schemas.ServiceRoleData}), wantNil: true},
		{name: "missing ledger is a failure, calls unknown", rc: toolTask(mcp), wantErr: errNoToolLedger, wantNil: true},
		{name: "empty ledger is zero calls", rc: toolTask(mcp), file: toolLedgerText(""), wantCalls: 0},
		{name: "rows read, torn line skipped", rc: toolTask(mcp), file: toolLedgerText(row + "\n" + `{"seq":2,"ser`), wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.rc.Dir = dir
			if tt.file != nil {
				if err := os.WriteFile(filepath.Join(dir, toolsFile), []byte(*tt.file), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			ev := &schemas.Evidence{}
			err := newToolLedgerSource(Deps{}).Collect(context.Background(), tt.rc, ev)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if (ev.ToolCalls == nil) != tt.wantNil {
				t.Fatalf("ToolCalls nil = %v, want %v", ev.ToolCalls == nil, tt.wantNil)
			}
			if !tt.wantNil && len(ev.ToolCalls) != tt.wantCalls {
				t.Fatalf("got %d calls, want %d", len(ev.ToolCalls), tt.wantCalls)
			}
		})
	}
}

func TestToolLedgerNoRunFolder(t *testing.T) {
	rc := toolTask(schemas.ServiceSpec{ID: "fs", Role: schemas.ServiceRoleMCP})
	err := newToolLedgerSource(Deps{}).Collect(context.Background(), rc, &schemas.Evidence{})
	if err == nil || !strings.Contains(err.Error(), "no run folder") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseToolLedger(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantRows int
		wantBad  int
	}{
		{name: "empty", in: "", wantRows: 0},
		{name: "blank lines", in: "\n\n", wantRows: 0},
		{name: "good and bad", in: `{"seq":1,"server":"a","tool":"x"}` + "\nnot json\n" + `{"server":"no seq"}` + "\n", wantRows: 1, wantBad: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, bad, err := ParseToolLedger(strings.NewReader(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			if rows == nil || len(rows) != tt.wantRows || bad != tt.wantBad {
				t.Fatalf("rows %v bad %d", rows, bad)
			}
		})
	}
}

func TestProxiedServices(t *testing.T) {
	yes, no := true, false
	rc := &schemas.RunContext{
		Suite: &schemas.Suite{Services: []schemas.ServiceSpec{{ID: "suite-tool", Role: schemas.ServiceRoleTool}}},
		Task: &schemas.Task{Services: []schemas.ServiceSpec{
			{ID: "mcp", Role: schemas.ServiceRoleMCP},
			{ID: "db", Role: schemas.ServiceRoleData},
			{ID: "db-proxied", Role: schemas.ServiceRoleData, Proxy: &yes},
			{ID: "mcp-direct", Role: schemas.ServiceRoleMCP, Proxy: &no},
			{ID: "hidden", Role: schemas.ServiceRoleTool, Hidden: true},
		}},
	}
	got := strings.Join(ProxiedServices(rc), ",")
	if got != "suite-tool,mcp,db-proxied" {
		t.Fatalf("got %s", got)
	}
	if ProxiedServices(nil) != nil {
		t.Fatal("nil run context has services")
	}
}

func toolLedgerText(s string) *string { return &s }
