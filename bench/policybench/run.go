package policybench

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
)

// GenerateOptions say which clauses to draft and how.
type GenerateOptions struct {
	Clauses  []string // empty: every clause not skipped and not drafted yet
	Redraft  bool     // draft again clauses whose drafts are pending (replaced)
	HeldOut  bool
	Taken    func(id string) bool // task ids already in the benchmark
	By       string
	Now      func() time.Time
	Progress func(done, total int, spent float64)
	Logf     func(format string, args ...any)
}

// Skip is a clause that produced no draft, and why.
type Skip struct {
	Clause string `json:"clause"`
	Reason string `json:"reason"`
}

// GenerateResult is what one generation did.
type GenerateResult struct {
	Doc       string   `json:"doc"`
	Model     string   `json:"model"`
	BudgetUSD float64  `json:"budget_usd"`
	SpentUSD  float64  `json:"spent_usd"`
	Calls     int      `json:"calls"`
	Clauses   int      `json:"clauses"`
	Drafted   []Draft  `json:"drafted"`
	Skipped   []Skip   `json:"skipped"`
	Stopped   bool     `json:"stopped,omitempty"`
	PolicyIn  string   `json:"policy_in"`
	Problems  []string `json:"problems,omitempty"`
	Next      string   `json:"next"`
}

// Selected is the clauses a generation drafts.
func Selected(d *Doc, want []string, redraft bool) ([]Clause, []Skip, error) {
	drafted := map[string]bool{}
	for _, x := range d.Drafts {
		if x.Status == StatusAccepted || (x.Status == StatusPending && !redraft) {
			drafted[x.Clause] = true
		}
	}
	var out []Clause
	var skipped []Skip
	if len(want) > 0 {
		for _, n := range want {
			c, ok := d.Clause(n)
			if !ok {
				return nil, nil, fmt.Errorf("clause %s is not in the document", n)
			}
			if drafted[n] {
				skipped = append(skipped, Skip{Clause: n, Reason: "already drafted (reject its drafts, or ask to draft again)"})
				continue
			}
			out = append(out, c)
		}
		return out, skipped, nil
	}
	for _, c := range d.Clauses {
		switch {
		case c.Skip:
		case drafted[c.Number]:
		default:
			out = append(out, c)
		}
	}
	return out, skipped, nil
}

// Forecast is the worst-case cost of one attempt per clause.
func Forecast(d *Doc, clauses []Clause, tools []Tool, seed any, price func(string, schemas.Tokens) (float64, bool), model string, maxTokens int) (float64, bool) {
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	sum := SeedSummary(seed, 6000)
	var usd float64
	for _, c := range clauses {
		p := Prompt(PromptInput{PolicyTitle: d.Title, Clause: c, Context: neighbours(d.Clauses, indexOf(d.Clauses, c.Number), 2), Tools: tools, SeedSummary: sum})
		v, ok := price(model, schemas.Tokens{Fresh: int64((len(SystemPrompt)+len(p))/3 + 64), Out: int64(maxTokens)})
		if !ok {
			return 0, false
		}
		usd += v
	}
	return usd, true
}

func indexOf(cs []Clause, n string) int {
	for i, c := range cs {
		if c.Number == n {
			return i
		}
	}
	return 0
}

// Generate drafts the selected clauses of d into pending tasks under the
// document's folder, saving doc.json after each clause so a stopped job
// keeps what it drafted. It stops at the budget.
func Generate(ctx context.Context, g *Generator, suiteRoot string, d *Doc, shape *scaffold.ScenarioShape, tools []Tool, o GenerateOptions) (*GenerateResult, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	clauses, skipped, err := Selected(d, o.Clauses, o.Redraft)
	if err != nil {
		return nil, err
	}
	psh, where, err := PolicyShape(shape, d)
	if err != nil {
		return nil, err
	}
	res := &GenerateResult{Doc: d.ID, Model: g.ModelRef(), BudgetUSD: g.Budget(), Clauses: len(clauses), Skipped: skipped, PolicyIn: where, Drafted: []Draft{}}
	if res.Skipped == nil {
		res.Skipped = []Skip{}
	}
	d.PolicyIn = where
	allowed := map[string]bool{}
	for _, t := range tools {
		allowed[t.Name] = true
	}
	sum := SeedSummary(psh.Seed, 6000)
	gen := Generation{At: o.Now().UTC().Format(time.RFC3339), Model: g.ModelRef(), PromptSHA: PromptSHA(), BudgetUSD: g.Budget(), By: o.By}
	finish := func() error {
		res.SpentUSD, res.Calls = g.Spent()
		gen.SpentUSD, gen.Calls, gen.Drafted, gen.Stopped = res.SpentUSD, res.Calls, len(res.Drafted), res.Stopped
		d.Generations = append(d.Generations, gen)
		return Save(suiteRoot, d, "")
	}
	for i, c := range clauses {
		if ctx.Err() != nil {
			_ = finish()
			return res, ctx.Err()
		}
		gen.Clauses = append(gen.Clauses, c.Number)
		cd := &ClauseDraft{tools: allowed}
		prompt := Prompt(PromptInput{PolicyTitle: d.Title, Clause: c, Context: neighbours(d.Clauses, indexOf(d.Clauses, c.Number), 2), Tools: tools, SeedSummary: sum})
		cost, attempts, err := g.Ask(ctx, SystemPrompt, prompt, cd)
		if errors.Is(err, ErrBudget) {
			res.Stopped = true
			res.Skipped = append(res.Skipped, Skip{Clause: c.Number, Reason: err.Error()})
			for _, rest := range clauses[i+1:] {
				res.Skipped = append(res.Skipped, Skip{Clause: rest.Number, Reason: "not drafted: the budget ran out"})
			}
			break
		}
		if err != nil {
			res.Skipped = append(res.Skipped, Skip{Clause: c.Number, Reason: err.Error()})
			o.Logf("clause %s: %v", c.Number, err)
			continue
		}
		if !cd.Testable {
			d.NotTestable = slices.DeleteFunc(d.NotTestable, func(n NoTest) bool { return n.Clause == c.Number })
			d.NotTestable = append(d.NotTestable, NoTest{Clause: c.Number, Reason: cd.Reason})
			res.Skipped = append(res.Skipped, Skip{Clause: c.Number, Reason: "nothing to test: " + cd.Reason})
			_ = Save(suiteRoot, d, "")
			continue
		}
		// Replace pending drafts of the clause when drafting again.
		for j := range d.Drafts {
			x := &d.Drafts[j]
			if x.Clause == c.Number && x.Status == StatusPending {
				_ = os.RemoveAll(DraftDir(suiteRoot, d.ID, x.Task))
				x.Status = StatusRejected
				x.DecidedBy, x.DecidedAt = "drafted again", o.Now().UTC().Format(time.RFC3339)
			}
		}
		built, problems := BuildClause(psh, d, c, cd, BuildOptions{HeldOut: o.HeldOut, Model: g.ModelRef(), Taken: o.Taken})
		res.Problems = append(res.Problems, problems...)
		per := cost / float64(max(1, len(built)))
		for _, t := range built {
			t.Draft.CostUSD = per
			t.Draft.At = o.Now().UTC().Format(time.RFC3339)
			if err := WriteDraft(suiteRoot, d.ID, t); err != nil {
				_ = finish()
				return res, fmt.Errorf("write draft %s: %w", t.Draft.Task, err)
			}
			d.Drafts = slices.DeleteFunc(d.Drafts, func(x Draft) bool { return x.Task == t.Draft.Task })
			d.Drafts = append(d.Drafts, t.Draft)
			res.Drafted = append(res.Drafted, t.Draft)
		}
		if len(built) == 0 {
			res.Skipped = append(res.Skipped, Skip{Clause: c.Number, Reason: "no scenario could become a task: " + strings.Join(problems, "; ")})
		}
		if err := Save(suiteRoot, d, ""); err != nil {
			return res, err
		}
		spent, _ := g.Spent()
		o.Logf("clause %s: %d draft(s) in %d attempt(s), $%.4f spent of $%.2f", c.Number, len(built), attempts, spent, g.Budget())
		if o.Progress != nil {
			o.Progress(i+1, len(clauses), spent)
		}
	}
	if err := finish(); err != nil {
		return res, err
	}
	switch {
	case len(res.Drafted) == 0:
		res.Next = "Nothing was drafted; see the reasons per clause."
	case res.Stopped:
		res.Next = fmt.Sprintf("The budget ran out after %d draft task(s). Review them, accept the good ones, then run the readiness check.", len(res.Drafted))
	default:
		res.Next = fmt.Sprintf("%d draft task(s) are pending review. Accept the good ones, then run the readiness check: each task's compliant reference must pass and its violating control must fail.", len(res.Drafted))
	}
	return res, nil
}

// SuiteWriter writes suite.yaml (versioned, guarded by the old content).
type SuiteWriter func(old, next []byte, note string) error

// Accept moves pending drafts into the benchmark's tasks and adds them to
// suite.yaml in one write (after edit, such as a grader switch, when set);
// on any failure the moves are undone.
func Accept(suiteRoot string, d *Doc, tasks []string, actor string, now time.Time, edit func([]byte) ([]byte, error), write SuiteWriter) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(suiteRoot, "suite.yaml"))
	if err != nil {
		return nil, err
	}
	next := raw
	if edit != nil {
		if next, err = edit(raw); err != nil {
			return nil, err
		}
	}
	type move struct{ from, to string }
	var moved []move
	undo := func() {
		for i := len(moved) - 1; i >= 0; i-- {
			_ = os.Rename(moved[i].to, moved[i].from)
		}
	}
	var done []string
	for _, id := range tasks {
		x := d.DraftByTask(id)
		if x == nil {
			undo()
			return nil, fmt.Errorf("task %s is not a draft of this document", id)
		}
		if x.Status != StatusPending {
			undo()
			return nil, fmt.Errorf("task %s is %s, not pending", id, x.Status)
		}
		from, to := DraftDir(suiteRoot, d.ID, id), filepath.Join(suiteRoot, "tasks", id)
		if _, err := os.Stat(to); err == nil {
			undo()
			return nil, fmt.Errorf("task %s already exists in the benchmark", id)
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			undo()
			return nil, err
		}
		if err := os.Rename(from, to); err != nil {
			undo()
			return nil, fmt.Errorf("move draft %s: %w", id, err)
		}
		moved = append(moved, move{from, to})
		n, _, err := scaffold.AddTaskID(next, id)
		if err != nil {
			undo()
			return nil, err
		}
		next = n
		done = append(done, id)
	}
	if len(done) == 0 {
		return nil, errors.New("no task to accept")
	}
	if err := write(raw, next, fmt.Sprintf("accept %d policy task(s) from %s", len(done), d.Name)); err != nil {
		undo()
		return nil, err
	}
	at := now.UTC().Format(time.RFC3339)
	for _, id := range done {
		x := d.DraftByTask(id)
		x.Status, x.DecidedBy, x.DecidedAt = StatusAccepted, actor, at
	}
	return done, Save(suiteRoot, d, "")
}

// Reject drops pending drafts.
func Reject(suiteRoot string, d *Doc, tasks []string, actor string, now time.Time) ([]string, error) {
	var done []string
	at := now.UTC().Format(time.RFC3339)
	for _, id := range tasks {
		x := d.DraftByTask(id)
		if x == nil {
			return done, fmt.Errorf("task %s is not a draft of this document", id)
		}
		if x.Status != StatusPending {
			return done, fmt.Errorf("task %s is %s, not pending (delete an accepted task from the benchmark instead)", id, x.Status)
		}
		if err := os.RemoveAll(DraftDir(suiteRoot, d.ID, id)); err != nil {
			return done, err
		}
		x.Status, x.DecidedBy, x.DecidedAt = StatusRejected, actor, at
		done = append(done, id)
	}
	return done, Save(suiteRoot, d, "")
}
