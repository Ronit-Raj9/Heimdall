package traceinbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/traceimport"
)

// chatTrace is a pushed chat transcript: a refund request with a planted
// key and email, a failing tool call, and an answer that claims success.
func chatTrace(id, order string, secs int) json.RawMessage {
	conv := map[string]any{"id": id, "model": "gpt-x", "status": "failed", "messages": []any{
		map[string]any{"role": "user", "content": fmt.Sprintf("Refund order %s, the tent arrived torn. Mail %s; my key is %s", order, plantedEmail, plantedKey)},
		map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "c1", "type": "function",
			"function": map[string]any{"name": "issue_refund", "arguments": fmt.Sprintf(`{"order":%q,"api_key":%q}`, order, plantedKey)}}}},
		map[string]any{"role": "tool", "tool_call_id": "c1", "content": fmt.Sprintf("Error: timeout after %ds", secs)},
		map[string]any{"role": "assistant", "content": "Your refund was issued."},
	}}
	b, _ := json.Marshal(conv)
	return b
}

var webhookCfg = &Config{Sources: []Source{{Name: "hook", Kind: KindWebhook, TokenEnv: "INBOX_TOKEN", RatePerMinute: 2, MaxBytes: 4096, Suite: "tools"}}}

func ingest(t *testing.T, ib *Inbox, trace json.RawMessage, reason string) *IngestResult {
	t.Helper()
	src, _ := ib.Config.Webhook()
	res, err := ib.Ingest(context.Background(), src, IngestRequest{Trace: trace, Reason: reason, Link: "https://obs.example/t/1"})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestDedupeBySignature(t *testing.T) {
	ib, _ := newTestInbox(t, webhookCfg, nil)
	a := ingest(t, ib, chatTrace("t-1", "A-1001", 30), "thumbs down")
	b := ingest(t, ib, chatTrace("t-2", "A-2002", 45), "thumbs down")
	again := ingest(t, ib, chatTrace("t-2", "A-2002", 45), "thumbs down")
	other := ingest(t, ib, json.RawMessage(`{"id":"t-3","messages":[{"role":"user","content":"What sizes does the tent come in?"},{"role":"assistant","content":"S and M."}]}`), "")
	if !a.Created || b.Created || b.ItemID != a.ItemID || b.Occurrences != 2 {
		t.Fatalf("a = %+v, b = %+v: the same failure on another order must collapse", a, b)
	}
	if again.Occurrences != 2 {
		t.Errorf("the same trace pushed twice counted twice: %+v", again)
	}
	if !other.Created || other.ItemID == a.ItemID {
		t.Errorf("a different request shared the item: %+v", other)
	}
	items := mustItems(t, ib)
	if len(items) != 2 {
		t.Fatalf("items = %d", len(items))
	}
	it := items[0]
	if it.Occurrences != 2 || len(it.Traces) != 2 || it.SuggestedKind != "tool-use" || it.SuggestedSuite != "tools" || it.Link != "https://obs.example/t/1" ||
		it.SignatureParts.Tool != "issue_refund" || it.SignatureParts.Error != "error: timeout after <n>s" {
		t.Errorf("item = %+v", it)
	}
	if items[1].SignatureParts.Error != "flag:pushed" || items[1].SuggestedKind != "qa" {
		t.Errorf("second item = %+v", items[1])
	}
	checkNoLeak(t, ib.Store.(*FileStore).Dir())
}

func TestSignature(t *testing.T) {
	sess := func(req, tool, errText string) *traceimport.Session {
		s := &traceimport.Session{Turns: []traceimport.Turn{{Role: "user", Text: req}}, Outcome: traceimport.Outcome{Status: traceimport.OutcomeError}}
		if tool != "" {
			s.ToolCalls = []traceimport.ToolCall{{Name: tool, IsError: true, Error: errText}}
		}
		return s
	}
	base, parts := Signature(sess("Please refund order 1001 for the torn tent today", "issue_refund", "timeout after 30s at 2026-11-18T05:00:00Z"), nil)
	same, _ := Signature(sess("please refund order 2002 for the torn tent today", "issue_refund", "Timeout after 31s at 2026-11-17T01:00:00Z"), nil)
	otherTool, _ := Signature(sess("Please refund order 1001 for the torn tent today", "lookup_order", "timeout after 30s"), nil)
	otherErr, _ := Signature(sess("Please refund order 1001 for the torn tent today", "issue_refund", "403 forbidden"), nil)
	otherReq, _ := Signature(sess("Cancel my subscription right now please", "issue_refund", "timeout after 30s"), nil)
	if base != same {
		t.Errorf("numbers, case and timestamps changed the signature: %s vs %s", base, same)
	}
	for name, sig := range map[string]string{"tool": otherTool, "error": otherErr, "request": otherReq} {
		if sig == base {
			t.Errorf("a different %s kept the signature", name)
		}
	}
	if !strings.HasPrefix(base, "f_") || len(base) != 18 || parts.Tool != "issue_refund" || !ValidItemID(ItemID(base)) {
		t.Errorf("signature %s, parts %+v", base, parts)
	}
	score1, _ := Signature(&traceimport.Session{Turns: []traceimport.Turn{{Role: "user", Text: "hi there friend"}}}, []string{"score:helpfulness"})
	fb, _ := Signature(&traceimport.Session{Turns: []traceimport.Turn{{Role: "user", Text: "hi there friend"}}}, []string{"feedback:thumbs"})
	if score1 == fb {
		t.Error("a low score and negative feedback on the same request share a signature")
	}
}

func TestAcceptDraftValidates(t *testing.T) {
	ib, _ := newTestInbox(t, webhookCfg, nil)
	res := ingest(t, ib, chatTrace("t-1", "A-1001", 30), "thumbs down")
	ctx := context.Background()
	if _, err := ib.Accept(ctx, res.ItemID, AcceptOptions{Suite: "nope"}); err == nil {
		t.Error("accept into a missing suite passed")
	}
	acc, err := ib.Accept(ctx, res.ItemID, AcceptOptions{Suite: "tools", TaskID: "refund-timeout"})
	if err != nil {
		t.Fatal(err)
	}
	if acc.Item.State != StateAccepted || acc.Item.Task == nil || TaskKey(acc.Item.Task) != "tools/refund-timeout" || acc.Item.Task.Dir != "benchmarks/tools/tasks/refund-timeout" {
		t.Fatalf("accepted item = %+v", acc.Item)
	}
	if !acc.Draft.Replayed || len(acc.Draft.Mutants) != 1 || len(acc.Draft.EditMe) == 0 {
		t.Errorf("draft = %+v; want the replay server, the observed-calls mutant and EDIT ME files", acc.Draft)
	}
	if _, err := ib.Accept(ctx, res.ItemID, AcceptOptions{Suite: "tools"}); !errors.Is(err, ErrConflict) {
		t.Errorf("second accept: %v, want a conflict", err)
	}
	st, task, err := suite.LoadTask(ib.BenchmarksRoot, "tools", "refund-timeout")
	if err != nil {
		t.Fatalf("draft does not load: %v", err)
	}
	if task.Provenance == nil || task.Provenance.Incident == nil || task.Provenance.Incident.ID != res.ItemID ||
		task.Provenance.Incident.Source != "hook" || task.Provenance.Incident.Signature != acc.Item.Signature || task.Provenance.CreatedBy != "task from-trace" {
		t.Errorf("provenance = %+v", task.Provenance)
	}
	if !contains(task.Tags, RegressionTag) {
		t.Errorf("tags = %v", task.Tags)
	}
	editMe := false
	for _, p := range suite.ValidateTask(ctx, st, task) {
		if p.Severity == suite.SeverityError && strings.Contains(p.Message, "EDIT ME") {
			editMe = true
		}
	}
	if !editMe {
		t.Fatal("task validate passed a draft that still holds EDIT ME markers")
	}
	removeEditMe(t, acc.Draft.Dir)
	st, task, err = suite.LoadTask(ib.BenchmarksRoot, "tools", "refund-timeout")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range suite.ValidateTask(ctx, st, task) {
		if p.Severity == suite.SeverityError {
			t.Errorf("after the EDIT ME lines are gone: %s: %s", p.Path, p.Message)
		}
	}
	checkNoLeak(t, ib.Store.(*FileStore).Dir(), acc.Draft.Dir)
	if findings, err := suite.ScanSecrets(acc.Draft.Dir); err != nil || len(findings) > 0 {
		t.Errorf("secret scan: %v %v", findings, err)
	}
	inc, err := IncidentTasks(ib.BenchmarksRoot)
	if err != nil || len(inc) != 1 || inc["tools/refund-timeout"].Incident.ID != res.ItemID {
		t.Errorf("incident tasks = %+v, %v", inc, err)
	}

	// The same failure again, after the accept: the item counts a
	// recurrence and shows the release status of its task.
	ib.Results = fakeResults{"tools/refund-timeout": {Status: ReleaseFails, Experiment: "e_9", Passed: 1, Runs: 3}}
	again := ingest(t, ib, chatTrace("t-9", "A-9009", 12), "thumbs down")
	if again.Created || again.ItemID != res.ItemID {
		t.Fatalf("recurrence = %+v", again)
	}
	it, _, err := ib.Show(ctx, res.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if it.Recurrences != 1 || it.LastRecurrence == nil || it.Release == nil || it.Release.Status != ReleaseFails || it.State != StateAccepted {
		t.Errorf("recurred item = %+v", it)
	}
}

type fakeResults map[string]*ReleaseStatus

func (f fakeResults) Latest(_ context.Context, suite, task string) (*ReleaseStatus, error) {
	return f[suite+"/"+task], nil
}

// removeEditMe deletes every EDIT ME line, as a person does after
// confirming each judgement (the same helper as traceimport's tests).
func removeEditMe(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var kept []string
		for _, line := range strings.Split(string(b), "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, scaffold.EditMeMarker) || strings.HasPrefix(l, "# EDIT ME") {
				continue
			}
			kept = append(kept, line)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(p, []byte(strings.Join(kept, "\n")), info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRejectSnoozeStates(t *testing.T) {
	ib, _ := newTestInbox(t, webhookCfg, nil)
	a := ingest(t, ib, chatTrace("t-1", "A-1", 30), "x")
	b := ingest(t, ib, json.RawMessage(`{"id":"t-2","messages":[{"role":"user","content":"Where is my parcel?"}]}`), "x")
	c := ingest(t, ib, json.RawMessage(`{"id":"t-3","messages":[{"role":"user","content":"Delete my account"}]}`), "x")
	if _, err := ib.Reject(a.ItemID, " ", ""); err == nil {
		t.Error("a reject without a reason passed")
	}
	if it, err := ib.Reject(a.ItemID, "expected: the order was already refunded", ""); err != nil || it.State != StateRejected {
		t.Fatalf("reject = %+v, %v", it, err)
	}
	if _, err := ib.Snooze(a.ItemID, 3); !errors.Is(err, ErrConflict) {
		t.Errorf("snooze of a rejected item: %v", err)
	}
	if it, err := ib.Reject(b.ItemID, "same as the other parcel question", c.ItemID); err != nil || it.State != StateDuplicate || it.DuplicateOf != c.ItemID {
		t.Errorf("duplicate = %+v, %v", it, err)
	}
	if _, err := ib.Snooze(c.ItemID, 0); err == nil {
		t.Error("a zero-day snooze passed")
	}
	it, err := ib.Snooze(c.ItemID, 2)
	if err != nil || it.State != StateSnoozed {
		t.Fatalf("snooze = %+v, %v", it, err)
	}
	counts, _ := ib.Counts()
	if counts[StateSnoozed] != 1 || counts[StateRejected] != 1 || counts[StateDuplicate] != 1 || counts[StateNew] != 0 {
		t.Errorf("counts = %v", counts)
	}
	ib.Now = func() time.Time { return testNow.Add(72 * time.Hour) }
	if l, _ := ib.List(context.Background(), StateNew, ""); len(l) != 1 || l[0].ID != c.ItemID {
		t.Errorf("after the snooze ran out, new = %+v", l)
	}
	if _, err := ib.Reject("inc-000000000000", "x", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown item: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(ib.Store.(*FileStore).Dir(), LogFile))
	if err != nil || !strings.Contains(string(data), `"event":"duplicate"`) || !strings.Contains(string(data), `"event":"snooze"`) {
		t.Errorf("log = %s, %v", data, err)
	}
}

func TestOTLPDirPull(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Sources: []Source{{Name: "collector", Kind: KindOTLPDir, Path: dir}}}
	ib, _ := newTestInbox(t, cfg, nil)
	span := func(trace, name string, status int, attrs map[string]string) string {
		var kv []any
		for k, v := range attrs {
			kv = append(kv, map[string]any{"key": k, "value": map[string]any{"stringValue": v}})
		}
		s := map[string]any{"traceId": trace, "spanId": trace[:8] + name[:2], "name": name, "startTimeUnixNano": fmt.Sprint(testNow.Add(-time.Hour).UnixNano()),
			"endTimeUnixNano": fmt.Sprint(testNow.Add(-time.Hour + time.Second).UnixNano()), "attributes": kv, "status": map[string]any{"code": status, "message": "boom"}}
		b, _ := json.Marshal(map[string]any{"resourceSpans": []any{map[string]any{"scopeSpans": []any{map[string]any{"spans": []any{s}}}}}})
		return string(b) + "\n"
	}
	msgs := `[{"role":"user","parts":[{"type":"text","content":"Book a table for 4, I am ` + plantedEmail + `"}]}]`
	file := filepath.Join(dir, "traces.jsonl")
	lines := span("aaaaaaaaaaaaaaaa1111111111111111", "chat model", 0, map[string]string{"gen_ai.operation.name": "chat", "gen_ai.input.messages": msgs}) +
		span("aaaaaaaaaaaaaaaa1111111111111111", "execute_tool book", 2, map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "book_table"}) +
		span("bbbbbbbbbbbbbbbb2222222222222222", "chat ok", 1, map[string]string{"gen_ai.input.messages": msgs}) +
		"not json\n" + `{"resourceSpans":[` // a line still being written
	if err := os.WriteFile(file, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ib.Pull(context.Background(), PullOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].New != 1 || res[0].Fetched != 1 || !strings.Contains(strings.Join(res[0].Warnings, ";"), "not JSON") {
		t.Fatalf("pull = %+v", res[0])
	}
	curs, _ := ib.Store.Cursors()
	off := curs["collector"].Files["traces.jsonl"]
	if off != int64(len(lines)-len(`{"resourceSpans":[`)) {
		t.Errorf("offset = %d; the half-written line must stay unread", off)
	}
	// The next pull reads only what was appended.
	f, _ := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString(`]}` + "\n" + span("cccccccccccccccc3333333333333333", "execute_tool book", 2, map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "book_table"}))
	f.Close()
	res, err = ib.Pull(context.Background(), PullOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Fetched != 1 {
		t.Errorf("second pull = %+v, want only the appended error trace", res[0])
	}
	checkNoLeak(t, ib.Store.(*FileStore).Dir())
}

// TestPullFailedSessionWriteRetries is audit 5-C-05: when the session of a
// new item cannot be written, no item is listed and the source position is
// not advanced, so the next pull takes the trace in with its session.
func TestPullFailedSessionWriteRetries(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Sources: []Source{{Name: "collector", Kind: KindOTLPDir, Path: dir}}}
	ib, _ := newTestInbox(t, cfg, nil)
	s := map[string]any{"traceId": "dddddddddddddddd4444444444444444", "spanId": "dddddddd0001", "name": "execute_tool book",
		"startTimeUnixNano": fmt.Sprint(testNow.Add(-time.Hour).UnixNano()), "endTimeUnixNano": fmt.Sprint(testNow.Add(-time.Hour + time.Second).UnixNano()),
		"attributes": []any{map[string]any{"key": "gen_ai.operation.name", "value": map[string]any{"stringValue": "execute_tool"}},
			map[string]any{"key": "gen_ai.tool.name", "value": map[string]any{"stringValue": "book_table"}}},
		"status": map[string]any{"code": 2, "message": "boom"}}
	b, _ := json.Marshal(map[string]any{"resourceSpans": []any{map[string]any{"scopeSpans": []any{map[string]any{"spans": []any{s}}}}}})
	if err := os.WriteFile(filepath.Join(dir, "traces.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	store := ib.Store.(*FileStore).Dir()
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(store, ItemsDir)
	if err := os.WriteFile(blocker, []byte("not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ib.Pull(context.Background(), PullOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Fetched != 1 || res[0].New != 0 || len(res[0].Warnings) == 0 {
		t.Fatalf("blocked pull = %+v", res[0])
	}
	if items, _ := ib.Store.Items(); len(items) != 0 {
		t.Fatalf("an item without its session was listed: %+v", items)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	res, err = ib.Pull(context.Background(), PullOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Fetched != 1 || res[0].New != 1 {
		t.Fatalf("retry pull = %+v, want the trace taken in", res[0])
	}
	items, _ := ib.Store.Items()
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	if _, err := ib.Store.Session(items[0].ID); err != nil {
		t.Fatalf("session of %s: %v", items[0].ID, err)
	}
}

func TestGate(t *testing.T) {
	g := NewGate("tok-123", 2, 100)
	if !g.Enabled() || !g.Check("Bearer tok-123") || !g.Check("tok-123") || g.Check("Bearer tok-1234") || g.Check("") || g.Check("Bearer ") {
		t.Error("token compare is wrong")
	}
	if NewGate("", 1, 1).Enabled() || NewGate("", 1, 1).Check("") {
		t.Error("a gate without a token let something in")
	}
	now := testNow
	if !g.Allow(now) || !g.Allow(now) || g.Allow(now) {
		t.Error("the bucket of 2 per minute allowed a third push at once")
	}
	if !g.Allow(now.Add(31 * time.Second)) {
		t.Error("the bucket did not refill")
	}
}

func TestIncidentSection(t *testing.T) {
	inc := map[string]IncidentTask{"tools/refund-timeout": {Suite: "tools", Task: "refund-timeout",
		Incident: schemas.TaskIncident{ID: "inc-0123456789ab", Source: "prod-langfuse", TraceID: "lf-1", FirstSeen: "2026-11-02T10:00:00Z"}},
		"tools/held": {Suite: "tools", Task: "held", Incident: schemas.TaskIncident{ID: "inc-ba9876543210", FirstSeen: "2026-11-05T10:00:00Z"}}}
	run := func(task, arm string, passed bool) schemas.SealedRun {
		return schemas.SealedRun{Slot: schemas.Slot{Suite: "tools@1.0.0", Task: task, Arm: arm}, Grade: &schemas.Grade{Passed: passed}}
	}
	runs := []schemas.SealedRun{run("refund-timeout", "candidate", false), run("refund-timeout", "candidate", true), run("refund-timeout", "baseline", true),
		run("held", "candidate", true), run("plain", "candidate", false)}
	sec := IncidentSection(runs, inc)
	for _, want := range []string{"## Production incidents", "2 of 3 tasks in this experiment came from production incidents", "1 failed a run",
		"Regression of incident inc-0123456789ab from 2026-11-02 (prod-langfuse trace lf-1): task tools/refund-timeout failed 1 of 2 runs in arm candidate.",
		"Incident inc-ba9876543210 from 2026-11-05: task tools/held passed every graded run."} {
		if !strings.Contains(sec, want) {
			t.Errorf("section lacks %q:\n%s", want, sec)
		}
	}
	if IncidentSection(runs[4:], inc) != "" {
		t.Error("an experiment without incident tasks got a section")
	}
}

func TestConfigValidate(t *testing.T) {
	bad := map[string]Source{
		"inline credentials": {Name: "a", Kind: KindLangSmith, Host: "https://user:pw@api.example", APIKeyEnv: "K", Project: "p", Filters: Filters{Errors: true}},
		"secret not a var":   {Name: "a", Kind: KindLangfuse, Host: "https://x.example", PublicKeyEnv: "pk-lf-123", SecretKeyEnv: "S", Filters: Filters{LevelError: true}},
		"field of another":   {Name: "a", Kind: KindOTLPDir, Path: "/tmp", Filters: Filters{MinRetries: 2}},
		"no filter":          {Name: "a", Kind: KindLangfuse, Host: "https://x.example", PublicKeyEnv: "P", SecretKeyEnv: "S"},
		"bad name":           {Name: "Prod Langfuse", Kind: KindWebhook, TokenEnv: "T"},
		"webhook no token":   {Name: "a", Kind: KindWebhook},
		"unknown kind":       {Name: "a", Kind: "datadog"},
	}
	for name, s := range bad {
		if err := (&Config{Sources: []Source{s}}).Validate(); err == nil {
			t.Errorf("%s: passed", name)
		}
	}
	dir := t.TempDir()
	p := filepath.Join(dir, SourcesFile)
	_ = os.WriteFile(p, []byte("sources:\n  - name: g\n    kind: bifrost\n    filters: {errors: true, min_retry: 2}\n"), 0o644)
	if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), "min_retry") {
		t.Errorf("a misspelled filter loaded: %v", err)
	}
	_ = os.WriteFile(p, []byte("schedule: \"*/30 * * * *\"\nsources:\n  - name: g\n    kind: bifrost\n    filters: {errors: true, window: 7d}\n"), 0o644)
	c, err := LoadConfig(p)
	if err != nil || time.Duration(c.Sources[0].Filters.Window) != 7*24*time.Hour || c.Schedule == "" {
		t.Errorf("config = %+v, %v", c, err)
	}
}

func TestServiceIngestOff(t *testing.T) {
	ib, _ := newTestInbox(t, nil, nil)
	svc := NewService(ib)
	if svc.Gate() != nil {
		t.Fatal("the webhook is on without a source")
	}
	if _, err := svc.Ingest(context.Background(), []byte(`{}`)); !errors.Is(err, ErrOff) {
		t.Errorf("ingest = %v", err)
	}
	l, err := svc.List(context.Background(), "", "")
	if err != nil || l.Items == nil || len(l.Suites) != 3 || l.IngestEnabled {
		t.Errorf("list = %+v, %v", l, err)
	}
}
