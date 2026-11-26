package investigator

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func steps(n int) []schemas.StepRecord {
	out := make([]schemas.StepRecord, n)
	for i := range out {
		out[i] = schemas.StepRecord{Seq: i + 1, Kind: "tool_request", Summary: fmt.Sprintf("step number %d ran a command", i+1)}
	}
	return out
}

func TestInvestigateWindow(t *testing.T) {
	st := steps(1000)
	errs := []int{100, 300, 301, 600, 900}
	for _, e := range errs {
		st[e-1].IsError = true
	}
	code := 3
	st[700-1].ExitCode = &code
	errs = append(errs, 700)
	got := Window(st)
	if len(got) > 200 {
		t.Fatalf("window has %d rows, want at most 200", len(got))
	}
	have := map[int]bool{}
	last := 0
	for _, s := range got {
		if s.Seq <= last {
			t.Fatalf("rows out of order at %d", s.Seq)
		}
		last = s.Seq
		have[s.Seq] = true
	}
	for _, e := range errs {
		for k := e - 5; k <= e; k++ {
			if !have[k] {
				t.Fatalf("window misses step %d (error %d or one of its 5 predecessors)", k, e)
			}
		}
	}
	for k := 1; k <= 10; k++ {
		if !have[k] {
			t.Fatalf("window misses first step %d", k)
		}
	}
	for k := 961; k <= 1000; k++ {
		if !have[k] {
			t.Fatalf("window misses last step %d", k)
		}
	}
}

func TestInvestigateRedactsPrompt(t *testing.T) {
	st := steps(3)
	st[1].Summary = "export OPENAI_API_KEY=sk-abcdefghijklmnopqrstuvwxyz123456 and mail jane.doe@example.com"
	f := &FakeModel{Replies: []string{`{"items":[]}`}, CostUSD: 0.01, Cap: 1}
	if _, err := Investigate(context.Background(), f, "", []RunInput{{RunID: "r1", Task: "s/t", Steps: st, Final: "contact jane.doe@example.com"}}); err != nil {
		t.Fatal(err)
	}
	for _, m := range f.Seen[0].Messages {
		if strings.Contains(m.Content, "sk-abcdefghij") || strings.Contains(m.Content, "jane.doe@example.com") {
			t.Fatalf("prompt not redacted: %s", m.Content)
		}
	}
}

func TestInvestigateDropsUnverified(t *testing.T) {
	bad := `{"items":[{"title":"Wrong file","hypothesis":"The agent edited the wrong file.","kind":"agent_mistake","quotes":[{"seq":2,"text":"the agent removed everything in the folder"}]}]}`
	f := &FakeModel{Replies: []string{bad, bad}, CostUSD: 0.01, Cap: 1}
	rep, err := Investigate(context.Background(), f, "", []RunInput{{RunID: "r1", Task: "s/t", Steps: steps(5)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Items) != 0 || rep.Dropped != 1 {
		t.Fatalf("items %d dropped %d, want 0 and 1", len(rep.Items), rep.Dropped)
	}
	if len(f.Seen) != 2 || !strings.Contains(f.Seen[1].Messages[len(f.Seen[1].Messages)-1].Content, "These quotes were not found exactly") {
		t.Fatalf("no repair round was sent: %d calls", len(f.Seen))
	}
}

func goodReply(seq int) string {
	return fmt.Sprintf(`{"items":[{"title":"Command failed","hypothesis":"The command at step %d failed.","kind":"environment","quotes":[{"seq":%d,"text":"step number %d ran a command"}]}]}`, seq, seq, seq)
}

func TestInvestigateBudgetStops(t *testing.T) {
	f := &FakeModel{Replies: []string{goodReply(1), goodReply(2), goodReply(3), `{"groups":[]}`}, CostUSD: 1, Cap: 2}
	runs := []RunInput{{RunID: "r1", Task: "s/a", Steps: steps(3)}, {RunID: "r2", Task: "s/b", Steps: steps(3)}, {RunID: "r3", Task: "s/c", Steps: steps(3)}}
	rep, err := Investigate(context.Background(), f, "", runs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rep.Runs, ",") != "r1,r2" || strings.Join(rep.Skipped, ",") != "r3" || rep.Stopped != "budget" {
		t.Fatalf("runs %v skipped %v stopped %q", rep.Runs, rep.Skipped, rep.Stopped)
	}
	if rep.SpentUSD != 2 || rep.BudgetUSD != 2 || len(rep.Items) != 2 {
		t.Fatalf("spent %v budget %v items %d", rep.SpentUSD, rep.BudgetUSD, len(rep.Items))
	}
	if !strings.HasPrefix(rep.Note, "These are hypotheses written by a model.") {
		t.Fatalf("note %q", rep.Note)
	}
}

func TestInvestigateGroupsIgnoreUnknownIDs(t *testing.T) {
	groups := `{"groups":[{"title":"Same command","kind":"environment","items":["i0","i9","i1"]}]}`
	f := &FakeModel{Replies: []string{goodReply(1), goodReply(2), goodReply(3), groups}, CostUSD: 0.01, Cap: 1}
	runs := []RunInput{{RunID: "r1", Task: "s/a", Steps: steps(3)}, {RunID: "r2", Task: "s/b", Steps: steps(3)}, {RunID: "r3", Task: "s/a", Steps: steps(3)}}
	rep, err := Investigate(context.Background(), f, "", runs)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Groups) != 2 {
		t.Fatalf("groups %+v, want 2", rep.Groups)
	}
	g := rep.Groups[0]
	if strings.Join(g.Items, ",") != "i0,i1" || g.Runs != 2 || strings.Join(g.Tasks, ",") != "s/a,s/b" {
		t.Fatalf("first group %+v", g)
	}
	if strings.Join(rep.Groups[1].Items, ",") != "i2" {
		t.Fatalf("the item in no group is not its own group: %+v", rep.Groups[1])
	}
	// The grouping call sends no step text.
	last := f.Seen[len(f.Seen)-1]
	for _, m := range last.Messages {
		if strings.Contains(m.Content, "#1 tool_request") {
			t.Fatalf("the grouping call carries step text: %s", m.Content)
		}
	}
}
