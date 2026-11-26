// Package tracedraft asks a model to draft what should have happened in one
// recorded session of the failure inbox: the expected answer or the right
// and forbidden tool calls, each with the reasons and the quotes they rest
// on (wave 4, W4-45). The model's words are a suggestion: every quote is
// checked against the session text the model saw, a reason with no quote
// that checks out is dropped and counted, and a person reviews the draft
// before it becomes a task.
package tracedraft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceimport"
)

// Proposal is the model's draft of what should have happened.
type Proposal struct {
	Item      string             `json:"item"`
	Kind      string             `json:"kind"`
	Guide     schemas.InboxGuide `json:"guide"`   // exactly what the accept form posts
	Reasons   []ProposalReason   `json:"reasons"` // why, each with verified quotes
	Origin    string             `json:"origin"`  // "model"
	Model     string             `json:"model"`
	CostUSD   float64            `json:"cost_usd"`
	Dropped   int                `json:"dropped_unverified"`
	CreatedAt time.Time          `json:"created_at"`
}

// ProposalReason is one reason the model gave for a guide field.
type ProposalReason struct {
	Field  string  `json:"field"` // "answers" | "calls" | "forbidden" | "reply_rubric" | "instruction"
	Text   string  `json:"text"`  // the model's sentence
	Quotes []Quote `json:"quotes"`
}

// GuideDraft and CallDraft are the guide shapes the model answers with.
type (
	GuideDraft = schemas.InboxGuide
	CallDraft  = schemas.InboxGuideCall
)

// modelDraft is the JSON the model is asked for.
type modelDraft struct {
	Guide   GuideDraft       `json:"guide"`
	Reasons []ProposalReason `json:"reasons"`
}

// Input is one draft request. Session may be unredacted: Draft redacts a
// copy before any text is put in a prompt.
type Input struct {
	Item    string
	Kind    string // qa | tool-use | mcp | memory
	Session *traceimport.Session
	Now     time.Time
}

// Purpose names the drafter's calls in the model call ledger.
const Purpose = "trace_draft"

// OriginModel marks text a model wrote.
const OriginModel = "model"

// Draft makes one model call (two with the quote repair round) and returns
// the checked proposal.
func Draft(ctx context.Context, c investigator.Caller, in Input) (*Proposal, error) {
	if c == nil {
		return nil, errors.New("this server makes no model calls")
	}
	if in.Session == nil {
		return nil, errors.New("the item has no session")
	}
	if !slices.Contains(traceimport.Kinds(), in.Kind) {
		return nil, fmt.Errorf("kind %q: want one of %s", in.Kind, strings.Join(traceimport.Kinds(), ", "))
	}
	sess, err := redactedCopy(in.Session)
	if err != nil {
		return nil, err
	}
	req := investigator.Request{Purpose: Purpose, Messages: prompt(in.Item, sess, in.Kind), Schema: replySchema, MaxTokens: 1500}
	p := &Proposal{Item: in.Item, Kind: in.Kind, Origin: OriginModel, CreatedAt: in.Now}
	draft, text, err := ask(ctx, c, req, p)
	if err != nil {
		return nil, err
	}
	src := SessionSource(in.Item, sess)
	if failed := failedQuotes(draft, src); len(failed) > 0 {
		repair := req
		repair.Messages = append(slices.Clone(req.Messages),
			investigator.Message{Role: "assistant", Content: text},
			investigator.Message{Role: "user", Content: repairMessage(failed)})
		if again, _, err := ask(ctx, c, repair, p); err == nil {
			draft = again
		} else if errors.Is(err, investigator.ErrBudget) || errors.Is(err, errOtherModel) {
			return nil, err
		}
	}
	p.Guide = fieldsFor(in.Kind, draft.Guide)
	p.Dropped += keepKnownTools(&p.Guide, sess)
	p.Reasons, p.Dropped = verified(draft.Reasons, src, p.Dropped)
	return p, nil
}

var errOtherModel = errors.New("other model")

// ask makes one call, checks the served model and parses the reply.
func ask(ctx context.Context, c investigator.Caller, req investigator.Request, p *Proposal) (modelDraft, string, error) {
	r, err := c.Call(ctx, req)
	if err != nil {
		return modelDraft{}, "", err
	}
	p.CostUSD += r.CostUSD
	if p.Model == "" {
		p.Model = r.Model
	}
	if r.Served != "" && r.Model != "" && r.Served != r.Model {
		return modelDraft{}, "", fmt.Errorf("%w", otherModelError{served: r.Served, asked: r.Model})
	}
	var d modelDraft
	if err := json.Unmarshal([]byte(stripFence(r.Text)), &d); err != nil {
		return modelDraft{}, r.Text, fmt.Errorf("the model's draft is not the JSON asked for: %v", err)
	}
	return d, r.Text, nil
}

type otherModelError struct{ served, asked string }

func (e otherModelError) Error() string {
	return fmt.Sprintf("The gateway answered with %s, not %s. Nothing was drafted.", e.served, e.asked)
}

func (e otherModelError) Is(target error) bool { return target == errOtherModel }

// stripFence removes a markdown code fence around a JSON reply.
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "```"); ok {
		rest = strings.TrimPrefix(rest, "json")
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "```"))
	}
	return s
}

// redactedCopy is a deep copy of s, redacted. The prompt is built from it
// only.
func redactedCopy(s *traceimport.Session) (*traceimport.Session, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var out traceimport.Session
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	traceimport.NewRedactor().Session(&out)
	return &out, nil
}

// fieldsFor keeps the guide fields of the kind: answers for qa and memory,
// calls and the reply rubric for tool-use and mcp; the instruction for all.
func fieldsFor(kind string, g GuideDraft) schemas.InboxGuide {
	out := schemas.InboxGuide{Instruction: g.Instruction}
	switch kind {
	case traceimport.KindQA, traceimport.KindMemory:
		out.Answers, out.Match, out.WrongAnswers, out.RecordedWrong = g.Answers, g.Match, g.WrongAnswers, g.RecordedWrong
	default:
		out.Calls, out.Forbidden, out.Ordered, out.ReplyRubric = g.Calls, g.Forbidden, g.Ordered, g.ReplyRubric
	}
	return out
}

// keepKnownTools drops the calls and forbidden calls that name a tool the
// session never used, and returns how many it dropped.
func keepKnownTools(g *schemas.InboxGuide, s *traceimport.Session) int {
	known := map[string]bool{}
	for _, c := range s.ToolCalls {
		known[c.Name] = true
	}
	dropped := 0
	keep := func(l []schemas.InboxGuideCall) []schemas.InboxGuideCall {
		var out []schemas.InboxGuideCall
		for _, c := range l {
			if known[c.Tool] {
				out = append(out, c)
			} else {
				dropped++
			}
		}
		return out
	}
	g.Calls, g.Forbidden = keep(g.Calls), keep(g.Forbidden)
	return dropped
}

// failedQuotes lists the quotes of the draft that do not check out.
func failedQuotes(d modelDraft, src func(string, int) (string, bool)) []QuoteResult {
	var out []QuoteResult
	for _, r := range d.Reasons {
		for _, q := range CheckQuotes(r.Quotes, src) {
			if !q.OK {
				out = append(out, q)
			}
		}
	}
	return out
}

// verified keeps only the quotes that check out, drops a reason left with
// none, and adds the dropped reasons to dropped.
func verified(reasons []ProposalReason, src func(string, int) (string, bool), dropped int) ([]ProposalReason, int) {
	out := []ProposalReason{}
	for _, r := range reasons {
		var ok []Quote
		for _, q := range CheckQuotes(r.Quotes, src) {
			if q.OK {
				ok = append(ok, q.Quote)
			}
		}
		if len(ok) == 0 {
			dropped++
			continue
		}
		r.Quotes = ok
		out = append(out, r)
	}
	return out, dropped
}

func repairMessage(failed []QuoteResult) string {
	var b strings.Builder
	b.WriteString("These quotes were not found exactly in the steps you cited. Copy them again exactly from the text, or drop them:")
	for _, f := range failed {
		fmt.Fprintf(&b, "\n- %q (closest: %q)", f.Quote.Text, f.Closest)
	}
	b.WriteString("\nAnswer again with the whole JSON.")
	return b.String()
}
