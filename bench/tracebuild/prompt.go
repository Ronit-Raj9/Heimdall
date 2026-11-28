package tracebuild

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/traceimport"
)

const diagnoseSchema = `{"type":"object","required":["kind","summary","quotes"],"properties":{` +
	`"kind":{"enum":["wrong_tool","wrong_arguments","false_claim","missing_step","unsafe_action","other"]},"summary":{"type":"string"},` +
	`"quotes":{"type":"array","items":{"type":"object","required":["run_id","seq","text"],"properties":{"run_id":{"type":"string"},"seq":{"type":"integer"},"text":{"type":"string"}}}}}}`

const writeSchema = `{"type":"object","required":["instruction","variants"],"properties":{` +
	`"instruction":{"type":"string"},"answers":{"type":"array","items":{"type":"string"}},"match":{"type":"string"},` +
	`"calls":{"type":"array"},"forbidden":{"type":"array"},"ordered":{"type":"boolean"},` +
	`"variants":{"type":"array"},"standin":{"type":"array"},"standin_why":{"type":"string"},"unsure":{"type":"string"}}}`

const diagnoseSystem = `You review one recorded agent session that went wrong and name where the agent went wrong.
Answer with JSON only: {"kind": one of wrong_tool, wrong_arguments, false_claim, missing_step, unsafe_action, other, "summary": one sentence, "quotes": [{"run_id": the session id given, "seq": the tool call number, or 0 for the turns and the final answer, "text": an exact copy of 8 to 240 characters}]}.
false_claim: the final answer says something the tool results do not support. Copy quotes exactly; a quote not found is dropped.`

const writeSystem = `You write one benchmark task that catches the failure named below. Answer with JSON only.
"instruction": the user's request as a task text; never state the expected answer in it.
For shape qa: "answers" (the right answer, one fact per entry), "match" (exact, contains_all, regex, numeric or choice).
For shape tool-use or mcp: "calls" (each {"tool", "args"} a correct agent makes), "forbidden" (calls it must never make), "ordered".
"variants": two to four wrong agents, each {"name", "what_it_does_wrong", "answer" (qa: its wrong final answer) or "calls" (tool shapes: its call list)}.
"standin": only when the right behaviour needs a tool result the session never showed: [{"tool", "arguments", "result", "is_error"}], with "standin_why".
"unsure": say here what you are not sure of; leave it empty when sure. Never write review or approval fields.`

func sessionText(item string, s *traceimport.Session) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Session id (run_id for quotes): %s\n\nTurns (quote them with seq 0):\n", item)
	for i, t := range s.Turns {
		fmt.Fprintf(&b, "T%d %s: %s\n", i+1, t.Role, t.Text)
	}
	b.WriteString("\nTool calls (quote them with their seq):\n")
	if len(s.ToolCalls) == 0 {
		b.WriteString("none\n")
	}
	for _, c := range s.ToolCalls {
		status := "ok"
		if c.IsError {
			status = "failed"
		}
		fmt.Fprintf(&b, "C%d %s (%s)\n  arguments: %s\n", c.Seq, c.Name, status, c.Arguments)
		if c.Result != "" {
			fmt.Fprintf(&b, "  result: %s\n", c.Result)
		}
		if c.Error != "" {
			fmt.Fprintf(&b, "  error: %s\n", c.Error)
		}
	}
	fmt.Fprintf(&b, "\nFinal answer (seq 0): %s\n\nOutcome: %s\n", s.FinalAnswer, s.Outcome.Status)
	for _, h := range s.Outcome.Hints {
		fmt.Fprintf(&b, "- %s\n", h)
	}
	return b.String()
}

func feedbackText(f string) string {
	if f = strings.TrimSpace(f); f == "" {
		return ""
	}
	return "\nAn earlier task built from this session was refused by admission: " + oneLine(f, 600) + "\nFix that.\n"
}

func diagnosePrompt(item string, s *traceimport.Session, feedback string) []investigator.Message {
	return []investigator.Message{{Role: "system", Content: diagnoseSystem}, {Role: "user", Content: sessionText(item, s) + feedbackText(feedback)}}
}

func writePrompt(item string, s *traceimport.Session, f Failure, shape, feedback string) []investigator.Message {
	diag, _ := json.Marshal(f)
	return []investigator.Message{{Role: "system", Content: writeSystem}, {Role: "user", Content: fmt.Sprintf("Shape: %s\nFailure: %s\n\n%s%s", shape, diag, sessionText(item, s), feedbackText(feedback))}}
}
