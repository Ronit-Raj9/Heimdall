// Weekly issue sync (W4-48): an org connects a repository once, and every
// week the merged fixes since the last sync become new tasks, within a weekly
// spending cap. The records live in issuesync/repos.json; each sync has a
// web schedule issue-sync-<id> in policies/schedules.yaml. A private
// repository is read with the org's GitHub connection on a worker; the token
// is never stored in a sync record, a schedule or a job parameter.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/datapolicy"
	"github.com/maximhq/bifrost/bench/issuebench/emit"
	"github.com/maximhq/bifrost/bench/issuebench/pipeline"
	"github.com/maximhq/bifrost/bench/issuebench/work"
	"github.com/maximhq/bifrost/bench/issuesync"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/valyala/fasthttp"
)

func init() {
	RegisterOrgArea("issuesync", registerIssueSync)
}

// IssueSyncRequest is the body of POST /issuesync and PUT /issuesync/{id}.
// Missing fields keep their value (PUT) or take the default (POST).
type IssueSyncRequest struct {
	Repo        string                `json:"repo,omitempty"`
	Benchmark   string                `json:"benchmark,omitempty"`
	Weekday     *int                  `json:"weekday,omitempty"`
	HourUTC     *int                  `json:"hour_utc,omitempty"`
	MaxIssues   *int                  `json:"max_issues,omitempty"`
	PerIssueUSD *float64              `json:"per_issue_usd,omitempty"`
	WeeklyUSD   *float64              `json:"weekly_usd,omitempty"`
	Model       *pipeline.ModelChoice `json:"model,omitempty"`
	Paused      *bool                 `json:"paused,omitempty"` // PUT only
}

// IssueSyncList is the answer of GET /issuesync.
type IssueSyncList struct {
	Repos []issuesync.Repo `json:"repos"`
}

// IssueSyncRunRequest is the body of POST /issuesync/{id}/run.
type IssueSyncRunRequest struct {
	Yes bool `json:"yes"`
}

// issueSyncPublic says whether a repository is readable without a token;
// tests replace it.
var issueSyncPublic = func(ctx context.Context, repo string) bool { return work.RepoPublic(ctx, nil, repo) }

// IssueSyncDeps builds what one sync of env's org needs.
func IssueSyncDeps(env OrgEnv) issuesync.Deps {
	root := env.Cfg.Root
	return issuesync.Deps{
		Store:   issuesync.Open(root),
		Manager: IssueBenchManager(root, func() pipeline.Stages { return issueBenchStagesOf(env) }),
		Tasks:   func(b string) (map[string]bool, error) { return benchmarkTaskIDs(root, b) },
		Public:  issueSyncPublic,
		HasGitHub: func(ctx context.Context) bool {
			if env.Services.Keys != nil {
				if t, _, err := auth.FindRepoToken(ctx, env.Services.Keys, env.Services.OrgID); err == nil && t != "" {
					return true
				}
			}
			return !env.Services.Hosted && strings.TrimSpace(os.Getenv("GITHUB_TOKEN")) != ""
		},
		FirstPrivate: func(r issuesync.Repo) error { return markPrivateSource(root, r) },
	}
}

func issueBenchStagesOf(env OrgEnv) pipeline.Stages {
	if IssueBenchStages == nil {
		return pipeline.Stages{}
	}
	return IssueBenchStages(env)
}

// benchmarkTaskIDs lists the task folders of benchmarks/<id>/tasks; a
// benchmark not made yet has none.
func benchmarkTaskIDs(root, id string) (map[string]bool, error) {
	out := map[string]bool{}
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return out, nil
	}
	ents, err := os.ReadDir(filepath.Join(root, string(projectfs.AreaBenchmarks), id, "tasks"))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		if e.IsDir() {
			out[e.Name()] = true
		}
	}
	return out, nil
}

// markPrivateSource limits a private repository's benchmark to the
// organization's members.
func markPrivateSource(root string, r issuesync.Repo) error {
	// Share links read this mark: nothing of the benchmark leaves the
	// organization through a link (shareprivate_w448.go).
	if err := issuesync.MarkPrivateSource(root, r.Benchmark, r.Repo, time.Now()); err != nil {
		return err
	}
	_, err := datapolicy.Open(root).Set(r.Benchmark, datapolicy.Policy{Access: datapolicy.Access{Mode: datapolicy.AccessOrg}}, "issue-sync", time.Now())
	return err
}

// issueSyncScheduleName is the web schedule of sync id.
func issueSyncScheduleName(id string) string { return schedule.IssueSyncPrefix + id }

// putIssueSyncSchedule writes (or with remove, deletes) the schedule of r.
func putIssueSyncSchedule(root string, r issuesync.Repo, remove bool) error {
	path := WebSchedulesPath(root)
	f, err := schedule.Load(path)
	if err != nil {
		return err
	}
	name := issueSyncScheduleName(r.ID)
	kept := f.Schedules[:0]
	for _, s := range f.Schedules {
		if s.Name != name {
			kept = append(kept, s)
		}
	}
	f.Schedules = kept
	if !remove {
		f.Schedules = append(f.Schedules, schedule.Schedule{Name: name, Job: schedule.JobIssueSync, Timezone: "UTC",
			Cron: fmt.Sprintf("0 %d * * %d", r.HourUTC, r.Weekday), Paused: r.Paused})
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return schedule.Save(path, f)
}

func registerIssueSync(env OrgEnv, r *router.Router) {
	h := &issueSyncHandler{env: env}
	r.GET(APIPrefix+"/issuesync", env.R(h.list))
	r.POST(APIPrefix+"/issuesync", env.W(h.create))
	r.PUT(APIPrefix+"/issuesync/{id}", env.W(h.change))
	r.DELETE(APIPrefix+"/issuesync/{id}", env.W(h.remove))
	r.POST(APIPrefix+"/issuesync/{id}/run", env.W(h.run))
}

type issueSyncHandler struct{ env OrgEnv }

func (h *issueSyncHandler) fail(ctx *fasthttp.RequestCtx, status int, typ, msg string) {
	lib.SendError(ctx, h.env.Mode, status, typ, msg)
}

func (h *issueSyncHandler) store() issuesync.Store { return issuesync.Open(h.env.Cfg.Root) }

func (h *issueSyncHandler) list(ctx *fasthttp.RequestCtx) {
	list, err := h.store().List()
	if err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, IssueSyncList{Repos: list})
}

func (h *issueSyncHandler) decode(ctx *fasthttp.RequestCtx, v any) bool {
	if err := decodeJSONStrict(ctx.PostBody(), v); err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "request body: "+err.Error())
		return false
	}
	return true
}

// apply copies the set fields of q into r.
func (q IssueSyncRequest) apply(r *issuesync.Repo) {
	if q.Benchmark != "" {
		r.Benchmark = q.Benchmark
	}
	if q.Weekday != nil {
		r.Weekday = *q.Weekday
	}
	if q.HourUTC != nil {
		r.HourUTC = *q.HourUTC
	}
	if q.MaxIssues != nil {
		r.MaxIssues = *q.MaxIssues
	}
	if q.PerIssueUSD != nil {
		r.PerIssueUSD = *q.PerIssueUSD
	}
	if q.WeeklyUSD != nil {
		r.WeeklyUSD = *q.WeeklyUSD
	}
	if q.Model != nil {
		r.Model = *q.Model
	}
}

func (h *issueSyncHandler) invalid(ctx *fasthttp.RequestCtx, err error) {
	var fe *issuesync.FieldError
	if errors.As(err, &fe) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, fe.Text)
		return
	}
	h.fail(ctx, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
}

func (h *issueSyncHandler) create(ctx *fasthttp.RequestCtx) {
	var q IssueSyncRequest
	if !h.decode(ctx, &q) {
		return
	}
	if q.Paused != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "a new weekly sync cannot start paused")
		return
	}
	r := issuesync.Repo{ID: issuesync.NewID(), Repo: strings.TrimSpace(q.Repo), Weekday: issuesync.DefaultWeekday, HourUTC: issuesync.DefaultHourUTC,
		CreatedBy: actor(ctx), CreatedAt: time.Now().UTC(), SeenPRs: []int{}, Runs: []issuesync.RunRef{}}
	q.apply(&r)
	r.Defaults()
	if r.Benchmark == "" {
		r.Benchmark = emit.SuiteID(r.Repo)
	}
	if err := r.Validate(); err != nil {
		h.invalid(ctx, err)
		return
	}
	err := h.store().Update(func(list []issuesync.Repo) ([]issuesync.Repo, error) {
		for _, x := range list {
			if strings.EqualFold(x.Repo, r.Repo) {
				return nil, &issuesync.FieldError{Field: "repo", Text: "This repository already has a weekly sync."}
			}
		}
		return append(list, r), nil
	})
	if err != nil {
		h.invalid(ctx, err)
		return
	}
	if err := putIssueSyncSchedule(h.env.Cfg.Root, r, false); err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "save the weekly schedule: "+err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, r)
}

func (h *issueSyncHandler) change(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	var q IssueSyncRequest
	if !h.decode(ctx, &q) {
		return
	}
	var out issuesync.Repo
	err := h.store().Update(func(list []issuesync.Repo) ([]issuesync.Repo, error) {
		for i := range list {
			if list[i].ID != id {
				continue
			}
			r := list[i]
			if q.Repo != "" && !strings.EqualFold(q.Repo, r.Repo) {
				return nil, &issuesync.FieldError{Field: "repo", Text: "The repository of a weekly sync cannot change: remove it and connect the other one."}
			}
			q.apply(&r)
			if q.Paused != nil {
				r.Paused = *q.Paused
				if !r.Paused {
					r.PausedReason = ""
				}
			}
			if err := r.Validate(); err != nil {
				return nil, err
			}
			list[i], out = r, r
			return list, nil
		}
		return nil, issuesync.ErrNotFound
	})
	if errors.Is(err, issuesync.ErrNotFound) {
		h.fail(ctx, fasthttp.StatusNotFound, lib.ErrTypeNotFound, err.Error())
		return
	}
	if err != nil {
		h.invalid(ctx, err)
		return
	}
	if err := putIssueSyncSchedule(h.env.Cfg.Root, out, false); err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "save the weekly schedule: "+err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (h *issueSyncHandler) remove(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	var gone issuesync.Repo
	err := h.store().Update(func(list []issuesync.Repo) ([]issuesync.Repo, error) {
		for i := range list {
			if list[i].ID == id {
				gone = list[i]
				return append(list[:i], list[i+1:]...), nil
			}
		}
		return nil, issuesync.ErrNotFound
	})
	if errors.Is(err, issuesync.ErrNotFound) {
		h.fail(ctx, fasthttp.StatusNotFound, lib.ErrTypeNotFound, err.Error())
		return
	}
	if err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
		return
	}
	if err := putIssueSyncSchedule(h.env.Cfg.Root, gone, true); err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "remove the weekly schedule: "+err.Error())
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

func (h *issueSyncHandler) run(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	var q IssueSyncRunRequest
	if !h.decode(ctx, &q) {
		return
	}
	if !q.Yes {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, `a sync spends money on your model key: send {"yes": true} to run it now`)
		return
	}
	work, cancel := workContext(ctx, 2*time.Minute)
	defer cancel()
	ref, err := issuesync.Run(work, IssueSyncDeps(h.env), id)
	switch {
	case errors.Is(err, issuesync.ErrBusy):
		h.fail(ctx, fasthttp.StatusConflict, lib.ErrTypeConflict, issuesync.BusyText)
	case errors.Is(err, issuesync.ErrNotFound):
		h.fail(ctx, fasthttp.StatusNotFound, lib.ErrTypeNotFound, err.Error())
	case err != nil:
		h.fail(ctx, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
	default:
		lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, ref)
	}
}
