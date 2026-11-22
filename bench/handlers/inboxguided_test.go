package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

func passingPush(id, sku string) []byte {
	conv := map[string]any{"id": id, "messages": []any{
		map[string]any{"role": "user", "content": "How many units of " + sku + " are in stock?"},
		map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "c1", "type": "function",
			"function": map[string]any{"name": "get_item", "arguments": `{"sku":"` + sku + `"}`}}}},
		map[string]any{"role": "tool", "tool_call_id": "c1", "content": `{"sku":"` + sku + `","stock":30}`},
		map[string]any{"role": "assistant", "content": "There are 30 units."},
	}}
	b, _ := json.Marshal(map[string]any{"trace": conv, "passed": true, "source": "support-widget"})
	return b
}

func TestInboxGuidedAccept(t *testing.T) {
	root := t.TempDir()
	bench := filepath.Join(root, "benchmarks")
	files, err := scaffold.Suite(scaffold.SuiteOptions{ID: "support", Template: scaffold.TemplateToolUse})
	if err != nil {
		t.Fatal(err)
	}
	if err := scaffold.WriteFiles(filepath.Join(bench, "support"), files, false); err != nil {
		t.Fatal(err)
	}
	one := 1
	cfg := &traceinbox.Config{Sources: []traceinbox.Source{{Name: "hook", Kind: traceinbox.KindWebhook, TokenEnv: "INBOX_TOKEN", RatePerMinute: 60, GoldenPerDay: &one}}}
	ib := traceinbox.New(filepath.Join(root, traceinbox.Dir), cfg, bench)
	ib.ProjectRoot = root
	ib.Getenv = func(k string) string {
		if k == "INBOX_TOKEN" {
			return inboxToken
		}
		return ""
	}
	c := newInboxClient(t, schemas.ModeLive, traceinbox.NewService(ib))
	ingest := APIPrefix + "/inbox/ingest"
	push := func(body []byte) schemas.InboxIngestResult {
		t.Helper()
		resp := c.do(t, "POST", ingest, body, inboxAuth())
		var r schemas.InboxIngestResult
		if resp.StatusCode() >= 300 || json.Unmarshal(resp.Body(), &r) != nil {
			t.Fatalf("push = %d %s", resp.StatusCode(), resp.Body())
		}
		return r
	}
	failed := push(pushBody("t-1", "A-1"))
	golden := push(passingPush("p-1", "W-100"))
	if !golden.Created || golden.ItemID == failed.ItemID {
		t.Fatalf("golden push = %+v", golden)
	}
	if over := push(passingPush("p-2", "W-200")); over.Skipped != 1 || over.Created {
		t.Fatalf("golden over the daily sample = %+v", over)
	}
	resp := c.do(t, "GET", APIPrefix+"/inbox/"+golden.ItemID, nil, nil)
	if !strings.Contains(string(resp.Body()), `"golden":true`) {
		t.Fatalf("golden item = %s", resp.Body())
	}

	accept := APIPrefix + "/inbox/" + failed.ItemID + "/accept"
	guide := `{"suite":"support","task_id":"refund-guided","guide":{"keep_turns":true,"calls":[{"tool":"issue_refund","args":{"order":"A-1"}}],"forbidden":[{"tool":"apply_credit","args":{"order":"A-1"}}]}`
	if resp := c.do(t, "POST", accept, []byte(guide+`}`), authed("application/json")); resp.StatusCode() != 409 || !strings.Contains(string(resp.Body()), "switch_grader") {
		t.Fatalf("guided accept without the switch = %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := c.do(t, "POST", accept, []byte(`{"suite":"support","guide":{"reply_rubric":"Says sorry"},"switch_grader":true}`), authed("application/json")); resp.StatusCode() != 400 || !strings.Contains(string(resp.Body()), "judge") {
		t.Fatalf("rubric without a judge = %d %s", resp.StatusCode(), resp.Body())
	}
	if raw, _ := os.ReadFile(filepath.Join(bench, "support", "suite.yaml")); strings.Contains(string(raw), "grader: checks") {
		t.Fatal("a refused accept switched the grader")
	}
	resp = c.do(t, "POST", accept, []byte(guide+`,"switch_grader":true}`), authed("application/json"))
	var res schemas.InboxAcceptResult
	if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &res) != nil || !res.Draft.Finished || len(res.Draft.EditMe) != 0 {
		t.Fatalf("guided accept = %d %s", resp.StatusCode(), resp.Body())
	}
	checks, err := os.ReadFile(filepath.Join(bench, "support", "tasks", "refund-guided", "verifier", "tests", "checks.yaml"))
	if err != nil || !strings.Contains(string(checks), "issue_refund") || !strings.Contains(string(checks), "tool_not_called") {
		t.Fatalf("checks.yaml = %s (%v)", checks, err)
	}

	bulk := schemas.InboxBulkAcceptRequest{IDs: []string{golden.ItemID, "inc-000000000000"}, Suite: "support", Guide: &schemas.InboxGuide{UseRecorded: true}}
	body, _ := json.Marshal(bulk)
	if resp := c.do(t, "POST", APIPrefix+"/inbox/accept", body, map[string]string{"Content-Type": "application/json"}); resp.StatusCode() != 401 {
		t.Fatalf("bulk accept without the write token = %d", resp.StatusCode())
	}
	resp = c.do(t, "POST", APIPrefix+"/inbox/accept", body, authed("application/json"))
	var br schemas.InboxBulkAcceptResult
	if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &br) != nil || len(br.Accepted) != 1 || len(br.Failed) != 1 || !br.Accepted[0].Draft.Finished {
		t.Fatalf("bulk accept = %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := c.do(t, "POST", APIPrefix+"/inbox/accept", []byte(`{"ids":["../x"],"suite":"support"}`), authed("application/json")); resp.StatusCode() != 400 {
		t.Fatalf("bad id = %d", resp.StatusCode())
	}
}
