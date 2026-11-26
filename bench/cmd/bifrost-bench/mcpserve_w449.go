package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// W4-49 D: MCP investigation tools. Each one is a fixed function over the
// API that answers small and exact: a page of steps, one step, the calls of
// a run a few at a time, retries per turn, two runs' commands lined up, the
// cost of an experiment per arm or condition, and the Inbox's recent
// failures. Every answer is cut at 64 KB.

const (
	mcpInvestigateCap  = 64 << 10
	mcpSummaryCap      = 300
	mcpCallTextCap     = 4 << 10
	mcpPreviewCap      = 8 << 10
	mcpAgentsCap       = 200
	mcpRetryTurnsCap   = 200
	mcpCompareDefault  = 100
	mcpCompareMax      = 300
	mcpConversationMax = 20
	mcpNotKept         = "The gateway kept no text for this call."
)

func mcpInt(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func init() {
	add := func(name string, t mcpTool) {
		inner := t.run
		t.readOnly = true
		t.run = func(ctx context.Context, s *mcpServer, args json.RawMessage) (any, error) {
			v, err := inner(ctx, s, args)
			if err != nil {
				return nil, err
			}
			return mcpCut64K(v)
		}
		mcpToolTable[name] = t
	}
	add("list_steps", mcpTool{
		description: "List the recorded steps of a run, a page at a time. Use after_seq from the last answer to get the next page.",
		schema: obj(map[string]any{"run_id": str("Run id"), "after_seq": mcpInt("Return steps after this seq (default 0)"),
			"limit": mcpInt("Steps per page, default 50, at most 200"), "kind": str("Only steps of this kind"),
			"agent_id": str("Only steps of this agent"), "parent_seq": mcpInt("Only steps nested under this seq")}, "run_id"),
		run: mcpListSteps,
	})
	add("get_step", mcpTool{
		description: "One step of a run with the steps that witnessed it and its child steps.",
		schema:      obj(map[string]any{"run_id": str("Run id"), "seq": mcpInt("Step seq")}, "run_id", "seq"),
		run: func(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
			var a struct {
				RunID string `json:"run_id"`
				Seq   *int   `json:"seq"`
			}
			if err := json.Unmarshal(raw, &a); err != nil || a.RunID == "" || a.Seq == nil {
				return nil, errors.New("run_id and seq are required")
			}
			var out map[string]any
			return out, s.api.getJSON(ctx, "/runs/"+url.PathEscape(a.RunID)+"/step/"+strconv.Itoa(*a.Seq), nil, &out)
		},
	})
	add("list_agents_in_run", mcpTool{
		description: "The agents of a multi-agent run and how they were nested.",
		schema:      obj(map[string]any{"run_id": str("Run id")}, "run_id"),
		run: func(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
			runID, err := mcpRunID(raw)
			if err != nil {
				return nil, err
			}
			var out map[string]any
			if err := s.api.getJSON(ctx, "/runs/"+url.PathEscape(runID)+"/agents", nil, &out); err != nil {
				return nil, err
			}
			if l, ok := out["agents"].([]any); ok && len(l) > mcpAgentsCap {
				out["agents"], out["agents_cut"] = l[:mcpAgentsCap], fmt.Sprintf("%d of %d agents shown", mcpAgentsCap, len(l))
			}
			return out, nil
		},
	})
	add("get_conversation", mcpTool{
		description: "The model calls of a run in order, with the last message sent and the answer, a few calls at a time.",
		schema: obj(map[string]any{"run_id": str("Run id"), "after_call": mcpInt("Return calls after this call number (default 0)"),
			"limit": mcpInt("Calls per answer, default 5, at most 20")}, "run_id"),
		run: mcpGetConversation,
	})
	add("get_retries", mcpTool{
		description: "How many times each model call of a run was retried by the gateway.",
		schema:      obj(map[string]any{"run_id": str("Run id")}, "run_id"),
		run: func(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
			runID, err := mcpRunID(raw)
			if err != nil {
				return nil, err
			}
			steps, err := mcpAllSteps(ctx, s, runID)
			if err != nil {
				return nil, err
			}
			return mcpRetries(steps), nil
		},
	})
	add("compare_commands", mcpTool{
		description: "Line up the shell commands two runs ran and show the first place they differ.",
		schema: obj(map[string]any{"run_a": str("First run id"), "run_b": str("Second run id"),
			"limit": mcpInt("Rows shown, default 100, at most 300")}, "run_a", "run_b"),
		run: func(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
			var a struct {
				RunA  string `json:"run_a"`
				RunB  string `json:"run_b"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(raw, &a); err != nil || a.RunA == "" || a.RunB == "" {
				return nil, errors.New("run_a and run_b are required")
			}
			sa, err := mcpAllSteps(ctx, s, a.RunA)
			if err != nil {
				return nil, err
			}
			sb, err := mcpAllSteps(ctx, s, a.RunB)
			if err != nil {
				return nil, err
			}
			return mcpCompareCommands(sa, sb, mcpClamp(a.Limit, mcpCompareDefault, mcpCompareMax)), nil
		},
	})
	add("cost_per_task", mcpTool{
		description: "Cost of an experiment per task, arm or condition, most expensive first.",
		schema: obj(map[string]any{"exp_id": str("Experiment id"),
			"group_by": map[string]any{"type": "string", "enum": []string{"task", "arm", "condition"}, "description": "Group rows by task (default), arm or condition"},
			"top":      mcpInt("Rows shown, default 20, at most 100")}, "exp_id"),
		run: mcpCostPerTask,
	})
	add("todays_failures", mcpTool{
		description: "Production failures that reached the Inbox recently.",
		schema:      obj(map[string]any{"since_hours": mcpInt("Window in hours, default 24, at most 168"), "limit": mcpInt("Items shown, default 20, at most 100")}),
		run:         mcpTodaysFailures,
	})
	add("get_inbox_item", mcpTool{
		description: "One Inbox item with its cleaned session preview.",
		schema:      obj(map[string]any{"id": str("Inbox item id")}, "id"),
		run: func(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
			var a struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &a); err != nil || a.ID == "" {
				return nil, errors.New("id is required")
			}
			var out schemas.InboxItemDetail
			if err := s.api.getJSON(ctx, "/inbox/"+url.PathEscape(a.ID), nil, &out); err != nil {
				return nil, err
			}
			out.Preview = mcpClip(out.Preview, mcpPreviewCap)
			return out, nil
		},
	})
}

// mcpCut64K encodes v and cuts it at 64 KB with a line saying so.
func mcpCut64K(v any) (any, error) {
	var text string
	if s, ok := v.(string); ok {
		text = s
	} else {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return nil, err
		}
		text = string(b)
	}
	if len(text) <= mcpInvestigateCap {
		return text, nil
	}
	return text[:mcpInvestigateCap] + fmt.Sprintf("\ncut: %d of %d bytes; ask for a smaller page", mcpInvestigateCap, len(text)), nil
}

func mcpClamp(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func mcpClip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func mcpRunID(raw json.RawMessage) (string, error) {
	var a struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.RunID == "" {
		return "", errors.New("run_id is required")
	}
	return a.RunID, nil
}

func mcpListSteps(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
	var a struct {
		RunID     string `json:"run_id"`
		AfterSeq  int    `json:"after_seq"`
		Limit     int    `json:"limit"`
		Kind      string `json:"kind"`
		AgentID   string `json:"agent_id"`
		ParentSeq *int   `json:"parent_seq"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.RunID == "" {
		return nil, errors.New("run_id is required")
	}
	q := url.Values{"after_seq": {strconv.Itoa(max(a.AfterSeq, 0))}, "limit": {strconv.Itoa(mcpClamp(a.Limit, handlers.StepsPageDefault, handlers.StepsPageMax))}}
	if a.Kind != "" {
		q.Set("kind", a.Kind)
	}
	if a.AgentID != "" {
		q.Set("agent_id", a.AgentID)
	}
	if a.ParentSeq != nil {
		q.Set("parent_seq", strconv.Itoa(*a.ParentSeq))
	}
	var page handlers.RunStepsPage
	if err := s.api.getJSON(ctx, "/runs/"+url.PathEscape(a.RunID)+"/steps/page", q, &page); err != nil {
		return nil, err
	}
	for i := range page.Steps {
		page.Steps[i].Summary = mcpClip(page.Steps[i].Summary, mcpSummaryCap)
	}
	return page, nil
}

// mcpAllSteps reads a run's whole timeline through the step route.
func mcpAllSteps(ctx context.Context, s *mcpServer, runID string) ([]schemas.StepRecord, error) {
	var out handlers.RunSteps
	if err := s.api.getJSON(ctx, "/runs/"+url.PathEscape(runID)+"/steps", nil, &out); err != nil {
		return nil, err
	}
	return out.Steps, nil
}

// mcpRetryTurn is one turn of get_retries.
type mcpRetryTurn struct {
	Turn     int `json:"turn"`
	Attempts int `json:"attempts"`
}

func mcpRetries(steps []schemas.StepRecord) map[string]any {
	turns := map[int]int{}
	for _, st := range steps {
		switch st.Kind {
		case schemas.StepModelTurn:
			if _, ok := turns[st.Turn]; !ok {
				turns[st.Turn] = 0
			}
		case schemas.StepModelAttempt:
			turns[st.Turn]++
		}
	}
	keys := make([]int, 0, len(turns))
	for k := range turns {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	list := []mcpRetryTurn{}
	total, withRetry := 0, 0
	for _, k := range keys {
		total += turns[k]
		if turns[k] > 0 {
			withRetry++
			if len(list) < mcpRetryTurnsCap {
				list = append(list, mcpRetryTurn{Turn: k, Attempts: turns[k]})
			}
		}
	}
	return map[string]any{"turns": list, "total_retries": total, "total_turns": len(keys), "turns_with_retry": withRetry}
}

var (
	mcpSpaces = regexp.MustCompile(`\s+`)
	mcpDigits = regexp.MustCompile(`[0-9]+`)
)

// mcpShellTools are the tool names whose tool_request rows are shell
// commands.
var mcpShellTools = map[string]bool{"bash": true, "shell": true, "sh": true, "run_command": true, "execute_command": true, "terminal": true, "exec": true}

func mcpNormalizeCommand(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = mcpSpaces.ReplaceAllString(s, " ")
	return mcpDigits.ReplaceAllString(s, "#")
}

type mcpCommand struct {
	seq  int
	text string
}

func mcpCommands(steps []schemas.StepRecord) []mcpCommand {
	var out []mcpCommand
	for _, st := range steps {
		if st.Kind == schemas.StepCommand || (st.Kind == schemas.StepToolRequest && mcpShellTools[strings.ToLower(st.ToolName)]) {
			out = append(out, mcpCommand{seq: st.Seq, text: mcpNormalizeCommand(st.Summary)})
		}
	}
	return out
}

// mcpCompareRow is one aligned row of compare_commands.
type mcpCompareRow struct {
	ASeq    *int   `json:"a_seq"`
	BSeq    *int   `json:"b_seq"`
	Command string `json:"command"`
	Side    string `json:"side"` // both | only_a | only_b
}

// mcpCompareCommands aligns two runs' commands by longest common
// subsequence.
func mcpCompareCommands(sa, sb []schemas.StepRecord, limit int) map[string]any {
	a, b := mcpCommands(sa), mcpCommands(sb)
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i].text == b[j].text {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	rows := []mcpCompareRow{}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i].text == b[j].text:
			as, bs := a[i].seq, b[j].seq
			rows = append(rows, mcpCompareRow{ASeq: &as, BSeq: &bs, Command: a[i].text, Side: "both"})
			i, j = i+1, j+1
		case j >= m || (i < n && lcs[i+1][j] >= lcs[i][j+1]):
			as := a[i].seq
			rows = append(rows, mcpCompareRow{ASeq: &as, Command: a[i].text, Side: "only_a"})
			i++
		default:
			bs := b[j].seq
			rows = append(rows, mcpCompareRow{BSeq: &bs, Command: b[j].text, Side: "only_b"})
			j++
		}
	}
	var first *int
	for k, r := range rows {
		if r.Side != "both" {
			first = &k
			break
		}
	}
	out := map[string]any{"rows": rows, "first_difference": first, "total_rows": len(rows)}
	if len(rows) > limit {
		out["rows"] = rows[:limit]
	}
	return out
}

// mcpConversationCall is one call of get_conversation.
type mcpConversationCall struct {
	Call        int    `json:"call"`
	RequestID   string `json:"request_id,omitempty"`
	Model       string `json:"model,omitempty"`
	Status      int    `json:"status"`
	Kept        bool   `json:"kept"`
	Note        string `json:"note,omitempty"`
	LastMessage string `json:"last_message,omitempty"`
	Response    string `json:"response,omitempty"`
}

func mcpGetConversation(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
	var a struct {
		RunID     string `json:"run_id"`
		AfterCall int    `json:"after_call"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.RunID == "" {
		return nil, errors.New("run_id is required")
	}
	limit := mcpClamp(a.Limit, 5, mcpConversationMax)
	base := "/runs/" + url.PathEscape(a.RunID) + "/calls"
	var calls handlers.RunCalls
	if err := s.api.getJSON(ctx, base, nil, &calls); err != nil {
		return nil, err
	}
	out := []mcpConversationCall{}
	var next *int
	for _, c := range calls.Calls {
		if c.Call <= a.AfterCall {
			continue
		}
		if len(out) == limit {
			last := out[len(out)-1].Call
			next = &last
			break
		}
		row := mcpConversationCall{Call: c.Call, RequestID: c.RequestID, Status: c.Status}
		var d handlers.RunCallDetail
		if c.RequestID != "" {
			if err := s.api.getJSON(ctx, base+"/"+url.PathEscape(c.RequestID), nil, &d); err != nil {
				return nil, err
			}
		}
		row.Model = d.Model
		if !d.Available {
			row.Note = mcpNotKept
		} else {
			row.Kept = true
			row.LastMessage = mcpClip(mcpLastMessage(d.Request), mcpCallTextCap)
			row.Response = mcpClip(mcpResponseText(d.Response), mcpCallTextCap)
		}
		out = append(out, row)
	}
	return map[string]any{"run_id": a.RunID, "calls": out, "next_after_call": next}, nil
}

// mcpLastMessage is the last message of a chat request body, as text.
func mcpLastMessage(body string) string {
	var req struct {
		Messages []json.RawMessage `json:"messages"`
		Input    json.RawMessage   `json:"input"`
	}
	if json.Unmarshal([]byte(body), &req) != nil {
		return body
	}
	if len(req.Messages) > 0 {
		return mcpMessageText(req.Messages[len(req.Messages)-1])
	}
	if len(req.Input) > 0 {
		var items []json.RawMessage
		if json.Unmarshal(req.Input, &items) == nil && len(items) > 0 {
			return mcpMessageText(items[len(items)-1])
		}
		return string(req.Input)
	}
	return body
}

func mcpMessageText(raw json.RawMessage) string {
	var m struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return string(raw)
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return strings.TrimSpace(m.Role + ": " + s)
	}
	return strings.TrimSpace(m.Role + ": " + string(m.Content))
}

// mcpResponseText is the assistant content and tool calls of a chat
// response body.
func mcpResponseText(body string) string {
	var resp struct {
		Choices []struct {
			Message struct {
				Content   json.RawMessage `json:"content"`
				ToolCalls []struct {
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal([]byte(body), &resp) != nil || len(resp.Choices) == 0 {
		return body
	}
	msg := resp.Choices[0].Message
	var parts []string
	var s string
	if json.Unmarshal(msg.Content, &s) == nil && s != "" {
		parts = append(parts, s)
	}
	for _, tc := range msg.ToolCalls {
		parts = append(parts, "tool call "+tc.Function.Name+" "+tc.Function.Arguments)
	}
	return strings.Join(parts, "\n")
}

// mcpCostRow is one row of cost_per_task.
type mcpCostRow struct {
	Key       string   `json:"key"`
	Label     string   `json:"label,omitempty"`
	CostUSD   *float64 `json:"cost_usd"`
	Runs      int      `json:"runs"`
	Successes int      `json:"successes"`
}

func mcpArmTotal(a struct {
	Arm        string        `json:"arm"`
	Label      string        `json:"label"`
	Runs       int           `json:"runs"`
	Successes  int           `json:"successes"`
	CostPerRun schemas.Value `json:"cost_per_run"`
}) mcpCostRow {
	r := mcpCostRow{Key: a.Arm, Label: a.Label, Runs: a.Runs, Successes: a.Successes}
	if a.CostPerRun.V != nil {
		v := *a.CostPerRun.V * float64(a.Runs)
		r.CostUSD = &v
	}
	return r
}

type mcpExplain struct {
	Condition  string   `json:"condition"`
	Conditions []string `json:"conditions"`
	A, B       struct {
		Arm        string        `json:"arm"`
		Label      string        `json:"label"`
		Runs       int           `json:"runs"`
		Successes  int           `json:"successes"`
		CostPerRun schemas.Value `json:"cost_per_run"`
	}
}

func mcpCostPerTask(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
	var a struct {
		ExpID   string `json:"exp_id"`
		GroupBy string `json:"group_by"`
		Top     int    `json:"top"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.ExpID == "" {
		return nil, errors.New("exp_id is required")
	}
	top := mcpClamp(a.Top, 20, 100)
	path := "/experiments/" + url.PathEscape(a.ExpID) + "/cost-explain"
	var e mcpExplain
	if err := s.api.getJSON(ctx, path, nil, &e); err != nil {
		return nil, err
	}
	var rows []mcpCostRow
	switch a.GroupBy {
	case "", "task":
		return nil, errors.New("cost per task is not available yet: the cost explanation is kept per arm and condition; use group_by arm or condition")
	case "arm":
		rows = []mcpCostRow{mcpArmTotal(e.A), mcpArmTotal(e.B)}
	case "condition":
		for _, c := range e.Conditions {
			var ce mcpExplain
			if err := s.api.getJSON(ctx, path, url.Values{"condition": {c}}, &ce); err != nil {
				return nil, err
			}
			ra, rb := mcpArmTotal(ce.A), mcpArmTotal(ce.B)
			row := mcpCostRow{Key: c, Runs: ra.Runs + rb.Runs, Successes: ra.Successes + rb.Successes}
			if ra.CostUSD != nil && rb.CostUSD != nil {
				v := *ra.CostUSD + *rb.CostUSD
				row.CostUSD = &v
			}
			rows = append(rows, row)
		}
	default:
		return nil, errors.New("group_by must be task, arm or condition")
	}
	var total float64
	known := true
	for _, r := range rows {
		if r.CostUSD == nil {
			known = false
			continue
		}
		total += *r.CostUSD
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ci, cj := rows[i].CostUSD, rows[j].CostUSD
		if ci == nil || cj == nil {
			return ci != nil
		}
		return *ci > *cj
	})
	if len(rows) > top {
		rows = rows[:top]
	}
	out := map[string]any{"exp_id": a.ExpID, "group_by": a.GroupBy, "rows": rows}
	if known {
		out["total_usd"] = total
	}
	return out, nil
}

// mcpFailure is one item of todays_failures.
type mcpFailure struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Source      string   `json:"source"`
	Occurrences int      `json:"occurrences"`
	Flagged     []string `json:"flagged"`
	Task        string   `json:"task,omitempty"`
}

func mcpTodaysFailures(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
	var a struct {
		SinceHours int `json:"since_hours"`
		Limit      int `json:"limit"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
	}
	hours := mcpClamp(a.SinceHours, 24, 168)
	limit := mcpClamp(a.Limit, 20, 100)
	var list schemas.InboxList
	if err := s.api.getJSON(ctx, "/inbox", nil, &list); err != nil {
		return nil, err
	}
	return mcpFailuresSince(list.Items, mcpNow().Add(-time.Duration(hours)*time.Hour), limit), nil
}

// mcpNow is the clock of todays_failures (tests set it).
var mcpNow = time.Now

func mcpFailuresSince(items []schemas.InboxItem, since time.Time, limit int) map[string]any {
	out := []mcpFailure{}
	matched := 0
	for _, it := range items {
		recent := !it.LastSeen.Before(since)
		take := recent && it.State == "new"
		if !take && it.State == "accepted" && it.LastRecurrence != nil && !it.LastRecurrence.Before(since) {
			take = true
		}
		if !take {
			continue
		}
		matched++
		if len(out) >= limit {
			continue
		}
		f := mcpFailure{ID: it.ID, Title: it.Title, Source: it.Source, Occurrences: it.Occurrences, Flagged: it.Flagged}
		if it.Task != nil {
			f.Task = it.Task.Suite + "/" + it.Task.ID
		}
		out = append(out, f)
	}
	return map[string]any{"since": since.UTC().Format(time.RFC3339), "items": out, "total": matched}
}
