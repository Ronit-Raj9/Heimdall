package mcptest

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// RunLedger is one finished run of a drafted benchmark: the agent (arm),
// the task, whether it passed, and the tool proxy ledger rows.
type RunLedger struct {
	RunID  string
	Agent  string
	Task   string
	Passed bool
	Rows   []schemas.ToolCall
}

// AgentToolStats is how one agent used one tool.
type AgentToolStats struct {
	Agent     string `json:"agent,omitempty"`
	Runs      int    `json:"runs"`      // runs of the tool's own task
	Picked    int    `json:"picked"`    // of those, runs that called the tool
	Calls     int    `json:"calls"`     // every call of the tool, in any run
	Valid     int    `json:"valid"`     // calls whose arguments fit the schema
	Succeeded int    `json:"succeeded"` // valid calls with a result that is not an error
	Passed    int    `json:"passed"`    // runs of the tool's own task that passed
}

// ToolStats is one tool across every agent.
type ToolStats struct {
	Tool string `json:"tool"`
	AgentToolStats
	PickRate    float64          `json:"pick_rate"`    // Picked / Runs
	ValidRate   float64          `json:"valid_rate"`   // Valid / Calls
	SuccessRate float64          `json:"success_rate"` // Succeeded / Calls
	P50MS       int64            `json:"p50_ms"`
	P95MS       int64            `json:"p95_ms"`
	ByAgent     []AgentToolStats `json:"by_agent"`
	// Advice says what the server author can change, when the numbers point at something.
	Advice []string `json:"advice"`
}

// ServerReport is the server-centric view of a drafted benchmark's runs.
type ServerReport struct {
	Server   string      `json:"server"`
	Runs     int         `json:"runs"`
	Agents   []string    `json:"agents"`
	Calls    int         `json:"calls"`
	Errors   int         `json:"errors"`
	Tools    []ToolStats `json:"tools"`
	Unlisted []string    `json:"unlisted_tools"` // tools agents called that the drafted listing does not have
}

// BuildServerReport aggregates runs. taskTool maps each task to the tool it
// is about; tools carries the schemas arguments are checked against. It is pure.
func BuildServerReport(server string, tools []ToolView, taskTool map[string]string, runs []RunLedger) ServerReport {
	rep := ServerReport{Server: server, Runs: len(runs), Agents: []string{}, Tools: []ToolStats{}, Unlisted: []string{}}
	schemaOf := map[string]json.RawMessage{}
	for _, t := range tools {
		schemaOf[t.Name] = t.InputSchema
	}
	type key struct{ tool, agent string }
	per := map[key]*AgentToolStats{}
	lat := map[string][]time.Duration{}
	agents := map[string]bool{}
	unlisted := map[string]bool{}
	get := func(tool, agent string) *AgentToolStats {
		k := key{tool, agent}
		if per[k] == nil {
			per[k] = &AgentToolStats{Agent: agent}
		}
		return per[k]
	}
	for _, r := range runs {
		agents[r.Agent] = true
		called := map[string]bool{}
		for _, row := range r.Rows {
			if row.Server != server || row.Blocked || row.Fault != "" || row.Kind != "" {
				continue
			}
			args, isCall := callArguments(row.Arguments)
			if !isCall {
				continue
			}
			rep.Calls++
			if row.IsError {
				rep.Errors++
			}
			schema, listed := schemaOf[row.Tool]
			if !listed {
				unlisted[row.Tool] = true
				continue
			}
			called[row.Tool] = true
			s := get(row.Tool, r.Agent)
			s.Calls++
			lat[row.Tool] = append(lat[row.Tool], time.Duration(row.LatencyMS)*time.Millisecond)
			if ValidArgs(schema, args) {
				s.Valid++
				if !row.IsError && (row.Status == 0 || row.Status < 400) {
					s.Succeeded++
				}
			}
		}
		if tool := taskTool[r.Task]; tool != "" {
			s := get(tool, r.Agent)
			s.Runs++
			if called[tool] {
				s.Picked++
			}
			if r.Passed {
				s.Passed++
			}
		}
	}
	for a := range agents {
		rep.Agents = append(rep.Agents, a)
	}
	sort.Strings(rep.Agents)
	for _, t := range tools {
		ts := ToolStats{Tool: t.Name, ByAgent: []AgentToolStats{}, Advice: []string{}}
		for _, a := range rep.Agents {
			s := per[key{t.Name, a}]
			if s == nil {
				continue
			}
			ts.ByAgent = append(ts.ByAgent, *s)
			ts.Runs += s.Runs
			ts.Picked += s.Picked
			ts.Calls += s.Calls
			ts.Valid += s.Valid
			ts.Succeeded += s.Succeeded
			ts.Passed += s.Passed
		}
		ts.Agent = ""
		ts.PickRate = ratio(ts.Picked, ts.Runs)
		ts.ValidRate = ratio(ts.Valid, ts.Calls)
		ts.SuccessRate = ratio(ts.Succeeded, ts.Calls)
		p := percentiles(lat[t.Name])
		ts.P50MS, ts.P95MS = p.P50MS, p.P95MS
		ts.Advice = advice(ts)
		rep.Tools = append(rep.Tools, ts)
	}
	for n := range unlisted {
		rep.Unlisted = append(rep.Unlisted, n)
	}
	sort.Strings(rep.Unlisted)
	return rep
}

func advice(ts ToolStats) []string {
	out := []string{}
	if ts.Runs >= 2 && ts.PickRate < 0.5 {
		out = append(out, fmt.Sprintf("Agents picked %s in only %d of %d runs of its own task: its description may not say when to use it, or another tool looks like a better fit.", ts.Tool, ts.Picked, ts.Runs))
	}
	if ts.Calls >= 2 && ts.ValidRate < 0.7 {
		out = append(out, fmt.Sprintf("%d of %d calls of %s had arguments that do not fit its schema: describe each parameter with its format and an example.", ts.Calls-ts.Valid, ts.Calls, ts.Tool))
	}
	if ts.Valid >= 2 && ratio(ts.Succeeded, ts.Valid) < 0.7 {
		out = append(out, fmt.Sprintf("%d of %d valid calls of %s failed on the server: check its error handling and the limits it applies.", ts.Valid-ts.Succeeded, ts.Valid, ts.Tool))
	}
	if ts.P95MS > 3000 {
		out = append(out, fmt.Sprintf("%s is slow for agents (p95 %d ms); every call holds up the agent's next step.", ts.Tool, ts.P95MS))
	}
	return out
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// callArguments reads the tools/call arguments a ledger row recorded. The
// second result is false when the row is not a tools/call.
func callArguments(raw string) (any, bool) {
	raw = strings.TrimSpace(raw)
	var msg struct {
		Method string `json:"method"`
		Params struct {
			Arguments json.RawMessage `json:"arguments"`
		} `json:"params"`
	}
	if strings.HasPrefix(raw, "[") {
		var batch []json.RawMessage
		if json.Unmarshal([]byte(raw), &batch) != nil {
			return nil, false
		}
		for _, b := range batch {
			if a, ok := callArguments(string(b)); ok {
				return a, true
			}
		}
		return nil, false
	}
	if json.Unmarshal([]byte(raw), &msg) != nil || msg.Method != "tools/call" {
		return nil, false
	}
	var args any = map[string]any{}
	if len(msg.Params.Arguments) > 0 && string(msg.Params.Arguments) != "null" {
		if json.Unmarshal(msg.Params.Arguments, &args) != nil {
			return nil, true
		}
	}
	return args, true
}

// ValidArgs is the verifier's schema check (assets/verify.py fits): type,
// enum, required and nested properties and items. It is pure.
func ValidArgs(schema json.RawMessage, value any) bool {
	var s any
	if len(schema) == 0 || json.Unmarshal(schema, &s) != nil {
		return value != nil
	}
	return fits(s, value)
}

func fits(schema, value any) bool {
	sm, ok := schema.(map[string]any)
	if !ok {
		return true
	}
	if enum, ok := sm["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if jsonEqual(e, value) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	switch t := sm["type"].(type) {
	case string:
		if !typeFits(t, value) {
			return false
		}
	case []any:
		anyOK, anyType := false, false
		for _, x := range t {
			if s, ok := x.(string); ok {
				anyType = true
				anyOK = anyOK || typeFits(s, value)
			}
		}
		if anyType && !anyOK {
			return false
		}
	}
	if obj, ok := value.(map[string]any); ok {
		props, _ := sm["properties"].(map[string]any)
		if req, ok := sm["required"].([]any); ok {
			for _, r := range req {
				if name, ok := r.(string); ok {
					if _, has := obj[name]; !has {
						return false
					}
				}
			}
		}
		for k, v := range obj {
			if p, ok := props[k]; ok && !fits(p, v) {
				return false
			}
		}
	}
	if arr, ok := value.([]any); ok {
		if items, ok := sm["items"].(map[string]any); ok {
			for _, x := range arr {
				if !fits(items, x) {
					return false
				}
			}
		}
	}
	return true
}

func typeFits(t string, v any) bool {
	switch t {
	case "string":
		_, ok := v.(string)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == float64(int64(f))
	case "number":
		_, ok := v.(float64)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "null":
		return v == nil
	}
	return true
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
