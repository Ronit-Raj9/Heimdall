package tracedraft

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/traceimport"
)

// replySchema is the JSON schema the model's reply must match.
const replySchema = `{"type":"object","required":["guide","reasons"],"properties":{` +
	`"guide":{"type":"object"},` +
	`"reasons":{"type":"array","items":{"type":"object","required":["field","text","quotes"],"properties":{` +
	`"field":{"enum":["answers","calls","forbidden","reply_rubric","instruction"]},"text":{"type":"string"},` +
	`"quotes":{"type":"array","items":{"type":"object","required":["run_id","seq","text"],"properties":{"run_id":{"type":"string"},"seq":{"type":"integer"},"text":{"type":"string"}}}}}}}}}`

const systemPrompt = `You review one recorded agent session that went wrong and draft what should have happened, so it can become a benchmark task.
Answer with JSON only: {"guide": {...}, "reasons": [...]}.
For kind qa or memory, "guide" holds "answers" (the right answer, one fact per entry), "match" (exact, contains_all, regex, numeric or choice), "wrong_answers" (answers that must fail) and "recorded_wrong" (true when the recorded final answer is wrong).
For kind tool-use or mcp, "guide" holds "calls" (the calls a correct agent makes, each {"tool": name, "args": {...}} with only the arguments that matter), "forbidden" (calls it must never make), "ordered" (true when the calls must happen in that order) and "reply_rubric" (what a good final reply says).
"guide" may also hold "instruction", a clearer task text for the first request.
Only name tools that appear in the session.
Each reason says why one guide field is right: {"field": "answers"|"calls"|"forbidden"|"reply_rubric"|"instruction", "text": one sentence, "quotes": [{"run_id": the session id given, "seq": the call number, or 0 for the turns and the final answer, "text": an exact copy of 8 to 240 characters from that step}]}.
Copy quotes exactly. A reason without a quote found in the session is dropped.`

// prompt builds the messages for one redacted session: the requests and
// turns (T<i>), the tool calls with arguments and results (C<seq>), the
// final answer, the outcome hints and the kind.
func prompt(item string, s *traceimport.Session, kind string) []investigator.Message {
	var b strings.Builder
	fmt.Fprintf(&b, "Kind: %s\nSession id (run_id for quotes): %s\n\nTurns (quote them with seq 0):\n", kind, item)
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
	return []investigator.Message{{Role: "system", Content: systemPrompt}, {Role: "user", Content: b.String()}}
}
