package traceinbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

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
	gate  *Gate
	hook  *Source
}

// NewService wraps an inbox for the API. The webhook gate is built from the
// configured webhook source and its token variable; without both the
// webhook stays off.
func NewService(ib *Inbox) *Service {
	s := &Service{Inbox: ib}
	if wh, ok := ib.Config.Webhook(); ok {
		s.hook = wh
		s.gate = NewGate(ib.getenv(wh.TokenEnv), wh.RatePerMinute, wh.MaxBytes)
	}
	return s
}

// Gate returns the webhook gate, or nil when the webhook is off.
func (s *Service) Gate() *Gate {
	if s.gate == nil || !s.gate.Enabled() {
		return nil
	}
	return s.gate
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
	for _, src := range s.Inbox.Config.Sources {
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
	return &schemas.InboxItemDetail{Item: *it, Preview: string(preview)}, nil
}

// Accept answers POST /inbox/{id}/accept.
func (s *Service) Accept(ctx context.Context, id string, req schemas.InboxAcceptRequest) (*schemas.InboxAcceptResult, error) {
	if req.Suite == "" {
		return nil, fmt.Errorf("%w: suite is required", ErrInvalid)
	}
	res, err := s.Inbox.Accept(ctx, id, AcceptOptions{Suite: req.Suite, Kind: req.Kind, TaskID: req.TaskID})
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	d := schemas.InboxDraft{Task: res.Task, Dir: res.Item.Task.Dir, Template: res.Draft.Template, EditMe: res.Draft.EditMe,
		Next: AcceptSteps(res, s.Inbox.ProjectRoot), Warnings: res.Draft.Warnings}
	if d.EditMe == nil {
		d.EditMe = []string{}
	}
	return &schemas.InboxAcceptResult{Item: res.Item, Draft: d}, nil
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
		"bifrost-bench task validate "+res.Task, "bifrost-bench task admit "+res.Task)
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
	if s.Gate() == nil {
		return nil, ErrOff
	}
	var req IngestRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return nil, fmt.Errorf("%w: parse push: %v", ErrInvalid, err)
	}
	res, err := s.Inbox.Ingest(ctx, s.hook, req)
	if err != nil {
		return nil, classify(err)
	}
	return &schemas.InboxIngestResult{ItemID: res.ItemID, Created: res.Created, Occurrences: res.Occurrences}, nil
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
