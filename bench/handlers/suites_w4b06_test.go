package handlers

import (
	"testing"
	"time"
)

// TestSuitesLightAlias: GET /suites?light=1 answers the light list, the
// way ?fields=light does (N194: the tester's form of the parameter).
func TestSuitesLightAlias(t *testing.T) {
	shortBudget(t, 5*time.Second)
	src := &stampedSuites{fakeSuites: testSuites(t), dir: t.TempDir(), taskCount: 3}
	src.set("m1", nil)
	c := newSuitesClient(t, src, nil, nil)
	resp := c.do(t, "GET", APIPrefix+"/suites?light=1", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("light=1 status %d: %s", resp.StatusCode(), resp.Body())
	}
	s := suiteByID(t, decodeBody(t, resp), "canary")
	if v, ok := s["tasks"].([]any); !ok || len(v) != 0 {
		t.Fatalf("light=1 canary tasks = %v, want []", s["tasks"])
	}
	if _, ok := s["ready_known"]; !ok {
		t.Fatalf("light=1 canary has no ready_known: %v", s)
	}
}

// TestGetOneSuite: GET /suites/{id} answers one suite's full summary, and
// 404 for an id that is not listed.
func TestGetOneSuite(t *testing.T) {
	c := newSuitesClient(t, testSuites(t), nil, nil)
	resp := c.do(t, "GET", APIPrefix+"/suites/canary", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("one suite status %d: %s", resp.StatusCode(), resp.Body())
	}
	one := decodeBody(t, resp)
	full := suiteByID(t, decodeBody(t, c.do(t, "GET", APIPrefix+"/suites", nil, nil)), "canary")
	if one["id"] != "canary" || one["task_count"] != full["task_count"] || one["lock_state"] != full["lock_state"] {
		t.Fatalf("one suite %v, list entry %v", one, full)
	}
	if tasks, _ := one["tasks"].([]any); len(tasks) == 0 {
		t.Fatalf("one suite has no tasks: %v", one)
	}
	if resp := c.do(t, "GET", APIPrefix+"/suites/nope", nil, nil); resp.StatusCode() != 404 {
		t.Fatalf("unknown suite status %d: %s", resp.StatusCode(), resp.Body())
	}
}
