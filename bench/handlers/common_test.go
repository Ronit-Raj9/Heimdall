package handlers

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

// TestRoutesStatusAndMode drives every route once and checks the status and
// that every JSON answer, error or not, carries the mode at the top level.
func TestRoutesStatusAndMode(t *testing.T) {
	c := newTestClient(t, fullDeps())
	expYAML := []byte("name: demo\nsuite: coding-core@1.0.0\nconditions:\n  - {id: clean, tasks: all, trials: 1}\n")
	tests := []struct {
		name, method, path string
		body               []byte
		headers            map[string]string
		status             int
	}{
		{"health", "GET", "/api/bench/health", nil, nil, 200},
		{"doctor", "GET", "/api/bench/doctor", nil, nil, 200},
		{"catalog", "GET", "/api/bench/catalog", nil, nil, 200},
		{"list experiments", "GET", "/api/bench/experiments", nil, nil, 200},
		{"plan", "POST", "/api/bench/experiments/plan", expYAML, authed("application/yaml"), 200},
		{"create", "POST", "/api/bench/experiments", expYAML, authed("application/yaml"), 201},
		{"start", "POST", "/api/bench/experiments/e_test/start", nil, authed(""), 202},
		{"start unknown", "POST", "/api/bench/experiments/e_nope/start", nil, authed(""), 404},
		{"get experiment", "GET", "/api/bench/experiments/e_test", nil, nil, 200},
		{"get unknown experiment", "GET", "/api/bench/experiments/e_nope", nil, nil, 404},
		{"verdict", "GET", "/api/bench/experiments/e_test/verdict", nil, nil, 200},
		{"verdict bad grade source", "GET", "/api/bench/experiments/e_test/verdict?grade_source=../x", nil, nil, 400},
		{"diagnostics", "GET", "/api/bench/experiments/e_test/diagnostics", nil, nil, 200},
		{"grid", "GET", "/api/bench/experiments/e_test/grid", nil, nil, 200},
		{"events poll", "GET", "/api/bench/experiments/e_test/events?since=0", nil, nil, 200},
		{"events bad since", "GET", "/api/bench/experiments/e_test/events?since=x", nil, nil, 400},
		{"run", "GET", "/api/bench/runs/r_t01_A", nil, nil, 200},
		{"run unknown", "GET", "/api/bench/runs/r_nope", nil, nil, 404},
		{"run events", "GET", "/api/bench/runs/r_t01_A/events", nil, nil, 200},
		{"run diff json", "GET", "/api/bench/runs/r_t01_A/diff", nil, nil, 200},
		{"run receipts", "GET", "/api/bench/runs/r_t01_A/receipts", nil, nil, 200},
		{"run file", "GET", "/api/bench/runs/r_t01_A/files/run.json", nil, nil, 404}, // allowlisted, absent
		{"pair", "GET", "/api/bench/pairs/p_t01", nil, nil, 200},
		{"pair unknown", "GET", "/api/bench/pairs/p_nope", nil, nil, 404},
		{"audit csv", "POST", "/api/bench/audit", []byte("task,a_pass,a_n,b_pass,b_n\nt1,1,3,2,3\n"), authed("text/csv"), 200},
		{"audit json", "POST", "/api/bench/audit", []byte(`{"rows":[{"task":"t1","a_pass":1,"a_n":3,"b_pass":2,"b_n":3}]}`), authed("application/json"), 200},
		{"audit bad csv", "POST", "/api/bench/audit", []byte("nope"), authed("text/csv"), 400},
		{"unknown route", "GET", "/api/bench/nope", nil, nil, 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := c.do(t, tt.method, tt.path, tt.body, tt.headers)
			if resp.StatusCode() != tt.status {
				t.Fatalf("status = %d, want %d; body %s", resp.StatusCode(), tt.status, resp.Body())
			}
			if got := string(resp.Header.Peek(lib.ModeHeader)); got != string(testMode) {
				t.Errorf("mode header = %q, want %q", got, testMode)
			}
			if !strings.HasPrefix(string(resp.Header.ContentType()), "application/json") {
				return
			}
			body := decodeBody(t, resp)
			if body["mode"] != string(testMode) {
				t.Errorf("mode = %v, want %q in %s", body["mode"], testMode, resp.Body())
			}
			if tt.status >= 400 {
				errObj, ok := body["error"].(map[string]any)
				if !ok || errObj["message"] == "" || errObj["type"] == "" {
					t.Errorf("error shape wrong: %s", resp.Body())
				}
			}
		})
	}
}

func TestWriteRoutesNeedToken(t *testing.T) {
	c := newTestClient(t, fullDeps())
	for _, path := range []string{"/api/bench/experiments/plan", "/api/bench/experiments", "/api/bench/experiments/e_test/start", "/api/bench/audit"} {
		for name, headers := range map[string]map[string]string{
			"missing": nil,
			"wrong":   {"Authorization": "Bearer nope"},
			"scheme":  {"Authorization": testToken},
		} {
			t.Run(path+" "+name, func(t *testing.T) {
				resp := c.do(t, "POST", path, []byte("x"), headers)
				if resp.StatusCode() != fasthttp.StatusUnauthorized {
					t.Fatalf("status = %d, want 401", resp.StatusCode())
				}
				if decodeBody(t, resp)["mode"] != string(testMode) {
					t.Errorf("401 without mode: %s", resp.Body())
				}
			})
		}
	}
}

func TestBodyOverLimitIs413(t *testing.T) {
	c := newTestClient(t, fullDeps())
	big := []byte(strings.Repeat("a", lib.MaxBodyBytes+1))
	resp := c.do(t, "POST", "/api/bench/audit", big, authed("text/csv"))
	if resp.StatusCode() != fasthttp.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode())
	}
}

func TestMissingDependencyIs503(t *testing.T) {
	c := newTestClient(t, testDeps{})
	for _, path := range []string{"/api/bench/doctor", "/api/bench/catalog", "/api/bench/experiments", "/api/bench/runs/r_1", "/api/bench/pairs/p_1"} {
		t.Run(path, func(t *testing.T) {
			if got := c.do(t, "GET", path, nil, nil).StatusCode(); got != fasthttp.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", got)
			}
		})
	}
	if got := c.do(t, "POST", "/api/bench/audit", []byte("x"), authed("text/csv")).StatusCode(); got != fasthttp.StatusServiceUnavailable {
		t.Fatalf("audit status = %d, want 503", got)
	}
}

func TestValidID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"e_codewins", true}, {"r_p20fj3s8wa", true}, {"v1.2-x", true},
		{"", false}, {".", false}, {"..", false}, {"a/b", false}, {"a b", false}, {strings.Repeat("a", 129), false},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			if got := validID(tt.id); got != tt.want {
				t.Errorf("validID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}
