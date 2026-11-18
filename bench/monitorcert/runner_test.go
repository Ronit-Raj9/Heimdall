package monitorcert

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fixedMonitor answers with fixed responses, or an error.
type fixedMonitor struct {
	resps []schemas.MonitorResponse
	err   error
}

func (f fixedMonitor) Score(context.Context, []schemas.MonitorRequest) ([]schemas.MonitorResponse, error) {
	return f.resps, f.err
}

func ptr(v float64) *float64 { return &v }

func TestScoreRequests(t *testing.T) {
	allow := func(s float64, reason string) schemas.MonitorResponse {
		return schemas.MonitorResponse{Decision: schemas.MonitorAllow, Score: ptr(s), Reason: reason}
	}
	tests := []struct {
		name    string
		reqs    int
		mon     fixedMonitor
		score   float64
		block   bool
		reason  string
		wantErr string
	}{
		{"max score wins", 3, fixedMonitor{resps: []schemas.MonitorResponse{allow(0.2, "a"), allow(0.7, "b"), allow(0.1, "c")}}, 0.7, false, "b", ""},
		{"any block blocks", 2, fixedMonitor{resps: []schemas.MonitorResponse{allow(0.9, "high"), {Decision: "block", Score: ptr(0.3), Reason: "blocked"}}}, 0.9, true, "blocked", ""},
		{"block without score is 1", 1, fixedMonitor{resps: []schemas.MonitorResponse{{Decision: "block"}}}, 1, true, "", ""},
		{"allow without score is 0", 1, fixedMonitor{resps: []schemas.MonitorResponse{{Decision: "allow"}}}, 0, false, "", ""},
		{"too few answers", 2, fixedMonitor{resps: []schemas.MonitorResponse{allow(0, "")}}, 0, false, "", "answered 1 of 2"},
		{"bad decision", 1, fixedMonitor{resps: []schemas.MonitorResponse{{Decision: "maybe"}}}, 0, false, "", "decision"},
		{"score above 1", 1, fixedMonitor{resps: []schemas.MonitorResponse{{Decision: "allow", Score: ptr(1.5)}}}, 0, false, "", "score"},
		{"negative score", 1, fixedMonitor{resps: []schemas.MonitorResponse{{Decision: "allow", Score: ptr(-0.1)}}}, 0, false, "", "score"},
		{"monitor error", 1, fixedMonitor{err: errors.New("container died")}, 0, false, "", "container died"},
		{"no requests", 0, fixedMonitor{}, 0, false, "", "no requests"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := scoreRequests(context.Background(), tc.mon, make([]schemas.MonitorRequest, tc.reqs))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if v.Score != tc.score || v.Block != tc.block || v.Reason != tc.reason || v.Requests != tc.reqs {
				t.Errorf("verdict %+v", v)
			}
		})
	}
}

func TestNewMonitorKinds(t *testing.T) {
	tests := []struct {
		name string
		cfg  schemas.MonitorConfig
		want string
	}{
		{"builtin keyword", schemas.MonitorConfig{Name: "k", Kind: "builtin", Builtin: BuiltinKeyword, Keywords: []string{"x"}}, "*monitorcert.keywordMonitor"},
		{"builtin always allow", schemas.MonitorConfig{Name: "a", Kind: "builtin", Builtin: BuiltinAlwaysAllow}, "*monitorcert.alwaysAllowMonitor"},
		{"command", schemas.MonitorConfig{Name: "c", Kind: "command", Image: "alpine:3.22", Argv: []string{"cat"}}, "*monitorcert.commandMonitor"},
		{"http", schemas.MonitorConfig{Name: "h", Kind: "http", URL: "http://127.0.0.1:1/x"}, "*monitorcert.httpMonitor"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Resolve(tc.cfg, "")
			if err != nil {
				t.Fatal(err)
			}
			m, err := NewMonitor(r, RunnerDeps{})
			if err != nil {
				t.Fatal(err)
			}
			if got := typeName(m); got != tc.want {
				t.Errorf("monitor %s, want %s", got, tc.want)
			}
		})
	}
	if _, err := NewMonitor(&Resolved{Config: schemas.MonitorConfig{Kind: "wasm"}}, RunnerDeps{}); err == nil {
		t.Error("unknown kind accepted")
	}
}

func typeName(v any) string {
	switch v.(type) {
	case *keywordMonitor:
		return "*monitorcert.keywordMonitor"
	case *alwaysAllowMonitor:
		return "*monitorcert.alwaysAllowMonitor"
	case *commandMonitor:
		return "*monitorcert.commandMonitor"
	case *httpMonitor:
		return "*monitorcert.httpMonitor"
	}
	return "unknown"
}
