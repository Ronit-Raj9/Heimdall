package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
)

type fakeScan struct{ cands []issuebench.Candidate }

func (f fakeScan) Scan(ctx context.Context, repo string, o ScanOptions) ([]issuebench.Candidate, issuebench.Funnel, error) {
	return f.cands, issuebench.Funnel{MergedPRs: 10, Linked: 4, Qualified: len(f.cands), Reasons: map[string]int{"no tests": 6}}, nil
}

type fakeBuild struct {
	mu    sync.Mutex
	calls int
	stop  map[int]issuebench.StopReason // by issue, first call only
}

func (f *fakeBuild) Build(ctx context.Context, c issuebench.Candidate, o BuildOptions, logf func(string, ...any)) (issuebench.BuildResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if s, ok := f.stop[c.Issue]; ok && len(o.Secrets) == 0 {
		return issuebench.BuildResult{Stop: s, Detail: "API_TOKEN", CostUSD: 0.25}, nil
	}
	return issuebench.BuildResult{Stop: issuebench.StopOK, CostUSD: 0.5, Env: &issuebench.EnvSpec{Repo: c.Repo, BaseImage: "python:3.12-slim"}}, nil
}

type fakeValidate struct{}

func (fakeValidate) Validate(ctx context.Context, c issuebench.Candidate, env issuebench.EnvSpec, logf func(string, ...any)) (issuebench.Validation, error) {
	v := issuebench.Validation{Candidate: c, PassToPass: []string{"t::ok"}, Checks: issuebench.LLMChecks{Clear: true}}
	if c.Issue != 3 {
		v.FailToPass = []string{"t::fixed"}
	}
	return v, nil
}

type fakeEmit struct{ got []issuebench.Validation }

func (f *fakeEmit) Emit(ctx context.Context, repo string, t []issuebench.Validation) (string, error) {
	f.got = t
	return "issues-" + "a-b", nil
}

func cand(issue int, month time.Month) issuebench.Candidate {
	return issuebench.Candidate{Repo: "a/b", Issue: issue, PR: issue + 100, IssueBody: "body", PRMerged: time.Date(2026, month, 2, 0, 0, 0, 0, time.UTC)}
}

func TestPipelineEndToEnd(t *testing.T) {
	b := &fakeBuild{stop: map[int]issuebench.StopReason{4: issuebench.StopNeedsSecret}}
	e := &fakeEmit{}
	m := NewManager(t.TempDir(), Stages{
		Scanner: fakeScan{cands: []issuebench.Candidate{cand(1, 3), cand(2, 3), cand(3, 4), cand(4, 5), {Issue: 9, Skip: "bot"}}},
		Builder: b, Validator: fakeValidate{}, Emitter: e,
	})
	ctx := context.Background()
	if _, err := m.Scan(ctx, "bad repo", ScanOptions{}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("bad repo: %v", err)
	}
	s, err := m.Scan(ctx, "a/b", ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Items) != 4 || s.Funnel.MergedPRs != 10 {
		t.Fatalf("items %d funnel %+v", len(s.Items), s.Funnel)
	}
	if _, err := m.Start(s.ID, false); !errors.Is(err, ErrNoConsent) {
		t.Fatalf("no consent: %v", err)
	}
	if _, err := m.Start(s.ID, true); err != nil {
		t.Fatal(err)
	}
	m.Wait()
	s, _ = m.Load(s.ID)
	want := map[int]string{1: StatusReady, 2: StatusReady, 3: StatusFailed, 4: StatusFailed}
	for _, it := range s.Items {
		if it.Status != want[it.Candidate.Issue] {
			t.Errorf("issue %d: %s (%s)", it.Candidate.Issue, it.Status, it.Reason)
		}
	}
	if b.calls != 3 { // version 2026-03 reused for issues 1 and 2
		t.Errorf("builds %d, want 3", b.calls)
	}
	if s.Status != RunDone || len(s.Log) == 0 {
		t.Errorf("status %s log %d", s.Status, len(s.Log))
	}
	it4 := s.Items[3]
	if it4.CostUSD != 0.25 || s.Items[0].CostUSD != 0.5 { // a failed build's cost stays on its item
		t.Errorf("item costs %v %v", it4.CostUSD, s.Items[0].CostUSD)
	}
	if it4.Fix != FixAddSecret || it4.Reason != "the tests need a secret: API_TOKEN" {
		t.Errorf("issue 4 fix %q reason %q", it4.Fix, it4.Reason)
	}
	if _, err := m.Retry(s.ID, 4, FixAddSecret, BuildOptions{}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("retry without secret: %v", err)
	}
	if _, err := m.Retry(s.ID, 4, FixAddSecret, BuildOptions{Secrets: []string{"API_TOKEN"}}); err != nil {
		t.Fatal(err)
	}
	m.Wait()
	s, _ = m.Load(s.ID)
	if s.Items[3].Status != StatusReady {
		t.Fatalf("after fix: %s %s", s.Items[3].Status, s.Items[3].Reason)
	}
	if _, err := m.Review(ctx, s.ID, 3, Review{Decision: "accept"}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("accept failed item: %v", err)
	}
	if _, err := m.Review(ctx, s.ID, 1, Review{Decision: "edit", Instruction: "clearer text"}); err != nil {
		t.Fatal(err)
	}
	s, err = m.Review(ctx, s.ID, 1, Review{Decision: "accept"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Benchmark != "issues-a-b" || len(e.got) != 1 || e.got[0].Candidate.IssueBody != "clearer text" {
		t.Fatalf("emit: bench %q got %+v", s.Benchmark, e.got)
	}
	if _, err := m.Review(ctx, s.ID, 2, Review{Decision: "reject"}); err != nil {
		t.Fatal(err)
	}
	runs, err := m.List()
	if err != nil || len(runs) != 1 || runs[0].Log != nil {
		t.Fatalf("list %v %+v", err, runs)
	}
}

func TestResumeSkipsFinishedItems(t *testing.T) {
	b := &fakeBuild{}
	m := NewManager(t.TempDir(), Stages{Scanner: fakeScan{cands: []issuebench.Candidate{cand(1, 3), cand(2, 4)}}, Builder: b, Validator: fakeValidate{}})
	s, _ := m.Scan(context.Background(), "a/b", ScanOptions{})
	_, _ = m.update(s.ID, func(s *State) error {
		s.Consent = true
		s.Items[0].Status = StatusReady
		return nil
	})
	if err := m.Run(context.Background(), s.ID, nil); err != nil {
		t.Fatal(err)
	}
	if b.calls != 1 {
		t.Fatalf("builds %d, want 1", b.calls)
	}
}

func TestMissingStagesSayWhy(t *testing.T) {
	m := NewManager(t.TempDir(), Stages{Scanner: fakeScan{cands: []issuebench.Candidate{cand(1, 3)}}})
	s, _ := m.Scan(context.Background(), "a/b", ScanOptions{})
	_, _ = m.Start(s.ID, true)
	m.Wait()
	s, _ = m.Load(s.ID)
	if s.Items[0].Status != StatusFailed || s.Items[0].Reason == "" {
		t.Fatalf("%+v", s.Items[0])
	}
	if _, err := m.Load("../x"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

type modelBuild struct {
	mu  sync.Mutex
	got []ModelChoice
}

func (f *modelBuild) Build(ctx context.Context, c issuebench.Candidate, o BuildOptions, logf func(string, ...any)) (issuebench.BuildResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, o.ModelChoice)
	return issuebench.BuildResult{Stop: issuebench.StopOK, Env: &issuebench.EnvSpec{Repo: c.Repo, BaseImage: "python:3.12-slim"}}, nil
}

type modelValidate struct {
	mu  sync.Mutex
	got []ModelChoice
}

func (f *modelValidate) Validate(ctx context.Context, c issuebench.Candidate, env issuebench.EnvSpec, logf func(string, ...any)) (issuebench.Validation, error) {
	f.mu.Lock()
	f.got = append(f.got, ModelFrom(ctx))
	f.mu.Unlock()
	return fakeValidate{}.Validate(ctx, c, env, logf)
}

func TestPipelineModelChoice(t *testing.T) {
	b, v := &modelBuild{}, &modelValidate{}
	m := NewManager(t.TempDir(), Stages{Scanner: fakeScan{cands: []issuebench.Candidate{cand(1, 3)}}, Builder: b, Validator: v, Emitter: &fakeEmit{}})
	s, err := m.Scan(context.Background(), "a/b", ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.StartWith(s.ID, true, ModelChoice{Provider: "openai"}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("half a choice: %v", err)
	}
	pick := ModelChoice{Provider: "anthropic", Model: "claude-x", KeyID: "k1"}
	if _, err := m.StartWith(s.ID, true, pick); err != nil {
		t.Fatal(err)
	}
	m.Wait()
	s, _ = m.Load(s.ID)
	if s.Model != pick || len(b.got) != 1 || b.got[0] != pick || len(v.got) != 1 || v.got[0] != pick {
		t.Fatalf("model not carried: state %+v build %+v validate %+v", s.Model, b.got, v.got)
	}
}

// noteBuild and noteValidate report their jobs through NoteJob, the way the
// hosted stage adapters do.
type noteBuild struct{ fail error }

func (f *noteBuild) Build(ctx context.Context, c issuebench.Candidate, o BuildOptions, logf func(string, ...any)) (issuebench.BuildResult, error) {
	issuebench.NoteJob(ctx, issuebench.StageBuild, "job-b")
	if f.fail != nil {
		return issuebench.BuildResult{}, f.fail
	}
	return issuebench.BuildResult{Stop: issuebench.StopOK, Env: &issuebench.EnvSpec{Repo: c.Repo, BaseImage: "python:3.12-slim"}}, nil
}

type noteValidate struct{ fail error }

func (f noteValidate) Validate(ctx context.Context, c issuebench.Candidate, env issuebench.EnvSpec, logf func(string, ...any)) (issuebench.Validation, error) {
	issuebench.NoteJob(ctx, issuebench.StageValidate, "job-v")
	if f.fail != nil {
		return issuebench.Validation{}, f.fail
	}
	issuebench.NoteJob(ctx, issuebench.StageChecks, "job-c")
	return issuebench.Validation{Candidate: c, FailToPass: []string{"t::fixed"}, Checks: issuebench.LLMChecks{Clear: true}}, nil
}

func TestPipelineNotesJobAndStage(t *testing.T) {
	m := NewManager(t.TempDir(), Stages{Scanner: fakeScan{cands: []issuebench.Candidate{cand(1, 3)}}, Builder: &noteBuild{}, Validator: noteValidate{}})
	s, _ := m.Scan(context.Background(), "a/b", ScanOptions{})
	if _, err := m.Start(s.ID, true); err != nil {
		t.Fatal(err)
	}
	m.Wait()
	s, _ = m.Load(s.ID)
	it := s.Items[0]
	if it.Status != StatusReady || it.JobID != "job-c" || it.Stage != issuebench.StageChecks {
		t.Fatalf("item %+v", it)
	}
	found := false
	for _, l := range s.Log {
		if l.Line == "issue #1: build runs as job job-b" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no job line in log %+v", s.Log)
	}
}

func TestPipelineNoWorkerOnline(t *testing.T) {
	noWorker := errors.New("No worker online: this job starts containers, so on the hosted site it runs on a worker")
	for _, tc := range []struct {
		name  string
		build error
		valid error
		stage string
	}{
		{"build", fmt.Errorf("queue job: %w", noWorker), nil, issuebench.StageBuild},
		{"validate", nil, fmt.Errorf("queue job: %w", noWorker), issuebench.StageValidate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &noteBuild{fail: tc.build}
			v := &noteValidate{fail: tc.valid}
			m := NewManager(t.TempDir(), Stages{Scanner: fakeScan{cands: []issuebench.Candidate{cand(1, 3)}}, Builder: b, Validator: v})
			s, _ := m.Scan(context.Background(), "a/b", ScanOptions{})
			if _, err := m.Start(s.ID, true); err != nil {
				t.Fatal(err)
			}
			m.Wait()
			s, _ = m.Load(s.ID)
			it := s.Items[0]
			if it.Status != StatusFailed || it.Fix != FixOpenWorkers || it.Reason != noWorker.Error() || it.Stage != tc.stage {
				t.Fatalf("item %+v", it)
			}
			b.fail, v.fail = nil, nil
			if _, err := m.Retry(s.ID, 1, FixOpenWorkers, BuildOptions{}); err != nil {
				t.Fatal(err)
			}
			m.Wait()
			s, _ = m.Load(s.ID)
			if it := s.Items[0]; it.Status != StatusReady || it.Fix != "" {
				t.Fatalf("after retry %+v", it)
			}
		})
	}
}

func TestFailReason(t *testing.T) {
	if r, f := failReason("build failed: ", errors.New("boom")); r != "build failed: boom" || f != FixRetry {
		t.Fatalf("%q %q", r, f)
	}
	if r, f := failReason("build failed: ", errors.New("run job: No worker online: open workers")); r != "No worker online: open workers" || f != FixOpenWorkers {
		t.Fatalf("%q %q", r, f)
	}
}
