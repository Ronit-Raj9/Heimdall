package issuesync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/emit"
	"github.com/maximhq/bifrost/bench/issuebench/pipeline"
)

type fakeScan struct{ cands []issuebench.Candidate }

func (f fakeScan) Scan(ctx context.Context, repo string, o pipeline.ScanOptions) ([]issuebench.Candidate, issuebench.Funnel, error) {
	return f.cands, issuebench.Funnel{Qualified: len(f.cands)}, nil
}

// countBuild counts builds; a build is what StartWith leads to.
type countBuild struct {
	mu    sync.Mutex
	calls int
}

func (b *countBuild) Build(ctx context.Context, c issuebench.Candidate, o pipeline.BuildOptions, logf func(string, ...any)) (issuebench.BuildResult, error) {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	return issuebench.BuildResult{Stop: issuebench.StopNeedsSecret, Detail: "X"}, nil
}

func cands(prs ...int) []issuebench.Candidate {
	out := []issuebench.Candidate{}
	for _, pr := range prs {
		out = append(out, issuebench.Candidate{Repo: "a/b", Issue: pr - 100, PR: pr, PRMerged: time.Date(2026, 11, pr%28+1, 0, 0, 0, 0, time.UTC)})
	}
	return out
}

func setup(t *testing.T, c []issuebench.Candidate) (Deps, *countBuild, string) {
	t.Helper()
	root := t.TempDir()
	b := &countBuild{}
	m := pipeline.NewManager(filepath.Join(root, "runs"), pipeline.Stages{Scanner: fakeScan{c}, Builder: b})
	st := Open(root)
	r := Repo{ID: "sync-0000aaaa", Repo: "a/b", Benchmark: "issues-a-b", MaxIssues: 10, PerIssueUSD: 1, WeeklyUSD: 10, SeenPRs: []int{101}}
	if err := st.Update(func(l []Repo) ([]Repo, error) { return append(l, r), nil }); err != nil {
		t.Fatal(err)
	}
	d := Deps{
		Store:     st,
		Manager:   m,
		Tasks:     func(string) (map[string]bool, error) { return map[string]bool{emit.InstanceID(c[1]): true}, nil },
		Public:    func(context.Context, string) bool { return true },
		HasGitHub: func(context.Context) bool { return true },
	}
	return d, b, r.ID
}

func TestRunSkipsSeenAndExisting(t *testing.T) {
	c := cands(101, 102, 103, 104)
	d, b, id := setup(t, c)
	ref, err := Run(context.Background(), d, id)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Status != RunStarted || ref.Started != 2 || ref.Skipped != 2 {
		t.Fatalf("ref = %+v, want started 2 skipped 2", ref)
	}
	d.Manager.Wait()
	st, _ := d.Manager.Load(ref.RunID)
	if len(st.Items) != 2 || st.Items[0].Candidate.PR != 103 || st.Funnel.Reasons["already_a_task"] != 2 {
		t.Fatalf("items %+v reasons %v", st.Items, st.Funnel.Reasons)
	}
	if st.CapUSD != 10 || st.Items[0].Options.BudgetUSD != 1 {
		t.Fatalf("cap %v budget %v", st.CapUSD, st.Items[0].Options.BudgetUSD)
	}
	if b.calls != 2 {
		t.Fatalf("builds = %d, want 2", b.calls)
	}
	r, _ := d.Store.Get(id)
	if !r.MergedAfter.Equal(c[3].PRMerged) && !r.MergedAfter.After(c[0].PRMerged) {
		t.Fatalf("cursor %v", r.MergedAfter)
	}
	if len(r.Runs) != 1 || len(r.SeenPRs) != 3 {
		t.Fatalf("runs %d seen %v", len(r.Runs), r.SeenPRs)
	}
}

func TestRunNothingNewIsFree(t *testing.T) {
	d, b, id := setup(t, cands(101, 102, 103))
	if _, err := Run(context.Background(), d, id); err != nil {
		t.Fatal(err)
	}
	d.Manager.Wait()
	before := b.calls
	ref, err := Run(context.Background(), d, id)
	if err != nil {
		t.Fatal(err)
	}
	d.Manager.Wait()
	if ref.Status != RunNothingNew || b.calls != before || ref.Started != 0 {
		t.Fatalf("second run %+v, builds %d then %d", ref, before, b.calls)
	}
}

func TestConcurrentRunRefused(t *testing.T) {
	d, _, id := setup(t, cands(101, 102, 103))
	running.Store(id, struct{}{})
	defer running.Delete(id)
	if _, err := Run(context.Background(), d, id); err != ErrBusy {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestPausedWhenGitHubGone(t *testing.T) {
	d, b, id := setup(t, cands(101, 102, 103))
	d.Public = func(context.Context, string) bool { return false }
	d.HasGitHub = func(context.Context) bool { return false }
	ref, err := Run(context.Background(), d, id)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := d.Store.Get(id)
	if ref.Status != RunRefused || ref.Reason != PrivateGoneReason || !r.Paused || r.PausedReason != PrivateGoneReason || b.calls != 0 {
		t.Fatalf("ref %+v repo paused %v %q", ref, r.Paused, r.PausedReason)
	}
	ref, _ = Run(context.Background(), d, id)
	if ref.Status != RunRefused || ref.Reason != PrivateGoneReason {
		t.Fatalf("second ref %+v", ref)
	}
}

func TestPublicRefusedWithoutGitHub(t *testing.T) {
	d, _, id := setup(t, cands(101, 102, 103))
	d.HasGitHub = func(context.Context) bool { return false }
	ref, _ := Run(context.Background(), d, id)
	r, _ := d.Store.Get(id)
	if ref.Status != RunRefused || ref.Reason != PublicNoGitHub || r.Paused {
		t.Fatalf("ref %+v paused %v", ref, r.Paused)
	}
}

func TestFirstPrivateRunMarksPrivate(t *testing.T) {
	d, _, id := setup(t, cands(101, 102, 103))
	d.Public = func(context.Context, string) bool { return false }
	calls := 0
	d.FirstPrivate = func(Repo) error { calls++; return nil }
	_, _ = Run(context.Background(), d, id)
	d.Manager.Wait()
	_, _ = Run(context.Background(), d, id)
	d.Manager.Wait()
	r, _ := d.Store.Get(id)
	if !r.Private || calls != 1 {
		t.Fatalf("private %v calls %d", r.Private, calls)
	}
}

func TestSyncRepoRecordHasNoToken(t *testing.T) {
	d, _, id := setup(t, cands(101, 102, 103))
	t.Setenv("GITHUB_TOKEN", "ghp_secretsecretsecret")
	if _, err := Run(context.Background(), d, id); err != nil {
		t.Fatal(err)
	}
	d.Manager.Wait()
	root := filepath.Dir(filepath.Dir(d.Store.(*fileStore).path))
	b, err := os.ReadFile(filepath.Join(root, File))
	if err != nil {
		t.Fatal(err)
	}
	list, _ := d.Store.List()
	j, _ := json.Marshal(list)
	for _, s := range []string{string(b), string(j)} {
		for _, bad := range []string{"ghp_", "github_pat_", "x-access-token"} {
			if strings.Contains(s, bad) {
				t.Fatalf("store holds %q", bad)
			}
		}
	}
}
