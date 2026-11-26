package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/agentversions"
	"github.com/maximhq/bifrost/bench/clusters"
	"github.com/maximhq/bifrost/bench/findings"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/runctl"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// Findings lifecycle and targeted rerun (wave 4 lane I30, design W4-43):
// GET /findings, GET /findings/{id}, POST /findings and the expected,
// reopen, rerun and check actions. Findings live in the org project's
// findings/ folder (bench/findings). A rerun acts only on an experiment of
// the caller's organization: it is looked up in the org's own index.

func init() {
	RegisterOrgArea("findings", registerFindings)
}

// Rerun arm ids of a findings rerun.
const (
	findingsOldArm   = "old"
	findingsFixedArm = "fixed"
)

const (
	findingsSameConfig   = "The agent has not changed since this failure. Change it first, then check the fix."
	findingsRebuiltNote  = "The old version was rebuilt from the settings saved with the first run; files it reads, such as skills, are today's."
	findingsNoTasks      = "None of this finding's tasks ran in that experiment. Pick the experiment the failure came from."
	findingsNoRerun      = "This finding has no rerun to check yet. Start one with Check the fix."
	findingsConfirm      = "Confirm the rerun first: it starts new runs."
	findingsBadTrials    = "trials must be between 1 and 5"
	findingsUnknownState = "state must be open, expected, fixed or reopened"
)

// FindingsList is the answer of GET /findings.
type FindingsList struct {
	Findings []findings.Finding `json:"findings"`
	Counts   map[string]int     `json:"counts"`
}

// FindingAddRequest is the body of POST /findings.
type FindingAddRequest struct {
	Source string `json:"source"` // inbox | cluster | investigation | check
	Ref    string `json:"ref"`
}

// FindingReasonRequest is the body of the expected and reopen actions.
type FindingReasonRequest struct {
	Reason string `json:"reason"`
}

// FindingRerunRequest is the body of POST /findings/{id}/rerun.
type FindingRerunRequest struct {
	ExpID  string `json:"exp_id"`
	Arm    string `json:"arm"`
	Trials int    `json:"trials,omitempty"`
	Yes    bool   `json:"yes"`
}

// FindingRerunResult is the answer of POST /findings/{id}/rerun.
type FindingRerunResult struct {
	Rerun   *RerunFailuresResult `json:"rerun"`
	Finding *findings.Finding    `json:"finding"`
	Note    string               `json:"note,omitempty"`
}

type findingsHandler struct {
	env OrgEnv
	now func() time.Time
}

func registerFindings(env OrgEnv, r *router.Router) {
	h := &findingsHandler{env: env, now: time.Now}
	r.GET(APIPrefix+"/findings", env.R(h.list))
	r.GET(APIPrefix+"/findings/{id}", env.R(h.get))
	r.POST(APIPrefix+"/findings", env.W(h.add))
	r.POST(APIPrefix+"/findings/{id}/expected", env.W(h.expected))
	r.POST(APIPrefix+"/findings/{id}/reopen", env.W(h.reopen))
	r.POST(APIPrefix+"/findings/{id}/rerun", env.W(h.rerun))
	r.POST(APIPrefix+"/findings/{id}/check", env.W(h.check))
}

func (h *findingsHandler) store() findings.Store {
	root := ""
	if h.env.Cfg != nil {
		root = h.env.Cfg.Root
	}
	return findings.Open(root)
}

func (h *findingsHandler) path(rel string) string {
	if h.env.Cfg == nil {
		return rel
	}
	return h.env.Cfg.Path(rel)
}

func (h *findingsHandler) bad(ctx *fasthttp.RequestCtx, msg string) {
	lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
}

func (h *findingsHandler) sendErr(ctx *fasthttp.RequestCtx, id string, err error) {
	switch {
	case errors.Is(err, findings.ErrNotFound):
		sendNotFound(ctx, h.env.Mode, "finding", id)
	case errors.Is(err, findings.ErrInvalid):
		h.bad(ctx, strings.TrimPrefix(err.Error(), findings.ErrInvalid.Error()+": "))
	default:
		sendDepError(ctx, h.env.Mode, "findings", err)
	}
}

func (h *findingsHandler) findingID(ctx *fasthttp.RequestCtx) (string, bool) {
	id := pathParam(ctx, "id")
	if !findings.ValidID(id) {
		sendNotFound(ctx, h.env.Mode, "finding", id)
		return "", false
	}
	return id, true
}

// rowsWalk reads every sealed, not superseded run of the org's index once
// per request, keyed by "suite/task". Synthetic experiments are skipped
// unless keep says otherwise.
type rowsWalk struct {
	byTask map[string][]findings.RunRow
	byExp  map[string][]findings.RunRow
}

func (h *findingsHandler) walk() *rowsWalk {
	w := &rowsWalk{byTask: map[string][]findings.RunRow{}, byExp: map[string][]findings.RunRow{}}
	idx := h.env.Services.Index
	if idx == nil {
		return w
	}
	for _, exp := range idx.Experiments() {
		m, ok := idx.Manifest(exp)
		if !ok || m == nil {
			continue
		}
		synthetic := findingsSynthetic(m)
		for _, rv := range idx.Runs(exp) {
			if !rv.Sealed || rv.Superseded {
				continue
			}
			row := findings.RunRow{Experiment: exp, ExperimentCreated: m.CreatedAt, RunID: rv.Run.RunID,
				Suite: suiteName(rv.Slot.Suite), Task: rv.Slot.Task, Arm: rv.Slot.Arm, Passed: passedOf(rv.Run.Outcome)}
			if a, ok := m.Arms[rv.Slot.Arm]; ok {
				row.Agent, row.ConfigHash = a.Config.ID, a.ConfigHash
			}
			w.byExp[exp] = append(w.byExp[exp], row)
			if !synthetic[rv.Slot.Arm] {
				k := row.Suite + "/" + row.Task
				w.byTask[k] = append(w.byTask[k], row)
			}
		}
	}
	return w
}

// findingsSynthetic marks the arms whose runs are synthetic, by the
// manifest part of the results index's rule (resultsindex/build.go
// classify): a smoke or scripted experiment, or a scripted agent.
func findingsSynthetic(m *schemas.Manifest) map[string]bool {
	out := map[string]bool{}
	for id, a := range m.Arms {
		out[id] = m.Experiment.NoPaidTraffic() || a.Config.Adapter == "scripted" || a.Config.Scripted != nil
	}
	return out
}

// passedOf is true for pass, false for the outcomes that score 0, nil for
// the infrastructure ones that do not score.
func passedOf(o schemas.Outcome) *bool {
	y, ok := o.Score()
	if !ok {
		return nil
	}
	p := y == 1
	return &p
}

func suiteName(s string) string {
	name, _, _ := strings.Cut(s, "@")
	return name
}

func (h *findingsHandler) inboxItems() map[string]schemas.InboxItem {
	items, err := traceinbox.NewFileStore(h.path(traceinbox.Dir)).Items()
	out := map[string]schemas.InboxItem{}
	if err != nil {
		return out
	}
	for _, it := range items {
		out[it.ID] = it
	}
	return out
}

func (h *findingsHandler) refresh(w *rowsWalk) error {
	var items map[string]schemas.InboxItem
	_, err := findings.Refresh(h.store(),
		func(task string) ([]findings.RunRow, error) { return w.byTask[task], nil },
		func(id string) (*schemas.InboxItem, error) {
			if items == nil {
				items = h.inboxItems()
			}
			if it, ok := items[id]; ok {
				return &it, nil
			}
			return nil, nil
		}, h.now())
	return err
}

// list handles GET /findings?state=: Refresh first, newest change first.
func (h *findingsHandler) list(ctx *fasthttp.RequestCtx) {
	state := string(ctx.QueryArgs().Peek("state"))
	if state != "" && !slices.Contains([]string{findings.StateOpen, findings.StateExpected, findings.StateFixed, findings.StateReopened}, state) {
		h.bad(ctx, findingsUnknownState)
		return
	}
	if err := h.refresh(h.walk()); err != nil {
		h.sendErr(ctx, "", err)
		return
	}
	all, err := h.store().List()
	if err != nil {
		h.sendErr(ctx, "", err)
		return
	}
	out := FindingsList{Findings: []findings.Finding{}, Counts: map[string]int{}}
	for _, f := range all {
		out.Counts[f.State]++
		if state == "" || f.State == state {
			out.Findings = append(out.Findings, f)
		}
	}
	slices.SortStableFunc(out.Findings, func(a, b findings.Finding) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (h *findingsHandler) get(ctx *fasthttp.RequestCtx) {
	id, ok := h.findingID(ctx)
	if !ok {
		return
	}
	f, err := h.store().Get(id)
	if err != nil {
		h.sendErr(ctx, id, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, f)
}

func (h *findingsHandler) by(ctx *fasthttp.RequestCtx) string {
	if n := actorName(ctx); n != "" {
		return n
	}
	return "Someone"
}

func (h *findingsHandler) decode(ctx *fasthttp.RequestCtx, v any) bool {
	if err := decodeJSONStrict(ctx.PostBody(), v); err != nil {
		h.bad(ctx, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// add handles POST /findings: 201 for a new finding, 200 for an existing
// one with the same key.
func (h *findingsHandler) add(ctx *fasthttp.RequestCtx) {
	var req FindingAddRequest
	if !h.decode(ctx, &req) {
		return
	}
	var f *findings.Finding
	var status int
	var msg string
	switch req.Source {
	case "inbox":
		f, status, msg = h.fromInbox(req.Ref)
	case "cluster":
		f, status, msg = h.fromCluster(ctx, req.Ref)
	case "investigation":
		f, status, msg = h.fromInvestigation(req.Ref)
	case "check":
		f, status, msg = h.fromCheck(req.Ref)
	default:
		h.bad(ctx, "source must be inbox, cluster, investigation or check")
		return
	}
	if f == nil {
		lib.SendError(ctx, h.env.Mode, status, errTypeOf(status), msg)
		return
	}
	got, created, err := findings.Add(h.store(), *f, h.by(ctx), h.now())
	if err != nil {
		h.sendErr(ctx, "", err)
		return
	}
	code := fasthttp.StatusOK
	if created {
		code = fasthttp.StatusCreated
	}
	lib.SendJSON(ctx, h.env.Mode, code, got)
}

func errTypeOf(status int) string {
	switch status {
	case fasthttp.StatusNotFound:
		return lib.ErrTypeNotFound
	case fasthttp.StatusBadRequest:
		return lib.ErrTypeBadRequest
	case fasthttp.StatusConflict:
		return lib.ErrTypeConflict
	case fasthttp.StatusServiceUnavailable:
		return lib.ErrTypeUnavailable
	default:
		return lib.ErrTypeBadRequest
	}
}

func taskRefOf(s string) (findings.TaskRef, bool) {
	suite, task, ok := strings.Cut(s, "/")
	if !ok || suite == "" || task == "" {
		return findings.TaskRef{}, false
	}
	return findings.TaskRef{Suite: suiteName(suite), Task: task}, true
}

func addTask(ts []findings.TaskRef, t findings.TaskRef) []findings.TaskRef {
	if slices.Contains(ts, t) {
		return ts
	}
	return append(ts, t)
}

func (h *findingsHandler) fromInbox(ref string) (*findings.Finding, int, string) {
	if !traceinbox.ValidItemID(ref) {
		return nil, fasthttp.StatusBadRequest, "ref must be an inbox item id"
	}
	it, ok := h.inboxItems()[ref]
	if !ok {
		return nil, fasthttp.StatusNotFound, "inbox item not found: " + ref
	}
	f := &findings.Finding{Key: "inbox:" + ref, Title: it.Title, Source: "inbox", Origin: "verified"}
	if it.Task != nil {
		f.Tasks = addTask(f.Tasks, findings.TaskRef{Suite: suiteName(it.Task.Suite), Task: it.Task.ID})
	}
	return f, 0, ""
}

func (h *findingsHandler) fromCluster(ctx *fasthttp.RequestCtx, sig string) (*findings.Finding, int, string) {
	if !validID(sig) {
		return nil, fasthttp.StatusBadRequest, "ref must be a cluster signature"
	}
	src, ok := h.env.Services.Index.(clusters.Source)
	if h.env.Services.Index == nil || !ok {
		return nil, fasthttp.StatusServiceUnavailable, "verified run index is not available on this server"
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	rep, err := clusters.Build(work, src, clusters.Query{Now: h.now()})
	if err != nil {
		return nil, fasthttp.StatusServiceUnavailable, "clusters: " + err.Error()
	}
	d, err := clusters.Find(rep, sig)
	if err != nil {
		return nil, fasthttp.StatusNotFound, "cluster not found: " + sig
	}
	f := &findings.Finding{Key: "cluster:" + sig, Title: d.Cluster.Title, Source: "cluster", Origin: "verified", Runs: slices.Clone(d.Cluster.Examples)}
	for _, t := range d.Cluster.Tasks {
		if tr, ok := taskRefOf(t); ok {
			f.Tasks = addTask(f.Tasks, tr)
		}
	}
	return f, 0, ""
}

// investigationFile holds only the fields of investigations/<job>.json
// (investigator.Report, design 10.1) that a finding copies.
type investigationFile struct {
	ID    string `json:"id"`
	Items []struct {
		ID         string           `json:"id"`
		RunID      string           `json:"run_id"`
		Hypothesis string           `json:"hypothesis"`
		Quotes     []findings.Quote `json:"quotes"`
	} `json:"items"`
	Groups []struct {
		Title string   `json:"title"`
		Kind  string   `json:"kind"`
		Items []string `json:"items"`
		Runs  int      `json:"runs"`
		Tasks []string `json:"tasks"`
	} `json:"groups"`
}

var jobIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

const maxInvestigationBytes = 8 << 20

func (h *findingsHandler) fromInvestigation(ref string) (*findings.Finding, int, string) {
	job, gs, ok := strings.Cut(ref, ":")
	gi, err := strconv.Atoi(gs)
	if !ok || !jobIDPattern.MatchString(job) || err != nil || gi < 0 {
		return nil, fasthttp.StatusBadRequest, "ref must be <investigation id>:<group number>"
	}
	p := h.path(filepath.Join("investigations", job+".json"))
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxInvestigationBytes {
		return nil, fasthttp.StatusNotFound, "investigation not found: " + job
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fasthttp.StatusNotFound, "investigation not found: " + job
	}
	var rep investigationFile
	if err := json.Unmarshal(b, &rep); err != nil {
		return nil, fasthttp.StatusServiceUnavailable, "investigation " + job + " could not be read"
	}
	if gi >= len(rep.Groups) {
		return nil, fasthttp.StatusNotFound, fmt.Sprintf("investigation %s has no group %d", job, gi)
	}
	g := rep.Groups[gi]
	f := &findings.Finding{Key: fmt.Sprintf("investigation:%s:%d", job, gi), Title: g.Title, Source: "investigation", Origin: "model"}
	for _, t := range g.Tasks {
		if tr, ok := taskRefOf(t); ok {
			f.Tasks = addTask(f.Tasks, tr)
		}
	}
	for _, id := range g.Items {
		for _, it := range rep.Items {
			if it.ID != id {
				continue
			}
			if f.Hypothesis == "" {
				f.Hypothesis = it.Hypothesis
			}
			f.Quotes = append(f.Quotes, it.Quotes...)
			if it.RunID != "" && !slices.Contains(f.Runs, it.RunID) {
				f.Runs = append(f.Runs, it.RunID)
			}
		}
	}
	return f, 0, ""
}

func (h *findingsHandler) expected(ctx *fasthttp.RequestCtx) {
	h.reasonAction(ctx, findings.MarkExpected)
}

func (h *findingsHandler) reopen(ctx *fasthttp.RequestCtx) {
	h.reasonAction(ctx, findings.Reopen)
}

func (h *findingsHandler) reasonAction(ctx *fasthttp.RequestCtx, fn func(findings.Store, string, string, string, time.Time) (*findings.Finding, error)) {
	id, ok := h.findingID(ctx)
	if !ok {
		return
	}
	var req FindingReasonRequest
	if !h.decode(ctx, &req) {
		return
	}
	f, err := fn(h.store(), id, req.Reason, h.by(ctx), h.now())
	if err != nil {
		h.sendErr(ctx, id, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, f)
}

// rerun handles POST /findings/{id}/rerun: an exploratory child of the
// experiment the failure came from, on the finding's tasks only, with two
// arms: old (the failed arm's sealed config) and fixed (the arm's agent
// file today).
func (h *findingsHandler) rerun(ctx *fasthttp.RequestCtx) {
	id, ok := h.findingID(ctx)
	if !ok {
		return
	}
	var req FindingRerunRequest
	if !h.decode(ctx, &req) {
		return
	}
	if !req.Yes {
		h.bad(ctx, findingsConfirm)
		return
	}
	if req.Trials == 0 {
		req.Trials = 3
	}
	if req.Trials < 1 || req.Trials > 5 {
		h.bad(ctx, findingsBadTrials)
		return
	}
	if h.env.Services.Launcher == nil || h.env.Services.Index == nil {
		sendUnavailable(ctx, h.env.Mode, "experiment launcher")
		return
	}
	f, err := h.store().Get(id)
	if err != nil {
		h.sendErr(ctx, id, err)
		return
	}
	if !validID(req.ExpID) {
		sendNotFound(ctx, h.env.Mode, "experiment", req.ExpID)
		return
	}
	m, ok := h.env.Services.Index.Manifest(req.ExpID) // the org's own index: never another org's experiment
	if !ok || m == nil {
		sendNotFound(ctx, h.env.Mode, "experiment", req.ExpID)
		return
	}
	arm, okA := m.Arms[req.Arm]
	spec, okS := m.Experiment.Arms[req.Arm]
	if !okA || !okS {
		h.bad(ctx, "experiment "+m.ExpID+" has no arm "+req.Arm)
		return
	}
	agentPath := cmpOr(spec.Agent, m.Experiment.Base)
	if len(spec.Set) == 0 && agentPath != "" {
		if doc, err := os.ReadFile(h.path(agentPath)); err == nil {
			if cur, err := agentversions.ConfigHash(doc); err == nil && cur == arm.ConfigHash {
				lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, findingsSameConfig)
				return
			}
		}
	}
	now := h.now()
	exp, n, err := findingsRerunExperiment(m, h.env.Services.Index.Runs(m.ExpID), f.Tasks, req.Trials, now)
	if errors.Is(err, runctl.ErrConflict) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, findingsNoTasks)
		return
	}
	if err != nil {
		h.bad(ctx, plainErr(err))
		return
	}
	doc, fromVersion, err := h.oldDoc(arm)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "rebuild the old agent", err)
		return
	}
	rel := fmt.Sprintf("%s/reruns/%s-%d/old.yaml", findings.Dir, f.ID, now.Unix())
	if err := writeFindingFile(h.path(filepath.FromSlash(rel)), doc); err != nil {
		sendDepError(ctx, h.env.Mode, "write the old agent", err)
		return
	}
	old := schemas.ArmSpec{Label: "Old agent", Agent: rel}
	if fromVersion {
		old.Set = spec.Set
	}
	fixed := spec
	if fixed.Agent == "" {
		fixed.Agent = m.Experiment.Base
	}
	exp.Arms = map[string]schemas.ArmSpec{findingsOldArm: old, findingsFixedArm: fixed}
	work, cancel := workContext(ctx, evaluationLaunchTimeout)
	defer cancel()
	res, err := launchRerun(work, ctx, h.env.Services.Launcher, m, exp, n)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, statusOf(err), lib.ErrTypeBadRequest, err.Error())
		return
	}
	got, err := findings.Update(h.store(), f.ID, func(x *findings.Finding) error {
		if !slices.Contains(x.Reruns, res.ExperimentID) {
			x.Reruns = append(x.Reruns, res.ExperimentID)
		}
		x.History = append(x.History, findings.Event{At: now, By: h.by(ctx), From: x.State, To: x.State,
			Reason: "Started a rerun to check the fix.", Experiment: res.ExperimentID})
		x.UpdatedAt = now
		return nil
	})
	if err != nil {
		h.sendErr(ctx, f.ID, err)
		return
	}
	out := FindingRerunResult{Rerun: res, Finding: got}
	if !fromVersion {
		out.Note = findingsRebuiltNote
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusAccepted, plainServed(out))
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// oldDoc is the YAML of the failed arm's config: the stored agent version
// whose hash is the arm's config hash, else the sealed config itself.
func (h *findingsHandler) oldDoc(arm schemas.ArmManifest) ([]byte, bool, error) {
	root := ""
	if h.env.Cfg != nil {
		root = h.env.Cfg.Root
	}
	if agentversions.ValidID(arm.Config.ID) {
		if vs, err := agentversions.List(root, arm.Config.ID); err == nil {
			for _, v := range vs {
				if v.Hash != arm.ConfigHash {
					continue
				}
				if doc, _, err := agentversions.Read(root, arm.Config.ID, v.Version); err == nil {
					return doc, true, nil
				}
			}
		}
	}
	doc, err := yaml.Marshal(arm.Config)
	return doc, false, err
}

func writeFindingFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// findingsRerunExperiment builds the exploratory child of m the way
// RerunExperiment does, with every condition narrowed to the finding's
// tasks that ran in it, and trials per condition. The caller sets the arms.
func findingsRerunExperiment(m *schemas.Manifest, runs []RunView, tasks []findings.TaskRef, trials int, now time.Time) (*schemas.Experiment, int, error) {
	if m.Experiment.Matrix != nil {
		return nil, 0, fmt.Errorf("%w: experiment %s is a model matrix; rerun its failures from a new experiment", runctl.ErrInvalid, m.ExpID)
	}
	want := map[string]bool{}
	for _, t := range tasks {
		want[t.String()] = true
	}
	byCond := map[string][]string{}
	all := []string{}
	for _, rv := range runs {
		if !rv.Slot.Counted || !want[suiteName(rv.Slot.Suite)+"/"+rv.Slot.Task] {
			continue
		}
		if !slices.Contains(byCond[rv.Slot.Condition], rv.Slot.Task) {
			byCond[rv.Slot.Condition] = append(byCond[rv.Slot.Condition], rv.Slot.Task)
		}
		if !slices.Contains(all, rv.Slot.Task) {
			all = append(all, rv.Slot.Task)
		}
	}
	if len(all) == 0 {
		return nil, 0, fmt.Errorf("%w: no task of the finding ran in %s", runctl.ErrConflict, m.ExpID)
	}
	slices.Sort(all)
	e := m.Experiment // a copy; slices below are rebuilt, never shared
	e.ID = ""
	base := strings.TrimSuffix(e.Name, "-finding")
	e.Name = fmt.Sprintf("%s-finding-%s", base, now.UTC().Format("20060102-150405"))
	e.RerunOf = m.ExpID
	e.Extends, e.Trials = nil, nil
	e.EvaluationID = ""
	e.Compatibility = nil
	var conds []schemas.ConditionSpec
	for _, c := range m.Experiment.Conditions {
		ts := byCond[c.ID]
		if len(ts) == 0 {
			continue
		}
		slices.Sort(ts)
		c.Tasks = schemas.TaskSelector{IDs: slices.Clone(ts)}
		c.Trials = trials
		conds = append(conds, c)
	}
	if !slices.ContainsFunc(conds, func(c schemas.ConditionSpec) bool { return c.ID == e.Primary.Condition }) {
		for _, c := range m.Experiment.Conditions {
			if c.ID == e.Primary.Condition {
				c.Tasks = schemas.TaskSelector{IDs: all}
				c.Trials = trials
				conds = append([]schemas.ConditionSpec{c}, conds...)
			}
		}
	}
	if len(conds) == 0 {
		return nil, 0, fmt.Errorf("%w: the finding's runs are of no planned condition", runctl.ErrConflict)
	}
	e.Conditions = conds
	return &e, len(all), nil
}

// check handles POST /findings/{id}/check: judge the newest rerun.
func (h *findingsHandler) check(ctx *fasthttp.RequestCtx) {
	id, ok := h.findingID(ctx)
	if !ok {
		return
	}
	f, err := h.store().Get(id)
	if err != nil {
		h.sendErr(ctx, id, err)
		return
	}
	if len(f.Reruns) == 0 {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, findingsNoRerun)
		return
	}
	exp := f.Reruns[len(f.Reruns)-1]
	w := h.walk()
	got, err := findings.Judge(h.store(), id, exp, findingsOldArm, findingsFixedArm, w.byExp[exp], h.now())
	if err != nil {
		h.sendErr(ctx, id, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, got)
}
