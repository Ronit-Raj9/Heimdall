package traceinbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceimport"
)

// ErrInvalid marks a request the inbox refuses as asked (HTTP 400).
var ErrInvalid = errors.New("invalid request")

// ErrOff marks the webhook when no webhook source is configured or its
// token variable is empty (HTTP 503).
var ErrOff = errors.New("the inbox webhook is off: add a webhook source with token_env to inbox/sources.yaml and set that variable")

// Service is the inbox as the HTTP API serves it (handlers/inbox.go).
type Service struct {
	Inbox *Inbox

	mu      sync.Mutex
	gate    *Gate
	hook    *Source
	hookKey string
}

// NewService wraps an inbox for the API. The webhook gate is built from the
// configured webhook source and its token variable; without both the
// webhook stays off. When the sources file changes while the server runs
// (Inbox.Sources rereads it), the gate is rebuilt on the next request.
func NewService(ib *Inbox) *Service {
	s := &Service{Inbox: ib}
	if wh, ok := ib.Config.Webhook(); ok {
		s.setHook(wh)
	}
	return s
}

// webhookKey names the settings the gate is built from, so a changed
// webhook source (added, removed, other token variable, rate or size)
// rebuilds it and an unchanged one keeps its rate limiter.
func webhookKey(wh *Source) string {
	if wh == nil {
		return ""
	}
	return fmt.Sprintf("%s\x00%s\x00%d\x00%d", wh.Name, wh.TokenEnv, wh.RatePerMinute, wh.MaxBytes)
}

func (s *Service) setHook(wh *Source) {
	s.hook, s.hookKey, s.gate = wh, webhookKey(wh), nil
	if wh != nil {
		s.gate = NewGate(s.Inbox.getenv(wh.TokenEnv), wh.RatePerMinute, wh.MaxBytes)
	}
}

// current returns the gate and webhook source of the sources as they are
// now, rebuilding the gate when the webhook source changed since the last
// call.
func (s *Service) current() (*Gate, *Source) {
	var wh *Source
	for _, src := range s.Inbox.Sources() {
		if src.Kind == KindWebhook {
			src := src
			wh = &src
			break
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if webhookKey(wh) != s.hookKey {
		s.setHook(wh)
	}
	return s.gate, s.hook
}

// Gate returns the webhook gate, or nil when the webhook is off.
func (s *Service) Gate() *Gate {
	g, _ := s.current()
	if g == nil || !g.Enabled() {
		return nil
	}
	return g
}

// List answers GET /inbox.
func (s *Service) List(ctx context.Context, state, source string) (*schemas.InboxList, error) {
	if state != "" && !validState(state) {
		return nil, fmt.Errorf("%w: state %q", ErrInvalid, state)
	}
	items, err := s.Inbox.List(ctx, state, source)
	if err != nil {
		return nil, err
	}
	counts, err := s.Inbox.Counts()
	if err != nil {
		return nil, err
	}
	out := &schemas.InboxList{Items: items, Counts: counts, Sources: []schemas.InboxSourceStatus{}, Suites: []schemas.InboxSuite{},
		IngestEnabled: s.Gate() != nil, RegressionTasks: RegressionCount(s.Inbox.BenchmarksRoot)}
	if out.Items == nil {
		out.Items = []schemas.InboxItem{}
	}
	cursors, err := s.Inbox.Store.Cursors()
	if err != nil {
		return nil, err
	}
	for _, src := range s.Inbox.Sources() {
		st := schemas.InboxSourceStatus{Name: src.Name, Kind: src.Kind}
		if c, ok := cursors[src.Name]; ok {
			st.LastError, st.Pulled = c.LastError, c.Pulled
			if !c.LastPull.IsZero() {
				t := c.LastPull
				st.LastPull = &t
			}
			if !c.Since.IsZero() {
				t := c.Since
				st.Since = &t
			}
		}
		out.Sources = append(out.Sources, st)
	}
	suites, _ := ListSuites(s.Inbox.BenchmarksRoot)
	for _, su := range suites {
		if slices.Contains(traceimport.Kinds(), su.Template) {
			out.Suites = append(out.Suites, schemas.InboxSuite{ID: su.ID, Template: su.Template})
		}
	}
	return out, nil
}

// Item answers GET /inbox/{id}.
func (s *Service) Item(ctx context.Context, id string) (*schemas.InboxItemDetail, error) {
	it, preview, err := s.Inbox.Show(ctx, id)
	if err != nil {
		return nil, err
	}
	d := &schemas.InboxItemDetail{Item: *it, Preview: string(preview)}
	if sess, err := s.Inbox.Store.Session(id); err == nil && sess != nil {
		d.Session = SessionSummary(sess)
	}
	return d, nil
}

// SessionSummary is the accept form's view of a stored (redacted) session.
func SessionSummary(sess *traceimport.Session) *schemas.InboxSession {
	out := &schemas.InboxSession{Requests: sess.Requests(), Calls: []schemas.InboxSessionCall{}, FinalAnswer: capText(strings.TrimSpace(sess.FinalAnswer), 4000)}
	if out.Requests == nil {
		out.Requests = []string{}
	}
	for i, c := range sess.ToolCalls {
		if i == 100 {
			break
		}
		args := string(c.Arguments)
		if args == "" {
			args = "{}"
		}
		out.Calls = append(out.Calls, schemas.InboxSessionCall{Seq: c.Seq, Tool: c.Name, Args: args,
			Result: oneLine(firstNonEmpty(c.Error, c.Result), 300), IsError: c.IsError})
	}
	return out
}

// Accept answers POST /inbox/{id}/accept.
func (s *Service) Accept(ctx context.Context, id string, req schemas.InboxAcceptRequest) (*schemas.InboxAcceptResult, error) {
	if req.Suite == "" {
		return nil, fmt.Errorf("%w: suite is required", ErrInvalid)
	}
	opts := AcceptOptions{Suite: req.Suite, Kind: req.Kind, TaskID: req.TaskID, Guide: GuideFrom(req.Guide), SwitchGrader: req.SwitchGrader}
	if req.Judge != nil {
		opts.Judge = &scaffold.JudgeConfig{Provider: strings.TrimSpace(req.Judge.Provider), Model: strings.TrimSpace(req.Judge.Model)}
	}
	res, err := s.Inbox.Accept(ctx, id, opts)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	d := schemas.InboxDraft{Task: res.Task, Dir: res.Item.Task.Dir, Template: res.Draft.Template, EditMe: res.Draft.EditMe,
		Finished: opts.Guide != nil && traceimport.GuidedDone(res.Draft), Next: AcceptSteps(res, s.Inbox.ProjectRoot), Warnings: res.Draft.Warnings}
	if d.EditMe == nil {
		d.EditMe = []string{}
	}
	return &schemas.InboxAcceptResult{Item: res.Item, Draft: d}, nil
}

// GuideFrom converts the accept form's guide; nil stays nil.
func GuideFrom(g *schemas.InboxGuide) *traceimport.Guide {
	if g == nil {
		return nil
	}
	calls := func(l []schemas.InboxGuideCall) []traceimport.GuideCall {
		var out []traceimport.GuideCall
		for _, c := range l {
			out = append(out, traceimport.GuideCall{Tool: c.Tool, Args: c.Args})
		}
		return out
	}
	return &traceimport.Guide{Instruction: g.Instruction, KeepTurns: g.KeepTurns, UseRecorded: g.UseRecorded,
		Answers: slices.Clone(g.Answers), Match: g.Match, WrongAnswers: slices.Clone(g.WrongAnswers), RecordedWrong: g.RecordedWrong,
		Calls: calls(g.Calls), Forbidden: calls(g.Forbidden), Ordered: g.Ordered, ReplyRubric: g.ReplyRubric}
}

// MaxBulkAccept caps the items one bulk accept takes.
const MaxBulkAccept = 100

// BulkAccept answers POST /inbox/accept: each item accepted into one suite
// with the shared guide, task ids from the item ids. One item failing does
// not stop the others.
func (s *Service) BulkAccept(ctx context.Context, req schemas.InboxBulkAcceptRequest) (*schemas.InboxBulkAcceptResult, error) {
	if req.Suite == "" {
		return nil, fmt.Errorf("%w: suite is required", ErrInvalid)
	}
	if len(req.IDs) == 0 || len(req.IDs) > MaxBulkAccept {
		return nil, fmt.Errorf("%w: ids: 1 to %d items", ErrInvalid, MaxBulkAccept)
	}
	out := &schemas.InboxBulkAcceptResult{Accepted: []schemas.InboxAcceptResult{}, Failed: []schemas.InboxBulkFailure{}}
	seen := map[string]bool{}
	for _, id := range req.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if ctx.Err() != nil {
			out.Failed = append(out.Failed, schemas.InboxBulkFailure{ID: id, Error: "stopped: " + ctx.Err().Error()})
			continue
		}
		var g *schemas.InboxGuide
		if req.Guide != nil {
			cp := *req.Guide
			g = &cp
		}
		res, err := s.Accept(ctx, id, schemas.InboxAcceptRequest{Suite: req.Suite, Kind: req.Kind, Guide: g, SwitchGrader: req.SwitchGrader, Judge: req.Judge})
		if err != nil {
			out.Failed = append(out.Failed, schemas.InboxBulkFailure{ID: id, Error: err.Error()})
			continue
		}
		out.Accepted = append(out.Accepted, *res)
	}
	return out, nil
}

// AcceptSteps are the steps a person takes after an accept, with paths
// relative to the project root when they are under it.
func AcceptSteps(res *AcceptResult, projectRoot string) []string {
	dir := res.Draft.Dir
	if projectRoot != "" {
		if r, err := filepath.Rel(projectRoot, dir); err == nil && !startsWithDotDot(r) {
			dir = r
		}
	}
	var out []string
	for _, rel := range res.Draft.EditMe {
		out = append(out, "edit "+filepath.ToSlash(filepath.Join(dir, filepath.FromSlash(rel)))+" and delete its EDIT ME lines")
	}
	return append(out, "review the rest of "+filepath.ToSlash(dir)+" ("+traceimport.TranscriptFile+" has the whole session)",
		"Check the task on the benchmark's Build tab (Readiness check)", "Admit the task from the same tab")
}

func startsWithDotDot(p string) bool {
	return p == ".." || len(p) > 2 && p[:3] == "../"
}

// Reject answers POST /inbox/{id}/reject.
func (s *Service) Reject(_ context.Context, id string, req schemas.InboxRejectRequest) (*schemas.InboxActionResult, error) {
	it, err := s.Inbox.Reject(id, req.Reason, req.DuplicateOf)
	if err != nil {
		return nil, classify(err)
	}
	return &schemas.InboxActionResult{Item: *it}, nil
}

// Snooze answers POST /inbox/{id}/snooze.
func (s *Service) Snooze(_ context.Context, id string, req schemas.InboxSnoozeRequest) (*schemas.InboxActionResult, error) {
	it, err := s.Inbox.Snooze(id, req.Days)
	if err != nil {
		return nil, classify(err)
	}
	return &schemas.InboxActionResult{Item: *it}, nil
}

// Ingest answers POST /inbox/ingest once the gate has let the push in.
func (s *Service) Ingest(ctx context.Context, body []byte) (*schemas.InboxIngestResult, error) {
	g, hook := s.current()
	if g == nil || !g.Enabled() {
		return nil, ErrOff
	}
	return PushBody(ctx, s.Inbox, hook, body)
}

// PushBody decodes one pushed body strictly and ingests it into ib as src.
// The local webhook (Service.Ingest) and the hosted per-org ingest route
// (handlers/inboxingest.go) both write through it.
func PushBody(ctx context.Context, ib *Inbox, src *Source, body []byte) (*schemas.InboxIngestResult, error) {
	var req IngestRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return nil, fmt.Errorf("%w: parse push: %v", ErrInvalid, err)
	}
	res, err := ib.Ingest(ctx, src, req)
	if err != nil {
		return nil, classify(err)
	}
	return &schemas.InboxIngestResult{ItemID: res.ItemID, Created: res.Created, Occurrences: res.Occurrences, Skipped: res.Skipped}, nil
}

// classify marks an error that is not a missing item or a state conflict as
// a refused request.
func classify(err error) error {
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) || errors.Is(err, ErrInvalid) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrInvalid, err)
}

// now is exported for the handler's rate limiter.
func (s *Service) Now() time.Time { return s.Inbox.now() }

// capText cuts s to at most n bytes on a rune boundary.
func capText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
