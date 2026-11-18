// Package a2a turns Bifrost's agent log into the witness of an a2a run (B1,
// docs/a2a-agents.md): which operations the gateway saw, which A2A tasks
// they belonged to, who called whom, which LLM and MCP rows carried a task's
// correlation id, and whether the launcher's result agrees with the
// gateway's record of it. Everything here is pure; the engine fetches the
// rows (engine/a2a.go) and the metrics read the result (metrics/a2a.go).
package a2a

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// CallerBench is the caller of an operation the bench's launcher sent.
const CallerBench = "bench"

// Lead is the member id of the entry agent.
const Lead = "lead"

// StrengthWitnessed and StrengthPartial label the evidence.
const (
	StrengthWitnessed = "witnessed"
	StrengthPartial   = "partial"
	StrengthNone      = "unwitnessed"
)

// Input is what Build needs.
type Input struct {
	Run       *schemas.A2ARun
	RunKeyID  string
	Launcher  *schemas.A2ALauncherReport
	Rows      []schemas.A2ALogRow
	Linked    []schemas.A2ALinkedRow
	FetchErrs []string
	Fault     *schemas.A2AFault
}

// Build is the run's A2A evidence. Operations are the agent log's request
// rows of the run's registrations; the caller of each comes from the key it
// came in on: the run key on the entry registration is the launcher, the
// run key on another registration is the entry agent (the only other holder
// of the run key, through credential forwarding), and a member key is that
// member (the only holder: Bifrost sends it to that agent alone).
func Build(in Input) *schemas.A2AEvidence {
	ev := &schemas.A2AEvidence{Strength: StrengthNone, TaskStates: map[string]string{}}
	if in.Run == nil {
		ev.Reason = "no agent gateway registration was made for the run"
		return ev
	}
	ev.Entry = in.Run.Entry
	ev.Team = in.Run.Members
	ev.Launcher = in.Launcher
	ev.Problems = append(append(ev.Problems, in.Run.Problems...), in.FetchErrs...)
	owned := map[string]bool{}
	for _, n := range in.Run.Owned {
		owned[n] = true
	}
	names := make([]string, 0, len(in.Run.Members))
	for n := range in.Run.Members {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		ev.Registrations = append(ev.Registrations, schemas.A2ARegistration{Name: n, Member: in.Run.Members[n], CardURL: in.Run.CardURLs[n], Owned: owned[n]})
	}
	rows := append([]schemas.A2ALogRow(nil), in.Rows...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Timestamp.Before(rows[j].Timestamp) })
	states := map[string]string{}
	// Only the run's own traffic counts: a request row on one of the run's
	// keys, and the stream events of the tasks those rows carry. Another
	// caller of a shared registration (a2a.registration) is not the run's.
	runTasks := map[string]bool{}
	for _, r := range rows {
		if r.RecordKind == "request" && r.TaskID != "" && runKey(in, r.VirtualKeyID) {
			runTasks[r.TaskID] = true
		}
	}
	for _, r := range rows {
		if _, ok := in.Run.Members[r.AgentName]; !ok {
			continue
		}
		if r.RecordKind == "request" && !runKey(in, r.VirtualKeyID) {
			ev.Foreign++
			continue
		}
		if r.RecordKind != "request" && !runTasks[r.TaskID] {
			continue
		}
		if r.RecordKind != "request" {
			ev.StreamEvents++
			if r.TaskID != "" {
				setState(states, r.TaskID, r.TaskState)
			}
			continue
		}
		op := schemas.A2AOperation{
			ID: r.ID, RequestID: r.RequestID, Timestamp: r.Timestamp, Operation: r.Operation, Status: r.Status,
			Agent: r.AgentName, Member: in.Run.Members[r.AgentName], VirtualKeyID: r.VirtualKeyID,
			TaskID: r.TaskID, ContextID: r.ContextID, DownstreamTransport: r.DownstreamTransport, UpstreamTransport: r.UpstreamTransport,
			LatencyMS: r.Latency, UpstreamLatencyMS: r.UpstreamLatency, OverheadLatencyMS: r.OverheadLatency, Error: r.Error,
		}
		op.Caller = callerOf(in, r)
		if st, ans := responseTask(r.ResponseBody); st != "" {
			op.TaskState = st
			op.AnswerSHA256 = ans
			if r.TaskID != "" {
				setState(states, r.TaskID, st)
			}
		}
		ev.Operations = append(ev.Operations, op)
	}
	for _, op := range ev.Operations {
		if op.TaskID != "" {
			if st := states[op.TaskID]; st != "" {
				ev.TaskStates[op.TaskID] = st
			}
		}
		if op.Member == "" || op.Caller == "" || op.Caller == CallerBench || op.Caller == op.Member {
			continue
		}
		if op.Operation == "SendMessage" || op.Operation == "SendStreamingMessage" {
			ev.Hops = append(ev.Hops, schemas.A2AHop{From: op.Caller, To: op.Member, OperationID: op.ID, TaskID: op.TaskID, Source: "gateway_key"})
		}
	}
	if len(ev.TaskStates) == 0 {
		ev.TaskStates = nil
	}
	for _, l := range in.Linked {
		l.Member = in.Run.KeyMember[l.VirtualKeyID]
		l.OnRunKey = l.Member != "" || (l.VirtualKeyID != "" && l.VirtualKeyID == in.RunKeyID)
		if l.Member == "" && l.OnRunKey {
			l.Member = Lead
		}
		ev.Linked = append(ev.Linked, l)
	}
	ev.Unmatched = unmatched(in.Launcher, ev.Operations, in.Run.Entry)
	ev.Agreement = agreement(in.Launcher, ev)
	ev.Fault = faultEvidence(in.Fault, in.Launcher, ev)
	switch {
	case in.Launcher == nil:
		ev.Reason = "the launcher printed no a2a_report"
	case len(ev.Unmatched) > 0:
		ev.Reason = fmt.Sprintf("%d launcher operation(s) are not in the agent log", len(ev.Unmatched))
	case len(in.FetchErrs) > 0:
		ev.Reason = "the agent log could not be read in full: " + strings.Join(in.FetchErrs, "; ")
	default:
		ev.Complete = true
	}
	switch {
	case ev.Complete:
		ev.Strength = StrengthWitnessed
	case len(ev.Operations) > 0:
		ev.Strength = StrengthPartial
	}
	return ev
}

// setState records a task's state. A terminal state is never replaced by a
// non-terminal one: a request row is timestamped when the operation starts
// but holds the response at its end, so its stream events sort after it.
func setState(states map[string]string, task, state string) {
	if state == "" || (terminal(states[task]) && !terminal(state)) {
		return
	}
	states[task] = state
}

func terminal(state string) bool {
	switch state {
	case "TASK_STATE_COMPLETED", "TASK_STATE_FAILED", "TASK_STATE_CANCELED", "TASK_STATE_REJECTED", "message":
		return true
	}
	return false
}

// runKey reports whether a key is one the run minted (the run key or a
// member key).
func runKey(in Input, id string) bool {
	if id == "" {
		return false
	}
	if id == in.RunKeyID {
		return true
	}
	_, ok := in.Run.KeyMember[id]
	return ok
}

// callerOf names who sent a request row, from its key.
func callerOf(in Input, r schemas.A2ALogRow) string {
	if r.VirtualKeyID == "" {
		return ""
	}
	if r.VirtualKeyID == in.RunKeyID {
		if r.AgentName == in.Run.Entry {
			return CallerBench
		}
		return Lead
	}
	return in.Run.KeyMember[r.VirtualKeyID]
}

// unmatched lists the launcher's operations with no request row of the
// same operation (and task, when the launcher knew it) on the entry
// registration. Each row matches one operation.
func unmatched(rep *schemas.A2ALauncherReport, ops []schemas.A2AOperation, entry string) []schemas.A2ALaunchOperation {
	if rep == nil {
		return nil
	}
	used := map[int]bool{}
	var out []schemas.A2ALaunchOperation
	for _, lo := range rep.Operations {
		found := false
		for i, op := range ops {
			if used[i] || op.Agent != entry || op.Caller != CallerBench || op.Operation != lo.Method {
				continue
			}
			if lo.TaskID != "" && op.TaskID != "" && lo.TaskID != op.TaskID {
				continue
			}
			used[i], found = true, true
			break
		}
		if !found {
			out = append(out, lo)
		}
	}
	return out
}

// agreement compares the launcher's final task with the gateway's record.
func agreement(rep *schemas.A2ALauncherReport, ev *schemas.A2AEvidence) *schemas.A2AAgreement {
	if rep == nil || rep.TaskID == "" {
		return nil
	}
	a := &schemas.A2AAgreement{TaskID: rep.TaskID, LauncherState: rep.FinalState, GatewayState: ev.TaskStates[rep.TaskID]}
	for i := len(ev.Operations) - 1; i >= 0; i-- {
		op := ev.Operations[i]
		if op.TaskID != rep.TaskID || op.AnswerSHA256 == "" || op.Caller != CallerBench {
			continue
		}
		match := op.AnswerSHA256 == rep.AnswerSHA256
		a.AnswerMatches = &match
		break
	}
	switch {
	case a.GatewayState == "":
		a.Note = "the agent log holds no final state for the task"
	case a.GatewayState != a.LauncherState:
		a.Note = "the launcher and the agent log disagree on the final state"
	case a.AnswerMatches == nil:
		a.Note = "the agent log holds no recorded answer for the task (a streamed result is recorded as events)"
	case !*a.AnswerMatches:
		a.Note = "the graded answer differs from the answer the agent log recorded"
	}
	return a
}

// faultEvidence says whether the a2a probe's fault fired (launcher) and
// whether the task was recovered through the gateway: a GetTask or
// SubscribeToTask row of the same task from the bench after the stream, and
// a completed final state in the agent log.
func faultEvidence(f *schemas.A2AFault, rep *schemas.A2ALauncherReport, ev *schemas.A2AEvidence) *schemas.A2AFaultEvidence {
	if f == nil {
		return nil
	}
	out := &schemas.A2AFaultEvidence{Kind: "stream_" + f.Stream}
	if rep == nil || rep.Fault == nil || !rep.Fault.Fired {
		out.Detail = "the launcher did not report the fault as fired"
		return out
	}
	out.Observed = true
	var recovery []string
	for _, op := range ev.Operations {
		if op.Caller == CallerBench && op.TaskID == rep.TaskID && (op.Operation == "GetTask" || op.Operation == "SubscribeToTask") {
			recovery = append(recovery, op.Operation)
		}
	}
	state := ev.TaskStates[rep.TaskID]
	out.Recovered = state == "TASK_STATE_COMPLETED" && (f.Stream != "drop" || len(recovery) > 0)
	switch {
	case f.Stream == "drop" && len(recovery) == 0:
		out.Detail = "no GetTask or SubscribeToTask of the task in the agent log after the dropped stream"
	case state == "":
		out.Detail = "the agent log holds no final state for the task"
	case len(recovery) == 0:
		out.Detail = "the stream ended in the task's final state after the stall; final state in the agent log: " + state
	default:
		out.Detail = fmt.Sprintf("recovery through the gateway: %s; final state in the agent log: %s", strings.Join(slices.Compact(recovery), ", "), state)
	}
	return out
}

// responseTask reads the final state and the answer hash of a recorded
// response body: a task, a SendMessage result wrapping a task or a message,
// or a bare message. The answer is computed as the launcher computes it
// (text parts of the artifacts that are not files, else the status message).
func responseTask(body string) (string, string) {
	if strings.TrimSpace(body) == "" {
		return "", ""
	}
	var doc struct {
		Task    *taskDoc `json:"task"`
		Message *msgDoc  `json:"message"`
		taskDoc
		Parts []partDoc `json:"parts"`
		Role  string    `json:"role"`
	}
	if json.Unmarshal([]byte(body), &doc) != nil {
		return "", ""
	}
	switch {
	case doc.Task != nil:
		return doc.Task.Status.State, hashText(doc.Task.answer())
	case doc.Message != nil:
		return "message", hashText(doc.Message.text())
	case doc.Status.State != "":
		return doc.Status.State, hashText(doc.taskDoc.answer())
	case doc.Role != "":
		return "message", hashText((&msgDoc{Parts: doc.Parts}).text())
	}
	return "", ""
}

type partDoc struct {
	Text     *string `json:"text"`
	Filename string  `json:"filename"`
}

type msgDoc struct {
	Parts []partDoc `json:"parts"`
}

func (m *msgDoc) text() string {
	if m == nil {
		return ""
	}
	var out []string
	for _, p := range m.Parts {
		if p.Text != nil {
			out = append(out, *p.Text)
		}
	}
	return strings.Join(out, "\n")
}

type taskDoc struct {
	Status struct {
		State   string  `json:"state"`
		Message *msgDoc `json:"message"`
	} `json:"status"`
	Artifacts []struct {
		Parts []partDoc `json:"parts"`
	} `json:"artifacts"`
}

func (t *taskDoc) answer() string {
	var out []string
	for _, a := range t.Artifacts {
		for _, p := range a.Parts {
			if p.Text != nil && p.Filename == "" {
				out = append(out, *p.Text)
			}
		}
	}
	if len(out) > 0 {
		return strings.Join(out, "\n")
	}
	return t.Status.Message.text()
}

func hashText(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ReadLauncherReport reads the last a2a_report line of the launcher's
// stdout (sessions append theirs; the last is the run's final task).
func ReadLauncherReport(path string) *schemas.A2ALauncherReport {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out *schemas.A2ALauncherReport
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), `"a2a_report"`) {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &probe) != nil || probe.Type != "a2a_report" {
			continue
		}
		var rep schemas.A2ALauncherReport
		if json.Unmarshal(line, &rep) == nil {
			out = &rep
		}
	}
	return out
}

// Messages turns the hops into team messages (Evidence.Messages), with the
// text of the request the gateway recorded, so the multi-agent metrics
// count agent-to-agent traffic through the gateway like mailbox messages.
func Messages(ev *schemas.A2AEvidence, rows []schemas.A2ALogRow) []schemas.AgentMessage {
	if ev == nil || len(ev.Hops) == 0 {
		return nil
	}
	body := map[string]string{}
	at := map[string]time.Time{}
	for _, r := range rows {
		body[r.ID] = requestText(r.RequestBody)
		at[r.ID] = r.Timestamp
	}
	var out []schemas.AgentMessage
	for i, h := range ev.Hops {
		b := body[h.OperationID]
		sum := sha256.Sum256([]byte(b))
		out = append(out, schemas.AgentMessage{
			Seq: i + 1, ID: h.OperationID, From: h.From, To: h.To, Body: b, BodySHA256: hex.EncodeToString(sum[:]), Size: int64(len(b)),
			Fields: map[string]string{"source": "agent_gateway", "task_id": h.TaskID}, SenderAgentID: h.From, At: at[h.OperationID], Delivered: true, DeliveredTo: []string{h.To},
		})
	}
	return out
}

// requestText is the text of the message in a recorded request body.
func requestText(body string) string {
	var doc struct {
		Message *msgDoc `json:"message"`
	}
	if json.Unmarshal([]byte(body), &doc) != nil || doc.Message == nil {
		return ""
	}
	return doc.Message.text()
}
