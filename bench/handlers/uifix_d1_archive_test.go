package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/findings"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// archiveTestRouter is an org router with the archive area, the
// evaluation, comparison, run, run list, findings and leaderboard routes
// over one project root and the leaderboard test index.
func archiveTestRouter(t *testing.T, launcher Launcher) (*router.Router, string, *fakeIndex) {
	t.Helper()
	root := t.TempDir()
	idx := leaderboardIndex()
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Index: idx, Launcher: launcher}}
	r := router.New()
	for _, name := range []string{"archive", "evaluations", "findings"} {
		orgAreas[name](env, r)
	}
	NewExperimentsHandler(testMode, idx, nil, nil, nil, nil).WithArchiveRoot(root).RegisterRoutes(r)
	NewRunsHandler(testMode, idx).WithArchiveRoot(root).RegisterRoutes(r)
	NewRunListHandler(testMode, idx, nil).WithRoot(root).RegisterRoutes(r)
	return r, root, idx
}

func d1Decode[T any](t *testing.T, res *fasthttp.Response) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(res.Body(), &v); err != nil {
		t.Fatalf("decode %s: %v", res.Body(), err)
	}
	return v
}

func saveEval(t *testing.T, root, id, status string) {
	t.Helper()
	ev := &schemas.Evaluation{ID: id, Status: status, CreatedAt: time.Now(), Spec: schemas.EvaluationSpec{Name: id}}
	if status == schemas.EvaluationRunning { // a child the index does not hold keeps it running
		ev.Children = []schemas.EvaluationChild{{Benchmark: "b", ExperimentID: "e_elsewhere", Status: "running"}}
	}
	if err := evalstore.Open(root).Save(ev); err != nil {
		t.Fatal(err)
	}
}

// TestArchiveEvaluations: a finished evaluation archives and unarchives,
// a running one is refused, lists hide archived ones by default, include
// and only work with paging totals, the evaluation still opens by id, and
// a later save of an old copy does not undo the archive.
func TestArchiveEvaluations(t *testing.T) {
	r, root, _ := archiveTestRouter(t, nil)
	saveEval(t, root, "ev_done", schemas.EvaluationCancelled)
	saveEval(t, root, "ev_other", schemas.EvaluationCancelled)
	saveEval(t, root, "ev_busy", schemas.EvaluationRunning)
	stale, _ := evalstore.Open(root).Load("ev_done")

	if res := shareCall(r, "POST", APIPrefix+"/evaluations/ev_busy/archive", nil); res.StatusCode() != 409 || !strings.Contains(string(res.Body()), "cancel it or let it finish") {
		t.Fatalf("busy: %d %s", res.StatusCode(), res.Body())
	}
	res := shareCall(r, "POST", APIPrefix+"/evaluations/ev_done/archive", nil)
	got := d1Decode[ArchiveResult](t, res)
	if res.StatusCode() != 200 || !got.IsOn || got.ArchivedAt == nil || got.ArchivedBy == "" || got.Kind != "evaluation" {
		t.Fatalf("archive: %d %s", res.StatusCode(), res.Body())
	}
	first := *got.ArchivedAt
	// Archiving again keeps the first time.
	if again := d1Decode[ArchiveResult](t, shareCall(r, "POST", APIPrefix+"/evaluations/ev_done/archive", nil)); !again.ArchivedAt.Equal(first) {
		t.Fatalf("second archive moved the time: %v", again.ArchivedAt)
	}
	// A refresh that read the evaluation before it was archived saves it.
	if err := evalstore.Open(root).Save(stale); err != nil {
		t.Fatal(err)
	}
	ids := func(q string) []string {
		l := d1Decode[EvaluationPagedList](t, shareCall(r, "GET", APIPrefix+"/evaluations"+q, nil))
		var out []string
		for _, ev := range l.Evaluations {
			out = append(out, ev.ID)
		}
		return out
	}
	if got := ids(""); strings.Contains(strings.Join(got, ","), "ev_done") || len(got) != 2 {
		t.Fatalf("default list %v", got)
	}
	if got := ids("?archived=only"); len(got) != 1 || got[0] != "ev_done" {
		t.Fatalf("only %v", got)
	}
	if got := ids("?archived=include"); len(got) != 3 {
		t.Fatalf("include %v", got)
	}
	paged := d1Decode[EvaluationPagedList](t, shareCall(r, "GET", APIPrefix+"/evaluations?limit=1", nil))
	if paged.ListPage == nil || paged.Total != 2 {
		t.Fatalf("paged total %s", shareCall(r, "GET", APIPrefix+"/evaluations?limit=1", nil).Body())
	}
	if res := shareCall(r, "GET", APIPrefix+"/evaluations?archived=yes", nil); res.StatusCode() != 400 {
		t.Fatalf("bad archived: %d", res.StatusCode())
	}
	one := shareCall(r, "GET", APIPrefix+"/evaluations/ev_done", nil)
	if one.StatusCode() != 200 || !strings.Contains(string(one.Body()), `"archived_at"`) {
		t.Fatalf("get archived: %d %s", one.StatusCode(), one.Body())
	}
	res = shareCall(r, "POST", APIPrefix+"/evaluations/ev_done/unarchive", nil)
	if got := d1Decode[ArchiveResult](t, res); res.StatusCode() != 200 || got.IsOn || got.ArchivedAt != nil {
		t.Fatalf("unarchive: %d %s", res.StatusCode(), res.Body())
	}
	if got := ids(""); len(got) != 3 {
		t.Fatalf("after unarchive %v", got)
	}
	for _, id := range []string{"ev_missing", "nope"} {
		if res := shareCall(r, "POST", APIPrefix+"/evaluations/"+id+"/archive", nil); res.StatusCode() != 404 && res.StatusCode() != 400 {
			t.Fatalf("%s: %d", id, res.StatusCode())
		}
	}
}

// TestArchiveComparisonAndRun: the sidecar marks a comparison and a run;
// the sealed folder is not read or written; lists filter; the comparison
// and run still open by id; the leaderboard is the same before and after.
func TestArchiveComparisonAndRun(t *testing.T) {
	r, root, idx := archiveTestRouter(t, nil)
	_, before := getBoard(t, idx, "")
	runID := idx.runs[0].Run.RunID

	if res := shareCall(r, "POST", APIPrefix+"/experiments/"+testExp+"/archive", nil); res.StatusCode() != 200 {
		t.Fatalf("archive exp: %d %s", res.StatusCode(), res.Body())
	}
	if res := shareCall(r, "POST", APIPrefix+"/runs/"+runID+"/archive", nil); res.StatusCode() != 200 {
		t.Fatalf("archive run: %d %s", res.StatusCode(), res.Body())
	}
	if _, err := os.Stat(filepath.Join(root, ".heimdall", "archive.json")); err != nil {
		t.Fatalf("sidecar: %v", err)
	}
	if res := shareCall(r, "POST", APIPrefix+"/experiments/e_missing/archive", nil); res.StatusCode() != 404 {
		t.Fatalf("missing exp: %d", res.StatusCode())
	}
	if res := shareCall(r, "POST", APIPrefix+"/runs/r_missing/archive", nil); res.StatusCode() != 404 {
		t.Fatalf("missing run: %d", res.StatusCode())
	}

	exps := d1Decode[schemas.ExperimentList](t, shareCall(r, "GET", APIPrefix+"/experiments", nil))
	if len(exps.Experiments) != 0 {
		t.Fatalf("default list keeps the archived comparison: %+v", exps.Experiments)
	}
	only := d1Decode[ExperimentPagedList](t, shareCall(r, "GET", APIPrefix+"/experiments?limit=5&archived=only", nil))
	if only.Total != 1 || len(only.Experiments) != 1 || only.Experiments[0].ArchivedAt == nil {
		t.Fatalf("only: %+v", only)
	}
	st := d1Decode[schemas.ExperimentStatus](t, shareCall(r, "GET", APIPrefix+"/experiments/"+testExp, nil))
	if st.ArchivedAt == nil {
		t.Fatalf("get comparison lacks archived_at")
	}

	runs := d1Decode[RunList](t, shareCall(r, "GET", APIPrefix+"/runs", nil))
	if runs.Total != len(idx.runs)-1 {
		t.Fatalf("run list total %d of %d", runs.Total, len(idx.runs))
	}
	for _, row := range runs.Runs {
		if row.RunID == runID {
			t.Fatalf("archived run listed")
		}
	}
	runs = d1Decode[RunList](t, shareCall(r, "GET", APIPrefix+"/runs?archived=only", nil))
	if runs.Total != 1 || runs.Runs[0].RunID != runID || runs.Runs[0].ArchivedAt == nil {
		t.Fatalf("runs only %+v", runs)
	}
	if res := shareCall(r, "GET", APIPrefix+"/runs/"+runID, nil); res.StatusCode() != 200 || !strings.Contains(string(res.Body()), `"archived_at"`) {
		t.Fatalf("get run: %d", res.StatusCode())
	}

	// Archiving changes no computed number.
	_, after := getBoard(t, idx, "")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("leaderboard changed by archiving:\n%v\n%v", before, after)
	}

	if res := shareCall(r, "POST", APIPrefix+"/experiments/"+testExp+"/unarchive", nil); res.StatusCode() != 200 {
		t.Fatalf("unarchive: %d", res.StatusCode())
	}
	if exps := d1Decode[schemas.ExperimentList](t, shareCall(r, "GET", APIPrefix+"/experiments", nil)); len(exps.Experiments) != 1 || exps.Experiments[0].ArchivedAt != nil {
		t.Fatalf("after unarchive %+v", exps.Experiments)
	}
}

// TestArchiveRefusesRunningComparison: a comparison still queued or
// running is refused with the reason.
func TestArchiveRefusesRunningComparison(t *testing.T) {
	r, _, _ := archiveTestRouter(t, &activeOp{})
	res := shareCall(r, "POST", APIPrefix+"/experiments/"+testExp+"/archive", nil)
	if res.StatusCode() != 409 || !strings.Contains(string(res.Body()), "still queued or running") {
		t.Fatalf("running: %d %s", res.StatusCode(), res.Body())
	}
}

// TestArchiveFindings: archiving keeps the state and updated_at; the list
// and its counts leave it out by default.
func TestArchiveFindings(t *testing.T) {
	r, root, _ := archiveTestRouter(t, nil)
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	var ids []string
	if err := findings.Open(root).Update(func(all []findings.Finding) ([]findings.Finding, error) {
		for _, k := range []string{"cluster:a", "cluster:b"} {
			f := findings.Finding{ID: findings.IDOf(k), Key: k, Title: k, Source: "cluster", Origin: "rule", State: findings.StateOpen, CreatedAt: now, UpdatedAt: now}
			ids = append(ids, f.ID)
			all = append(all, f)
		}
		return all, nil
	}); err != nil {
		t.Fatal(err)
	}
	res := shareCall(r, "POST", APIPrefix+"/findings/"+ids[0]+"/archive", nil)
	if res.StatusCode() != 200 {
		t.Fatalf("archive: %d %s", res.StatusCode(), res.Body())
	}
	f, _ := findings.Open(root).Get(ids[0])
	if f.State != findings.StateOpen || !f.UpdatedAt.Equal(now) || f.ArchivedAt == nil {
		t.Fatalf("stored %+v", f)
	}
	l := d1Decode[FindingsList](t, shareCall(r, "GET", APIPrefix+"/findings", nil))
	if len(l.Findings) != 1 || l.Findings[0].ID != ids[1] || l.Counts[findings.StateOpen] != 1 {
		t.Fatalf("list %+v", l)
	}
	l = d1Decode[FindingsList](t, shareCall(r, "GET", APIPrefix+"/findings?archived=include", nil))
	if len(l.Findings) != 2 || l.Counts[findings.StateOpen] != 2 {
		t.Fatalf("include %+v", l)
	}
	if res := shareCall(r, "POST", APIPrefix+"/findings/fnd-000000000000/archive", nil); res.StatusCode() != 404 {
		t.Fatalf("missing: %d", res.StatusCode())
	}
}

// fakeImprove is an improvement source with fixed summaries.
type fakeImprove struct{ list []improve.Summary }

func (f *fakeImprove) List(context.Context) (*improve.List, error) {
	return &improve.List{Improvements: append([]improve.Summary(nil), f.list...)}, nil
}
func (f *fakeImprove) Get(_ context.Context, id string) (*improve.Detail, error) {
	for _, s := range f.list {
		if s.ID == id {
			return &improve.Detail{Summary: s}, nil
		}
	}
	return nil, improve.ErrNotFound
}
func (f *fakeImprove) Lineage(context.Context, string) (*improve.LineageView, error) { return nil, nil }
func (f *fakeImprove) Create(context.Context, *improve.Spec, string) (*improve.Summary, error) {
	return nil, nil
}
func (f *fakeImprove) Approve(context.Context, string, int, string, string) (*improve.Summary, error) {
	return nil, nil
}
func (f *fakeImprove) Stop(context.Context, string, string) (*improve.Summary, error) {
	return nil, nil
}

// TestArchiveImprovements: a finished improvement run archives into the
// sidecar, a running one is refused, the list filters, the detail shows it.
func TestArchiveImprovements(t *testing.T) {
	root := t.TempDir()
	done, busy := "imp_done", "imp_busy"
	src := &fakeImprove{list: []improve.Summary{{ID: done, Status: improve.StatusDone}, {ID: busy, Status: improve.StatusRunning, Running: true}}}
	r := router.New()
	NewImproveHandler(testMode, src, nil).WithArchiveRoot(root).RegisterRoutes(r)
	if !improve.ValidID(done) {
		t.Skip("improvement ids have another form")
	}
	if res := shareCall(r, "POST", APIPrefix+"/improvements/"+busy+"/archive", nil); res.StatusCode() != 409 || !strings.Contains(string(res.Body()), "stop it first") {
		t.Fatalf("busy: %d %s", res.StatusCode(), res.Body())
	}
	if res := shareCall(r, "POST", APIPrefix+"/improvements/"+done+"/archive", nil); res.StatusCode() != 200 {
		t.Fatalf("archive: %d %s", res.StatusCode(), res.Body())
	}
	l := d1Decode[improve.List](t, shareCall(r, "GET", APIPrefix+"/improvements", nil))
	if len(l.Improvements) != 1 || l.Improvements[0].ID != busy {
		t.Fatalf("list %+v", l)
	}
	l = d1Decode[improve.List](t, shareCall(r, "GET", APIPrefix+"/improvements?archived=only", nil))
	if len(l.Improvements) != 1 || l.Improvements[0].ArchivedAt == nil {
		t.Fatalf("only %+v", l)
	}
	if res := shareCall(r, "GET", APIPrefix+"/improvements/"+done, nil); !strings.Contains(string(res.Body()), `"archived_at"`) {
		t.Fatalf("detail %s", res.Body())
	}
	if res := shareCall(r, "POST", APIPrefix+"/improvements/"+done+"/unarchive", nil); res.StatusCode() != 200 {
		t.Fatalf("unarchive: %d", res.StatusCode())
	}
	if l := d1Decode[improve.List](t, shareCall(r, "GET", APIPrefix+"/improvements", nil)); len(l.Improvements) != 2 {
		t.Fatalf("after unarchive %+v", l)
	}
}

// TestArchiveSidecar: archive keeps the first mark, unarchive of an id
// that is not archived writes nothing, and dropArchived clears an erased
// comparison and its runs.
func TestArchiveSidecar(t *testing.T) {
	root := t.TempDir()
	if m, err := setArchive(root, archiveRuns, "r_1", false, "", time.Now()); err != nil || m.IsArchived() {
		t.Fatalf("unarchive of nothing: %+v %v", m, err)
	}
	if _, err := os.Stat(archivePath(root)); !os.IsNotExist(err) {
		t.Fatalf("unarchive of nothing wrote the file: %v", err)
	}
	t1 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	_, _ = setArchive(root, archiveExperiments, "e_1", true, "usr_a", t1)
	m, _ := setArchive(root, archiveExperiments, "e_1", true, "usr_b", t1.Add(time.Hour))
	if !m.ArchivedAt.Equal(t1) || m.ArchivedBy != "usr_a" {
		t.Fatalf("second archive changed the mark %+v", m)
	}
	_, _ = setArchive(root, archiveRuns, "r_1", true, "usr_a", t1)
	dropArchived(root, "e_1", []string{"r_1"})
	a, err := readArchive(root)
	if err != nil || a.mark(archiveExperiments, "e_1").IsArchived() || a.mark(archiveRuns, "r_1").IsArchived() {
		t.Fatalf("drop: %+v %v", a, err)
	}
}

// TestSharedPageHidesArchiveMark: archived_at and archived_by never reach
// a shared page part.
func TestSharedPageHidesArchiveMark(t *testing.T) {
	for _, part := range []string{"evaluation", "experiment", "run", "verdict"} {
		v := withoutNewSharedFields(part, map[string]any{"id": "x", "archived_at": "2026-11-01T00:00:00Z", "archived_by": "usr_a"}).(map[string]any)
		if _, ok := v["archived_at"]; ok {
			t.Fatalf("%s keeps archived_at", part)
		}
		if _, ok := v["archived_by"]; ok {
			t.Fatalf("%s keeps archived_by", part)
		}
	}
}

// TestArchiveRoleInHandler: the handler itself checks the role of every
// archive route, so a router without the policy table (as here) still
// refuses a viewer everywhere and a member on the admin routes, and writes
// nothing.
func TestArchiveRoleInHandler(t *testing.T) {
	r, root, idx := archiveTestRouter(t, nil)
	saveEval(t, root, "ev_done", schemas.EvaluationCancelled)
	runID := idx.runs[0].Run.RunID
	fid := findings.IDOf("cluster:role")
	if err := findings.Open(root).Update(func(all []findings.Finding) ([]findings.Finding, error) {
		return append(all, findings.Finding{ID: fid, Key: "cluster:role", Title: "x", Source: "cluster", Origin: "rule", State: findings.StateOpen}), nil
	}); err != nil {
		t.Fatal(err)
	}
	imp := router.New()
	NewImproveHandler(testMode, &fakeImprove{list: []improve.Summary{{ID: "imp_done", Status: improve.StatusDone}}}, nil).WithArchiveRoot(root).RegisterRoutes(imp)
	admin := []string{"/evaluations/ev_done", "/experiments/" + testExp, "/runs/" + runID}
	for _, verb := range []string{"/archive", "/unarchive"} {
		for _, p := range admin {
			for _, role := range []auth.Role{auth.RoleViewer, auth.RoleMember} {
				if res := shareCallAs(r, "POST", APIPrefix+p+verb, nil, role); res.StatusCode() != 403 {
					t.Fatalf("%s %s%s: %d %s", role, p, verb, res.StatusCode(), res.Body())
				}
			}
		}
		for _, role := range []auth.Role{auth.RoleViewer, auth.RoleMember} {
			if res := shareCallAs(imp, "POST", APIPrefix+"/improvements/imp_done"+verb, nil, role); res.StatusCode() != 403 {
				t.Fatalf("%s improvement%s: %d %s", role, verb, res.StatusCode(), res.Body())
			}
		}
		if res := shareCallAs(r, "POST", APIPrefix+"/findings/"+fid+verb, nil, auth.RoleViewer); res.StatusCode() != 403 {
			t.Fatalf("viewer finding%s: %d %s", verb, res.StatusCode(), res.Body())
		}
	}
	if _, err := os.Stat(archivePath(root)); !os.IsNotExist(err) {
		t.Fatalf("a refused archive wrote the sidecar: %v", err)
	}
	if ev, _ := evalstore.Open(root).Load("ev_done"); ev.IsArchived() {
		t.Fatal("a refused archive marked the evaluation")
	}
	if res := shareCallAs(r, "POST", APIPrefix+"/findings/"+fid+"/archive", nil, auth.RoleMember); res.StatusCode() != 200 {
		t.Fatalf("member finding: %d %s", res.StatusCode(), res.Body())
	}
	if res := shareCallAs(r, "POST", APIPrefix+"/experiments/"+testExp+"/archive", nil, auth.RoleAdmin); res.StatusCode() != 200 {
		t.Fatalf("admin comparison: %d %s", res.StatusCode(), res.Body())
	}
}

// TestArchiveSidecarBound: an archive that would grow the sidecar past the
// size the lists read is refused, and the marks already stored stay
// readable.
func TestArchiveSidecarBound(t *testing.T) {
	root := t.TempDir()
	var a archiveFile
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	add := func(from, to int) {
		for i := from; i < to; i++ {
			a.kind(archiveRuns)[fmt.Sprintf("r_%07d", i)] = schemas.Archived{ArchivedAt: &at, ArchivedBy: "usr_1"}
		}
	}
	add(0, 1000)
	one, _ := json.MarshalIndent(a, "", "  ")
	n := (archiveMaxBytes - 4096) / (len(one) / 1000) // just under the bound
	add(1000, n)
	data, _ := json.MarshalIndent(a, "", "  ")
	if len(data) > archiveMaxBytes-1 || len(data) < archiveMaxBytes-64<<10 {
		t.Fatalf("setup: %d bytes", len(data))
	}
	if err := os.MkdirAll(filepath.Dir(archivePath(root)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath(root), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := setArchive(root, archiveRuns, "r_"+strings.Repeat("x", 8000), true, "usr_1", at); err == nil {
		t.Fatal("an archive past the bound was written")
	}
	got, err := readArchive(root)
	if err != nil || len(got.Runs) != len(a.Runs) {
		t.Fatalf("stored marks: %d of %d, %v", len(got.Runs), len(a.Runs), err)
	}
}
