package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBehaviourChecksPutValidates(t *testing.T) {
	c := newW441Client(t, w441Index())
	code, body := c.do(t, "PUT", "/behaviour-checks", []byte(`{"rules":[{"id":"bad_kind","title":"Bad","kind":"often","a":{}},{"id":"ok_rule","title":"Fine","kind":"present","a":{"class":"edit"}}]}`), nil)
	if code != 422 {
		t.Fatalf("%d %s", code, body)
	}
	var out BehaviourChecksInvalid
	if err := json.Unmarshal(body, &out); err != nil || len(out.Errors) != 1 || out.Errors[0].Rule != "bad_kind" ||
		out.Errors[0].Error != "rule kind must be one of present, absent, sequence, before, repeat, after_error" {
		t.Fatalf("got %s", body)
	}
	if _, err := os.Stat(filepath.Join(c.root, BehaviourChecksFile)); !os.IsNotExist(err) {
		t.Fatal("a refused PUT wrote the file")
	}
	if code, _ := c.do(t, "PUT", "/behaviour-checks", []byte(`{"rules":[]}`), map[string]string{"Authorization": ""}); code != 401 {
		t.Fatalf("PUT without the write token: %d", code)
	}
}

func TestBehaviourChecksBuiltinIDReserved(t *testing.T) {
	c := newW441Client(t, w441Index())
	code, body := c.do(t, "PUT", "/behaviour-checks", []byte(`{"rules":[{"id":"reads_git_history","title":"Mine","kind":"present","a":{"class":"edit"}}]}`), nil)
	if code != 422 || !strings.Contains(string(body), `"rule":"reads_git_history"`) {
		t.Fatalf("%d %s", code, body)
	}
}

func TestBehaviourVersionBump(t *testing.T) {
	c := newW441Client(t, w441Index())
	put := func(title string) BehaviourChecks {
		t.Helper()
		code, body := c.do(t, "PUT", "/behaviour-checks", []byte(`{"rules":[{"id":"no_force_push","title":"`+title+`","kind":"present","a":{"command":"\\bgit\\s+push\\b.*(--force|-f)\\b"},"version":9}]}`), nil)
		if code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		var out BehaviourChecks
		_ = json.Unmarshal(body, &out)
		return out
	}
	if v := put("Force-pushes").Rules[0].Version; v != 1 {
		t.Fatalf("new rule version %d", v)
	}
	if v := put("Force-pushes").Rules[0].Version; v != 1 {
		t.Fatalf("unchanged rule version %d", v)
	}
	if v := put("Force pushes").Rules[0].Version; v != 2 {
		t.Fatalf("changed rule version %d", v)
	}
	code, body := c.do(t, "GET", "/behaviour-checks", nil, nil)
	if code != 200 || !strings.Contains(string(body), `"no_force_push"`) || !strings.Contains(string(body), `"claimed_without_tests"`) {
		t.Fatalf("get: %d %s", code, body)
	}
}

func TestRunBehaviourRoute(t *testing.T) {
	c := newW441Client(t, w441Index())
	code, body := c.do(t, "GET", "/runs/r2/behaviour", nil, nil)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var out RunBehaviour
	if err := json.Unmarshal(body, &out); err != nil || !out.Available {
		t.Fatalf("got %s", body)
	}
	status := map[string]string{}
	for _, r := range out.Results {
		status[r.RuleID] = r.Status
	}
	if status["claimed_without_tests"] != "fired" || status["edits_protected_tests"] != "fired" || status["reads_git_history"] != "clear" {
		t.Fatalf("statuses %v", status)
	}
	code, body = c.do(t, "GET", "/runs/old/behaviour", nil, nil)
	if code != 200 || !strings.Contains(string(body), `"available":false`) || !strings.Contains(string(body), `"status":"unknown"`) {
		t.Fatalf("old run: %d %s", code, body)
	}
	if code, _ := c.do(t, "GET", "/runs/nope/behaviour", nil, nil); code != 404 {
		t.Fatalf("unknown run: %d", code)
	}
}

func TestExperimentBehaviourSplitsByOutcome(t *testing.T) {
	c := newW441Client(t, w441Index())
	code, body := c.do(t, "GET", "/experiments/exp1/behaviour", nil, nil)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var out ExperimentBehaviour
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	for _, r := range out.Rules {
		if r.Rule.ID != "claimed_without_tests" {
			continue
		}
		cand, base := r.PerArm["cand"], r.PerArm["base"]
		if cand.Fired != 1 || cand.FiredFailed != 1 || cand.FiredPassed != 0 || cand.Runs != 1 {
			t.Fatalf("cand %+v", cand)
		}
		if base.Runs != 3 || base.Fired != 0 || base.Unknown != 1 {
			t.Fatalf("base %+v", base)
		}
		if len(r.Runs["cand"]) != 1 || r.Runs["cand"][0] != "r2" {
			t.Fatalf("runs %v", r.Runs)
		}
		return
	}
	t.Fatalf("rule missing: %s", body)
}
