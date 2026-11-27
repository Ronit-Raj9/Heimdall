package handlers

import (
	"errors"
	"log/slog"
	"slices"
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

// Archive instead of delete (UI fix wave D1, decision 6):
//
//	POST /evaluations/{id}/archive, /unarchive   admin (as DELETE)
//	POST /experiments/{id}/archive, /unarchive   admin (as DELETE)
//	POST /runs/{id}/archive, /unarchive          admin (as deleting its comparison)
//	POST /improvements/{id}/archive, /unarchive  admin (as creating one)
//	POST /findings/{id}/archive, /unarchive      member (as its other actions)
//
// Archiving hides an object from its list (archived= shows it again) and
// deletes nothing. A queued or running object is refused with the reason.
// The dispatcher writes the audit row of every one of these writes (who,
// what, when). Marks of sealed comparisons and runs, and of improvement
// runs, live in the organization's sidecar (uifix_d1_archive_store.go);
// evaluations and findings carry them in their own records. Archived
// objects still count in every computed number (leaderboards, verdicts,
// usage, spend), still open by id, and keep their share links working:
// a shared page never shows archived_at (uifix_d1_sharedfields.go).

// ArchiveResult answers every archive and unarchive route: the object and
// its archive mark now (archived_at empty after unarchive).
type ArchiveResult struct {
	Kind string `json:"kind"` // evaluation | experiment | run | improvement | finding
	ID   string `json:"id"`
	IsOn bool   `json:"archived"`
	schemas.Archived
}

// archiveActor is who archives: the principal's actor id, "local" for a
// local caller with none.
func archiveActor(ctx *fasthttp.RequestCtx) string {
	if id := auth.PrincipalFrom(ctx).ActorID(); id != "" {
		return id
	}
	return "local"
}

// archiveRoleOK checks the role of an archive route inside the handler too,
// not only in the policy table: admin for evaluations, comparisons, runs and
// improvement runs, member for findings. A local caller is the one person
// of the bench and passes. Otherwise it answers 403 with the reason.
func archiveRoleOK(ctx *fasthttp.RequestCtx, mode schemas.Mode, role auth.Role) bool {
	if isLocalCaller(ctx) || auth.PrincipalFrom(ctx).Can(role) {
		return true
	}
	who := "an org owner or admin"
	if role == auth.RoleMember {
		who = "an org member, admin or owner"
	}
	lib.SendError(ctx, mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "Only "+who+" may archive or unarchive this.")
	return false
}

// archiveMarks is one list's archive filter and the sidecar marks of kind.
type archiveMarks struct {
	filter archiveFilter
	file   archiveFile
	kind   string
}

func (m archiveMarks) mark(id string) schemas.Archived { return m.file.mark(m.kind, id) }
func (m archiveMarks) keep(id string) bool             { return m.filter.keep(m.mark(id).IsArchived()) }

// loadArchiveMarks reads archived= (or answers a 400) and the sidecar of
// root. An unreadable sidecar marks nothing archived, so a list never fails
// because of it; it is logged.
func loadArchiveMarks(ctx *fasthttp.RequestCtx, mode schemas.Mode, root, kind string) (archiveMarks, bool) {
	f, ok := readArchiveFilter(ctx, mode)
	if !ok {
		return archiveMarks{}, false
	}
	a, err := readArchive(root)
	if err != nil {
		slog.Warn("archive sidecar unreadable; nothing is shown as archived", "error", err.Error())
	}
	return archiveMarks{filter: f, file: a, kind: kind}, true
}

// archiveMarkOf is the sidecar mark of one id; empty when unreadable.
func archiveMarkOf(root, kind, id string) schemas.Archived {
	a, _ := readArchive(root)
	return a.mark(kind, id)
}

// WithArchiveRoot sets the project root whose archive sidecar marks
// archived comparisons (UI fix wave D1).
func (h *ExperimentsHandler) WithArchiveRoot(root string) *ExperimentsHandler {
	h.archiveRoot = root
	return h
}

func (h *ExperimentsHandler) archiveMarks(ctx *fasthttp.RequestCtx) (archiveMarks, bool) {
	return loadArchiveMarks(ctx, h.mode, h.archiveRoot, archiveExperiments)
}

func (h *ExperimentsHandler) archiveMark(id string) schemas.Archived {
	return archiveMarkOf(h.archiveRoot, archiveExperiments, id)
}

// WithArchiveRoot sets the project root whose archive sidecar marks
// archived runs (UI fix wave D1).
func (h *RunsHandler) WithArchiveRoot(root string) *RunsHandler {
	h.archiveRoot = root
	return h
}

// WithArchiveRoot sets the project root whose archive sidecar marks
// archived improvement runs (UI fix wave D1).
func (h *ImproveHandler) WithArchiveRoot(root string) *ImproveHandler {
	h.archiveRoot = root
	return h
}

// archivedList keeps the rows archived= asks for and marks the archived
// ones. The source's list is not changed.
func (h *ImproveHandler) archivedList(ctx *fasthttp.RequestCtx, l *improve.List) (*improve.List, bool) {
	marks, ok := loadArchiveMarks(ctx, h.mode, h.archiveRoot, archiveImprovements)
	if !ok {
		return nil, false
	}
	out := &improve.List{Improvements: make([]improve.Summary, 0, len(l.Improvements))}
	for _, s := range l.Improvements {
		if !marks.keep(s.ID) {
			continue
		}
		s.Archived = marks.mark(s.ID)
		out.Improvements = append(out.Improvements, s)
	}
	return out, true
}

func (h *ImproveHandler) registerArchiveRoutes(r *router.Router, middlewares, writeMW []lib.Middleware) {
	r.POST(APIPrefix+"/improvements/{id}/archive", lib.ChainMiddlewares(func(ctx *fasthttp.RequestCtx) { h.archive(ctx, true) }, writeMW...))
	r.POST(APIPrefix+"/improvements/{id}/unarchive", lib.ChainMiddlewares(func(ctx *fasthttp.RequestCtx) { h.archive(ctx, false) }, writeMW...))
}

// improveBusy are the states in which an improvement run still works or
// waits to go on.
var improveBusy = []string{improve.StatusPlanned, improve.StatusRunning, improve.StatusAwaiting}

func (h *ImproveHandler) archive(ctx *fasthttp.RequestCtx, archive bool) {
	if !archiveRoleOK(ctx, h.mode, auth.RoleAdmin) {
		return
	}
	id, ok := h.id(ctx)
	if !ok || !h.ready(ctx) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	d, err := h.src.Get(work, id)
	if err != nil {
		h.sendErr(ctx, "improvement "+id, err)
		return
	}
	if archive && (d.Running || slices.Contains(improveBusy, d.Status)) {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "improvement run "+id+" is still "+improveStatusWords(d.Status)+"; stop it first, then archive it")
		return
	}
	if h.archiveRoot == "" {
		sendUnavailable(ctx, h.mode, "archiving (no project folder)")
		return
	}
	mark, err := setArchive(h.archiveRoot, archiveImprovements, id, archive, archiveActor(ctx), time.Now())
	if err != nil {
		sendDepError(ctx, h.mode, "archive improvement run", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, ArchiveResult{Kind: "improvement", ID: id, IsOn: mark.IsArchived(), Archived: mark})
}

func improveStatusWords(s string) string {
	switch s {
	case improve.StatusAwaiting:
		return "waiting for approval"
	case improve.StatusPlanned:
		return "queued"
	}
	return "running"
}

func init() {
	RegisterOrgArea("archive", func(env OrgEnv, r *router.Router) {
		h := &archiveHandler{env: env, now: time.Now}
		for _, k := range []struct {
			path string
			fn   func(*fasthttp.RequestCtx, bool)
		}{
			{"/evaluations/{id}", h.evaluation},
			{"/experiments/{id}", h.experiment},
			{"/runs/{id}", h.run},
			{"/findings/{id}", h.finding},
		} {
			fn := k.fn
			r.POST(APIPrefix+k.path+"/archive", env.W(func(ctx *fasthttp.RequestCtx) { fn(ctx, true) }))
			r.POST(APIPrefix+k.path+"/unarchive", env.W(func(ctx *fasthttp.RequestCtx) { fn(ctx, false) }))
		}
	})
}

type archiveHandler struct {
	env OrgEnv
	now func() time.Time
}

func (h *archiveHandler) data() *dataOpsHandler { return &dataOpsHandler{env: h.env} }

func (h *archiveHandler) send(ctx *fasthttp.RequestCtx, kind, id string, mark schemas.Archived) {
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, ArchiveResult{Kind: kind, ID: id, IsOn: mark.IsArchived(), Archived: mark})
}

func (h *archiveHandler) conflict(ctx *fasthttp.RequestCtx, msg string) {
	lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, msg)
}

// evaluation archives one evaluation of this org; refused unless it has
// finished (as deleting it is).
func (h *archiveHandler) evaluation(ctx *fasthttp.RequestCtx, archive bool) {
	if !archiveRoleOK(ctx, h.env.Mode, auth.RoleAdmin) {
		return
	}
	ev := &evaluationsHandler{env: h.env, now: h.now}
	e, ok := ev.load(ctx)
	if !ok {
		return
	}
	if archive {
		ev.refresh(e)
		if !terminalEvaluation(e.Status) {
			h.conflict(ctx, "evaluation "+e.ID+" is "+e.Status+"; cancel it or let it finish, then archive it")
			return
		}
	}
	saved, err := ev.store().SetArchived(e.ID, archive, archiveActor(ctx), h.now())
	if errors.Is(err, evalstore.ErrNotFound) {
		sendNotFound(ctx, h.env.Mode, "evaluation", e.ID)
		return
	}
	if err != nil {
		sendDepError(ctx, h.env.Mode, "archive evaluation", err)
		return
	}
	h.send(ctx, "evaluation", saved.ID, saved.Archived)
}

// experiment archives one comparison of this org's index; refused while it
// is queued or running. The sealed folder is never touched.
func (h *archiveHandler) experiment(ctx *fasthttp.RequestCtx, archive bool) {
	if !archiveRoleOK(ctx, h.env.Mode, auth.RoleAdmin) {
		return
	}
	id, _ := ctx.UserValue("id").(string)
	idx := h.env.Services.Index
	if idx == nil {
		sendUnavailable(ctx, h.env.Mode, "run index")
		return
	}
	if !validID(id) {
		sendNotFound(ctx, h.env.Mode, "experiment", id)
		return
	}
	if _, ok := idx.Manifest(id); !ok {
		sendNotFound(ctx, h.env.Mode, "experiment", id)
		return
	}
	if archive && h.data().active(id) {
		h.conflict(ctx, "comparison "+id+" is still queued or running; cancel it or let it finish, then archive it")
		return
	}
	h.sidecar(ctx, archiveExperiments, "experiment", id, archive)
}

// run archives one run of this org's index; refused while its comparison
// is queued or running.
func (h *archiveHandler) run(ctx *fasthttp.RequestCtx, archive bool) {
	if !archiveRoleOK(ctx, h.env.Mode, auth.RoleAdmin) {
		return
	}
	id, _ := ctx.UserValue("id").(string)
	idx := h.env.Services.Index
	if idx == nil {
		sendUnavailable(ctx, h.env.Mode, "run index")
		return
	}
	if !validID(id) {
		sendNotFound(ctx, h.env.Mode, "run", id)
		return
	}
	rv, ok := idx.Run(id)
	if !ok {
		sendNotFound(ctx, h.env.Mode, "run", id)
		return
	}
	if archive && (h.data().active(rv.Slot.ExpID) && !rv.Sealed) {
		h.conflict(ctx, "run "+id+" is still running; let it finish, then archive it")
		return
	}
	h.sidecar(ctx, archiveRuns, "run", id, archive)
}

func (h *archiveHandler) sidecar(ctx *fasthttp.RequestCtx, kind, label, id string, archive bool) {
	root := h.data().root()
	if root == "" {
		sendUnavailable(ctx, h.env.Mode, "archiving (no project folder)")
		return
	}
	mark, err := setArchive(root, kind, id, archive, archiveActor(ctx), h.now())
	if err != nil {
		sendDepError(ctx, h.env.Mode, "archive "+label, err)
		return
	}
	h.send(ctx, label, id, mark)
}

// finding archives one finding of this org. A finding never runs, so
// nothing is refused; its state does not change.
func (h *archiveHandler) finding(ctx *fasthttp.RequestCtx, archive bool) {
	if !archiveRoleOK(ctx, h.env.Mode, auth.RoleMember) {
		return
	}
	fh := &findingsHandler{env: h.env, now: h.now}
	id, ok := fh.findingID(ctx)
	if !ok {
		return
	}
	f, err := findings.SetArchived(fh.store(), id, archive, archiveActor(ctx), h.now())
	if err != nil {
		fh.sendErr(ctx, id, err)
		return
	}
	h.send(ctx, "finding", f.ID, f.Archived)
}

// runIDs are the run ids of a comparison in this org's index, read before
// it is erased so their archive marks can go with it.
func (h *dataOpsHandler) runIDs(expID string) []string {
	idx := h.env.Services.Index
	if idx == nil {
		return nil
	}
	var out []string
	for _, rv := range idx.Runs(expID) {
		out = append(out, rv.Run.RunID)
	}
	return out
}
