package shadowreplay

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/replayrec"
)

func strp(s string) *string { return &s }

var t0 = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

// chat is a chat completions answer body with text.
func chat(text string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}}})
	return string(b)
}

// toolAnswer is a chat completions answer body with one tool call.
func toolAnswer(id, name, args string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "",
		"tool_calls": []any{map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}}}}, "finish_reason": "tool_calls"}}})
	return string(b)
}

type msg = map[string]any

func reqBody(msgs []msg, tools bool) string {
	b := map[string]any{"model": "openai/gpt-4o", "messages": msgs}
	if tools {
		b["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup"}}}
	}
	out, _ := json.Marshal(b)
	return string(out)
}

func asstTool(id, name, args string) msg {
	return msg{"role": "assistant", "content": "", "tool_calls": []any{msg{"id": id, "type": "function", "function": msg{"name": name, "arguments": args}}}}
}

// session: user asks, model calls lookup(a), then lookup(b), then answers;
// then the user asks again and the model answers again.
func session(t *testing.T) *replayrec.Recording {
	t.Helper()
	sys := msg{"role": "system", "content": "You help."}
	u1 := msg{"role": "user", "content": "Find order 7."}
	a1 := asstTool("c1", "lookup", `{"id":"7","kind":"order"}`)
	r1 := msg{"role": "tool", "tool_call_id": "c1", "content": "order 7: shipped"}
	a2 := asstTool("c2", "lookup", `{"id":"7","kind":"invoice"}`)
	r2 := msg{"role": "tool", "tool_call_id": "c2", "content": "invoice 7: paid"}
	fin := msg{"role": "assistant", "content": "Order 7 shipped and is paid."}
	u2 := msg{"role": "user", "content": "Thanks, when does it arrive?"}
	rows := []replayrec.Row{
		{ID: "r0", Timestamp: t0, SessionID: strp("s1"), Status: "success", Provider: "openai", Model: "gpt-4o",
			RawRequest: reqBody([]msg{sys, u1}, true), RawResponse: toolAnswer("c1", "lookup", `{"kind":"order","id":"7"}`)},
		{ID: "r1", Timestamp: t0.Add(time.Second), SessionID: strp("s1"), Status: "success", Provider: "openai", Model: "gpt-4o",
			RawRequest: reqBody([]msg{sys, u1, a1, r1}, true), RawResponse: toolAnswer("c2", "lookup", `{"kind":"invoice","id":"7"}`)},
		{ID: "r2", Timestamp: t0.Add(2 * time.Second), SessionID: strp("s1"), Status: "success", Provider: "openai", Model: "gpt-4o",
			RawRequest: reqBody([]msg{sys, u1, a1, r1, a2, r2}, true), RawResponse: chat("Order 7 shipped and is paid.")},
		{ID: "r3", Timestamp: t0.Add(3 * time.Second), SessionID: strp("s1"), Status: "success", Provider: "openai", Model: "gpt-4o",
			RawRequest: reqBody([]msg{sys, u1, a1, r1, a2, r2, fin, u2}, true), RawResponse: chat("It arrives Friday.")},
	}
	rec, err := replayrec.Build("prod", rows)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func fake(replies ...string) *investigator.FakeModel {
	return &investigator.FakeModel{Replies: replies, CostUSD: 0.01, Cap: 10}
}

var opt = Options{Candidate: "openai/gpt-4o-mini", MaxSteps: 30}

func TestFixedSkipsRedacted(t *testing.T) {
	rec, err := replayrec.Build("prod", []replayrec.Row{{ID: "r0", Timestamp: t0, SessionID: strp("s1"),
		RawRequest: reqBody([]msg{{"role": "user", "content": "mail me at jane.doe@example.com"}}, false), RawResponse: chat("ok")}})
	if err != nil {
		t.Fatal(err)
	}
	m := fake(chat("ok"))
	res, stop := Fixed(context.Background(), m, nil, rec, opt)
	if len(m.Seen) != 0 {
		t.Fatalf("a redacted request was sent: %d calls", len(m.Seen))
	}
	if res.Steps[0].State != StateRedacted || res.Steps[0].CostUSD != 0 || stop != "" {
		t.Fatalf("step = %+v, stop %q", res.Steps[0], stop)
	}
}

func TestFixedInsufficientWithoutToolDefs(t *testing.T) {
	hist := []json.RawMessage{json.RawMessage(`{"role":"user","content":"Find order 7."}`)}
	out := json.RawMessage(`{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}`)
	rec, err := replayrec.Build("prod", []replayrec.Row{{ID: "r0", Timestamp: t0, SessionID: strp("s1"), InputHistory: hist, OutputMessage: out}})
	if err != nil {
		t.Fatal(err)
	}
	m := fake(chat("x"))
	res, _ := Fixed(context.Background(), m, nil, rec, opt)
	if len(m.Seen) != 0 || res.Steps[0].State != StateInsufficient {
		t.Fatalf("step = %+v, sent %d", res.Steps[0], len(m.Seen))
	}
	if !strings.Contains(res.Steps[0].New, "no tool definitions") {
		t.Fatalf("no reason given: %+v", res.Steps[0])
	}
}

func TestFixedSameToolCallsEquivalent(t *testing.T) {
	rec := session(t)
	m := fake(toolAnswer("x1", "lookup", `{ "id": "7", "kind": "order" }`), toolAnswer("x2", "lookup", `{"kind":"invoice","id":"7"}`),
		chat("Order 7 shipped and is  paid."), chat("It arrives Friday."))
	res, stop := Fixed(context.Background(), m, nil, rec, opt)
	if stop != "" {
		t.Fatal(stop)
	}
	for i, s := range res.Steps {
		if s.State != StateEquivalent || s.Basis != "exact" {
			t.Fatalf("step %d = %+v", i, s)
		}
	}
	if len(m.Seen) != 4 {
		t.Fatalf("calls = %d", len(m.Seen))
	}
	// Every request is the recorded one, with the candidate model and no stream.
	var body map[string]any
	if err := json.Unmarshal(m.Seen[1].Raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "openai/gpt-4o-mini" || body["stream"] != false || len(body["messages"].([]any)) != 4 {
		t.Fatalf("request 1 = %s", m.Seen[1].Raw)
	}
	if res.ToolAgreement == nil || *res.ToolAgreement != 1 || res.Final != StateEquivalent || res.DivergedAt != nil {
		t.Fatalf("session = %+v", res)
	}
}

func TestFixedDifferentArgs(t *testing.T) {
	rec := session(t)
	m := fake(toolAnswer("x1", "lookup", `{"id":"8","kind":"order"}`), toolAnswer("x2", "search", `{}`), chat("Something else."), chat("It arrives Friday."))
	res, _ := Fixed(context.Background(), m, nil, rec, opt)
	if res.Steps[0].State != StateDifferentTool || res.Steps[1].State != StateDifferentTool || res.Steps[2].State != StateDifferentText {
		t.Fatalf("steps = %+v", res.Steps)
	}
	if res.DivergedAt == nil || *res.DivergedAt != 0 {
		t.Fatalf("diverged at %v", res.DivergedAt)
	}
	// The model's answer never feeds the next step: step 1 sends recorded request 1.
	if !strings.Contains(string(m.Seen[1].Raw), "order 7: shipped") {
		t.Fatalf("step 1 did not send the recorded request: %s", m.Seen[1].Raw)
	}
}

func TestFixedJudgeBasis(t *testing.T) {
	rec, err := replayrec.Build("prod", []replayrec.Row{{ID: "r0", Timestamp: t0, SessionID: strp("s1"),
		RawRequest: reqBody([]msg{{"role": "user", "content": "Is order 7 shipped?"}}, false), RawResponse: chat("Yes, it shipped.")}})
	if err != nil {
		t.Fatal(err)
	}
	m := fake(chat("It has been shipped."), `{"equivalent": true, "reason": "Both say it shipped."}`)
	res, _ := Fixed(context.Background(), m, m, rec, opt)
	s := res.Steps[0]
	if s.State != StateEquivalent || s.Basis != "judge" || s.JudgeNote != "Both say it shipped." {
		t.Fatalf("step = %+v", s)
	}
	if m.Seen[1].Purpose != "replay_judge" || !strings.Contains(m.Seen[1].Messages[0].Content, "Two assistants answered") {
		t.Fatalf("judge request = %+v", m.Seen[1])
	}
}

func TestTrajectoryStopsOnMiss(t *testing.T) {
	rec := session(t)
	m := fake(toolAnswer("x1", "lookup", `{"kind":"order","id":"7"}`), toolAnswer("x2", "lookup", `{"kind":"invoice","id":"7"}`),
		toolAnswer("x3", "refund_order", `{"id":"7"}`), chat("never used"))
	res, _ := Trajectory(context.Background(), m, nil, rec, opt)
	if res.Final != StateMissingTool || res.DivergedAt == nil || *res.DivergedAt != 2 {
		t.Fatalf("session = %+v", res)
	}
	if len(m.Seen) != 3 || len(m.Replies) != 1 {
		t.Fatalf("calls = %d, replies left %d", len(m.Seen), len(m.Replies))
	}
	last := res.Steps[len(res.Steps)-1]
	if !strings.Contains(last.New, "Stopped at step 3: openai/gpt-4o-mini called refund_order with arguments the recording has no answer for.") {
		t.Fatalf("last step = %+v", last)
	}
	// The tool answers came from the recording.
	if !strings.Contains(string(m.Seen[2].Raw), "invoice 7: paid") || !strings.Contains(string(m.Seen[1].Raw), "order 7: shipped") {
		t.Fatalf("recorded tool results not sent: %s", m.Seen[2].Raw)
	}
	if res.WriteSetEqual == nil || *res.WriteSetEqual {
		t.Fatalf("write set = %v", res.WriteSetEqual)
	}
}

func TestTrajectoryInjectsLaterUserMessage(t *testing.T) {
	rec := session(t)
	m := fake(toolAnswer("x1", "lookup", `{"kind":"order","id":"7"}`), toolAnswer("x2", "lookup", `{"kind":"invoice","id":"7"}`),
		chat("Order 7 shipped and is paid."), chat("It arrives Friday."))
	res, _ := Trajectory(context.Background(), m, nil, rec, opt)
	if len(m.Seen) != 4 {
		t.Fatalf("calls = %d", len(m.Seen))
	}
	if !strings.Contains(string(m.Seen[3].Raw), "Thanks, when does it arrive?") {
		t.Fatalf("later user message not sent: %s", m.Seen[3].Raw)
	}
	if strings.Contains(string(m.Seen[0].Raw), "Thanks, when") {
		t.Fatal("the later user message was sent too early")
	}
	if res.Final != StateEquivalent || res.DivergedAt != nil {
		t.Fatalf("session = %+v", res)
	}
	if res.WriteSetEqual == nil || !*res.WriteSetEqual {
		t.Fatalf("write set = %v", res.WriteSetEqual)
	}
}

func TestTrajectoryStepLimit(t *testing.T) {
	rec := session(t)
	m := fake(toolAnswer("x1", "lookup", `{"kind":"order","id":"7"}`), toolAnswer("x2", "lookup", `{"kind":"invoice","id":"7"}`), chat("x"), chat("y"))
	o := opt
	o.MaxSteps = 2
	res, _ := Trajectory(context.Background(), m, nil, rec, o)
	if res.Final != StateInsufficient || len(m.Seen) != 2 {
		t.Fatalf("session = %+v, calls %d", res, len(m.Seen))
	}
}

func TestTrajectoryNeverSendsRedactedText(t *testing.T) {
	rec := session(t)
	// The recorded tool result was cleaned of an email: the model must not get it.
	for i := range rec.Tools {
		if rec.Tools[i].CallID == "c1" {
			rec.Tools[i].Content = "order 7 for [REDACTED email]"
		}
	}
	m := fake(toolAnswer("x1", "lookup", `{"kind":"order","id":"7"}`), chat("never"))
	res, _ := Trajectory(context.Background(), m, nil, rec, opt)
	if len(m.Seen) != 1 || res.Final != StateRedacted {
		t.Fatalf("session = %+v, calls %d", res, len(m.Seen))
	}
}

func TestReplayBudgetStopsMidSession(t *testing.T) {
	rec := session(t)
	m := &investigator.FakeModel{Replies: []string{toolAnswer("x1", "lookup", `{"kind":"order","id":"7"}`), chat("a"), chat("b"), chat("c")}, CostUSD: 1, Cap: 1.5}
	rep := Run(context.Background(), "job1", ModeFixed, []*replayrec.Recording{rec, rec}, m, nil, opt)
	if rep.Stopped != "budget" {
		t.Fatalf("stopped = %q", rep.Stopped)
	}
	s := rep.Sessions[0]
	if s.Steps[1].State != StateInfraFailure || len(rep.Sessions) != 1 {
		t.Fatalf("sessions = %+v", rep.Sessions)
	}
	if rep.Note != Note || rep.Counts[StateInfraFailure] != 1 || rep.SpentUSD != 1 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestWriteSetEqual(t *testing.T) {
	a := []replayrec.ToolCall{{Name: "send_email", Arguments: `{"to":"a"}`, Write: true}, {Name: "lookup", Arguments: `{}`}}
	b := []replayrec.ToolCall{{Name: "send_email", Arguments: `{"to":"a"}`, Write: true}}
	c := []replayrec.ToolCall{{Name: "send_email", Arguments: `{"to":"b"}`, Write: true}}
	if !writeSetEqual(a, b) || writeSetEqual(a, c) || writeSetEqual(b, nil) || !writeSetEqual(nil, nil) {
		t.Fatal("write set comparison is wrong")
	}
}

func TestWilson(t *testing.T) {
	lo, hi := Wilson(8, 10)
	if lo < 0.49 || lo > 0.50 || hi < 0.94 || hi > 0.95 {
		t.Fatalf("wilson(8,10) = %v %v", lo, hi)
	}
}
