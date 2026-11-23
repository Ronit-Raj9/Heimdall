package handlers

// Dataset API (lane Y15): a question-and-answer benchmark's tasks managed
// from code. POST /suites/{id}/dataset/tasks adds or replaces task rows
// (input or chat messages, expected answers, rubric, metadata,
// attachments); GET lists them; DELETE removes one; every change is a
// numbered version (GET /suites/{id}/dataset/versions). The benchmark's
// personal data policy runs on every row before anything is written. The
// public API serves these as /api/v1/benchmarks/{id}/tasks and
// /api/v1/benchmarks/{id}/versions (apiv1.go).

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/datapolicy"
	"github.com/maximhq/bifrost/bench/dataset"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// Dataset write modes.
const (
	DatasetUpsert = "upsert" // a row whose id names a task made from rows replaces it
	DatasetAdd    = "add"    // every row is a new task; a taken id is a problem
)

// DatasetTasksRequest is the body of POST /suites/{id}/dataset/tasks.
type DatasetTasksRequest struct {
	Tasks []dataset.Row `json:"tasks"`
	// Mode is upsert (default) or add.
	Mode string `json:"mode,omitempty"`
	// Match is the answer_match mode of rows that name none.
	Match string `json:"match,omitempty"`
	// Source names where the rows came from, for provenance.
	Source string `json:"source,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// DatasetTasksResult answers POST /suites/{id}/dataset/tasks.
type DatasetTasksResult struct {
	Benchmark string             `json:"benchmark"`
	DryRun    bool               `json:"dry_run"`
	Version   *dataset.Version   `json:"version"` // nil when nothing changed or dry run
	Added     []string           `json:"added"`
	Updated   []string           `json:"updated"`
	Unchanged []string           `json:"unchanged"`
	Tasks     []scaffold.RowTask `json:"tasks"`
	Problems  []dataset.Problem  `json:"problems"`
	PII       *dataset.PIIReport `json:"pii"`
	Next      string             `json:"next"`
}

// DatasetTask is one task in GET /suites/{id}/dataset/tasks. Expected
// answers are never listed: they are the benchmark's hidden checks.
type DatasetTask struct {
	ID          string         `json:"id"`
	Title       string         `json:"title,omitempty"`
	HeldOut     bool           `json:"held_out,omitempty"`
	FromRows    bool           `json:"from_rows"` // made from rows (editable here); false for hand-written tasks
	RowSHA256   string         `json:"row_sha256,omitempty"`
	Chat        bool           `json:"chat,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Attachments []string       `json:"attachments,omitempty"`
}

// DatasetTaskList answers GET /suites/{id}/dataset/tasks.
type DatasetTaskList struct {
	Benchmark string        `json:"benchmark"`
	Version   int           `json:"version"`
	Total     int           `json:"total"`
	Offset    int           `json:"offset"`
	Tasks     []DatasetTask `json:"tasks"`
}

// DatasetVersionList answers GET /suites/{id}/dataset/versions.
type DatasetVersionList struct {
	Benchmark string               `json:"benchmark"`
	Versions  []DatasetVersionView `json:"versions"`
}

// DatasetVersionView is one version of a benchmark's rows: ActorName is how
// its actor is shown (actornames.go); an email actor is left out for a
// caller who is not an org admin.
type DatasetVersionView struct {
	dataset.Version
	ActorName string `json:"actor_name,omitempty"`
}

// DatasetVersionDetail answers GET /suites/{id}/dataset/versions/{n}.
type DatasetVersionDetail struct {
	Benchmark string            `json:"benchmark"`
	Version   dataset.Version   `json:"version"`
	Tasks     map[string]string `json:"tasks"` // task id -> row sha256
	// ActorName is how version.actor is shown; an email actor is left out
	// of version.actor for a caller who is not an org admin (fix wave 3,
	// group D review: every authoring path now records versions).
	ActorName string `json:"actor_name,omitempty"`
}

// DatasetTaskDeleted answers DELETE /suites/{id}/dataset/tasks/{task}.
type DatasetTaskDeleted struct {
	Benchmark string           `json:"benchmark"`
	Task      string           `json:"task"`
	Version   *dataset.Version `json:"version"`
}

type datasetAPI struct {
	build *suiteBuild
	env   OrgEnv
	now   func() time.Time
}

func init() {
	RegisterOrgArea("datasetapi", func(env OrgEnv, r *router.Router) {
		h := &datasetAPI{build: &suiteBuild{&suiteAuthoring{mode: env.Mode, projects: env.Services.Projects, suites: env.Services.Suites}}, env: env, now: time.Now}
		r.POST(APIPrefix+"/suites/{id}/dataset/tasks", env.U(refuseBuiltin(env.Mode, env.Services.Suites, h.postTasks)))
		r.GET(APIPrefix+"/suites/{id}/dataset/tasks", env.R(h.listTasks))
		r.DELETE(APIPrefix+"/suites/{id}/dataset/tasks/{task}", env.W(refuseBuiltin(env.Mode, env.Services.Suites, h.deleteTask)))
		r.GET(APIPrefix+"/suites/{id}/dataset/versions", env.R(h.versions))
		r.GET(APIPrefix+"/suites/{id}/dataset/versions/{n}", env.R(h.version))
	})
}

func (h *datasetAPI) root() string {
	if h.env.Services.Projects != nil {
		return h.env.Services.Projects.Root()
	}
	if h.env.Cfg != nil {
		return h.env.Cfg.Root
	}
	return ""
}

func (h *datasetAPI) bad(ctx *fasthttp.RequestCtx, msg string) {
	lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
}

// postTasks handles POST /suites/{id}/dataset/tasks.
func (h *datasetAPI) postTasks(ctx *fasthttp.RequestCtx) {
	b := h.build
	if !b.needProjects(ctx) {
		return
	}
	id, dir, ok := b.suiteDirParam(ctx)
	if !ok {
		return
	}
	var req DatasetTasksRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"tasks":[{"id"?,"input"|"messages","expected"?,"rubric"?,"metadata"?,"attachments"?}],"mode"?,"match"?,"source"?,"dry_run"?}: `+err.Error())
		return
	}
	if len(req.Tasks) == 0 {
		h.bad(ctx, "tasks is empty")
		return
	}
	if len(req.Tasks) > dataset.MaxTasksPerRequest {
		h.bad(ctx, "at most "+strconv.Itoa(dataset.MaxTasksPerRequest)+" tasks per request; send the rest in further requests")
		return
	}
	if req.Mode == "" {
		req.Mode = DatasetUpsert
	}
	if req.Mode != DatasetUpsert && req.Mode != DatasetAdd {
		h.bad(ctx, "mode: want upsert or add")
		return
	}
	if req.Match != "" && !slices.Contains([]string{"exact", "contains_all", "regex", "numeric", "choice"}, req.Match) {
		h.bad(ctx, "match: want exact, contains_all, regex, numeric or choice")
		return
	}
	if len(req.Source) > 200 || strings.ContainsAny(req.Source, "\r\n") {
		h.bad(ctx, "source: one line of at most 200 characters")
		return
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "dataset API"
	}
	out, status := h.writeRows(ctx, id, dir, req.Tasks, req.Mode, req.Match, source, req.DryRun)
	if out != nil {
		lib.SendJSON(ctx, h.env.Mode, status, out)
	}
}

var taskSlugRE = regexp.MustCompile(`[^a-z0-9]+`)

func normalTaskID(s string) string {
	return strings.Trim(taskSlugRE.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-")
}

// fromRows reports whether the task folder was written from rows (the web
// import, the dataset API or a platform import): only those are replaced
// or deleted here.
func fromRows(dir string) bool {
	raw, err := os.ReadFile(filepath.Join(dir, suite.TaskFile))
	if err != nil {
		return false
	}
	var t struct {
		Project string `yaml:"project"`
	}
	return yaml.Unmarshal(raw, &t) == nil && t.Project == "own-data"
}

// writeRows cleans rows with the benchmark's personal data policy and
// writes them as tasks. It answers errors itself and returns nil then.
func (h *datasetAPI) writeRows(ctx *fasthttp.RequestCtx, id, dir string, in []dataset.Row, mode, match, source string, dryRun bool) (*DatasetTasksResult, int) {
	b := h.build
	raw, s, ok := b.readAnswerSuite(ctx, id, dir)
	if !ok {
		return nil, 0
	}
	grader := scaffold.RowGraderExact
	if s.Grader == scaffold.RowGraderJudge {
		grader = scaffold.RowGraderJudge
	} else if s.Grader != scaffold.RowGraderExact {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest,
			"benchmark "+id+" uses grader "+s.Grader+"; pick the exact answer or the rubric judge grader first (PUT /api/v1/benchmarks/"+id+"/grader)")
		return nil, 0
	}
	root := h.root()
	red, _, err := datapolicy.Open(root).Redactor(id)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "read the benchmark's data policy: "+err.Error())
		return nil, 0
	}
	store := dataset.Open(root)
	unlock := store.Lock(id)
	defer unlock()

	out := &DatasetTasksResult{Benchmark: id, DryRun: dryRun, Added: []string{}, Updated: []string{}, Unchanged: []string{},
		Tasks: []scaffold.RowTask{}, Problems: []dataset.Problem{}, PII: dataset.NewPIIReport(red.Policy.Effective())}
	existing := map[string]bool{}
	for _, t := range s.Tasks {
		existing[t] = true
	}
	if ents, err := os.ReadDir(filepath.Join(dir, "tasks")); err == nil {
		for _, e := range ents {
			existing[e.Name()] = true
		}
	}
	cur, err := store.Snapshot(id, 0)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "read the version log: "+err.Error())
		return nil, 0
	}
	type pending struct {
		row    dataset.Row
		sha    string
		data   []byte
		update bool
	}
	byLine := map[int]*pending{}
	var rows []scaffold.Row
	claimed := map[string]bool{}
	for i, r := range in {
		idx := i + 1
		clean, msg := dataset.Clean(r, idx, red, out.PII)
		if msg != "" {
			out.Problems = append(out.Problems, dataset.Problem{Index: idx, ID: r.ID, Message: msg})
			continue
		}
		p := &pending{row: clean}
		if clean.ID != "" {
			tid := normalTaskID(clean.ID)
			if claimed[tid] {
				out.Problems = append(out.Problems, dataset.Problem{Index: idx, ID: clean.ID, Message: "the same id is twice in this request"})
				continue
			}
			claimed[tid] = true
			if existing[tid] && mode == DatasetUpsert {
				if !fromRows(filepath.Join(dir, "tasks", tid)) {
					out.Problems = append(out.Problems, dataset.Problem{Index: idx, ID: clean.ID, Message: "task " + tid + " was written by hand, not from rows; change it on the benchmark's Edit tab"})
					continue
				}
				p.update = true
			}
			clean.ID = tid
			p.row.ID = tid
		}
		sha, data, err := dataset.RowSHA(p.row)
		if err != nil {
			out.Problems = append(out.Problems, dataset.Problem{Index: idx, ID: r.ID, Message: err.Error()})
			continue
		}
		p.sha, p.data = sha, data
		if p.update && cur.Tasks[p.row.ID] == sha {
			out.Unchanged = append(out.Unchanged, p.row.ID)
			continue
		}
		byLine[idx] = p
		rows = append(rows, dataset.ScaffoldRow(p.row, idx))
	}
	updating := func(tid string) bool {
		for _, p := range byLine {
			if p.update && p.row.ID == tid {
				return true
			}
		}
		return false
	}
	tasks, more := scaffold.ImportRows(rows, scaffold.RowTaskOptions{Grader: grader, Match: match, Source: source,
		Taken: func(tid string) bool { return existing[tid] && !updating(tid) }})
	for _, p := range more {
		out.Problems = append(out.Problems, dataset.Problem{Index: p.Line, ID: in[p.Line-1].ID, Message: p.Message})
	}
	slices.SortStableFunc(out.Problems, func(a, b dataset.Problem) int { return a.Index - b.Index })
	out.Tasks = append(out.Tasks, tasks...)
	out.PII.Finish()
	if dryRun || len(tasks) == 0 {
		switch {
		case dryRun:
			out.Next = "Check the preview, then send the same request without dry_run."
		case len(out.Unchanged) > 0 && len(out.Problems) == 0:
			out.Next = "Nothing changed: every row matches the current version."
		default:
			out.Next = "No row could become a task; fix the rows listed and send them again."
		}
		return out, fasthttp.StatusOK
	}

	var written []string
	var swaps [][2]string // new dir, old dir kept until the suite file is written
	undo := func() {
		for _, d := range written {
			os.RemoveAll(d)
		}
		for _, sw := range swaps {
			_ = os.Rename(sw[1], sw[0])
		}
	}
	edited := raw
	changed := map[string]string{}
	for _, t := range tasks {
		p := byLine[t.Line]
		extra, err := dataset.ExtraFiles(p.row)
		if err != nil {
			undo()
			h.bad(ctx, "task "+t.ID+": "+err.Error())
			return nil, 0
		}
		files := append(slices.Clone(t.Files), extra...)
		taskDir := filepath.Join(dir, "tasks", t.ID)
		p.row.ID = t.ID
		sha, data, _ := dataset.RowSHA(p.row)
		if p.update {
			old := filepath.Join(dir, "tasks", ".old-"+t.ID+"-"+strconv.FormatInt(h.now().UnixNano(), 36))
			if err := os.Rename(taskDir, old); err != nil {
				undo()
				b.sendStoreError(ctx, "replace task "+t.ID, err)
				return nil, 0
			}
			swaps = append(swaps, [2]string{taskDir, old})
		}
		if err := writeTreeAtomic(taskDir, files); err != nil {
			undo()
			b.sendStoreError(ctx, "write task "+t.ID, err)
			return nil, 0
		}
		written = append(written, taskDir)
		if !p.update {
			next, _, err := scaffold.AddTaskID(edited, t.ID)
			if err != nil {
				undo()
				lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "add the tasks to suite.yaml: "+err.Error())
				return nil, 0
			}
			edited = next
			out.Added = append(out.Added, t.ID)
		} else {
			out.Updated = append(out.Updated, t.ID)
		}
		if err := store.PutRow(id, sha, data); err != nil {
			undo()
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "store row: "+err.Error())
			return nil, 0
		}
		changed[t.ID] = sha
	}
	if len(out.Added) > 0 {
		if _, err := h.env.Services.Projects.Write(ctx, projectfs.AreaBenchmarks, id+"/"+suite.SuiteFile, edited,
			projectfs.WriteOpts{Actor: actorName(ctx), Note: "dataset: add " + plural(len(out.Added), "task") + " from " + source, IfMatch: shaOf(raw)}); err != nil {
			undo()
			b.sendStoreError(ctx, "add the tasks to suite.yaml", err)
			return nil, 0
		}
		_ = os.Chmod(filepath.Join(dir, suite.SuiteFile), 0o644)
	}
	for _, sw := range swaps {
		os.RemoveAll(sw[1])
	}
	v, err := store.Record(id, changed, out.Added, out.Updated, nil, actorName(ctx), source, h.now())
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "the tasks were written but the version was not recorded: "+err.Error())
		return nil, 0
	}
	out.Version = v
	out.Next = "Run the readiness checks: each new or changed task must prove its check passes the right answer and fails wrong ones before evaluations use it."
	return out, fasthttp.StatusCreated
}

// listTasks handles GET /suites/{id}/dataset/tasks?offset=&limit=.
func (h *datasetAPI) listTasks(ctx *fasthttp.RequestCtx) {
	b := h.build
	if !b.needProjects(ctx) {
		return
	}
	id, dir, ok := b.suiteDirParam(ctx)
	if !ok {
		return
	}
	raw, err := os.ReadFile(filepath.Join(dir, suite.SuiteFile))
	if err != nil {
		b.sendStoreError(ctx, "read suite.yaml", err)
		return
	}
	var sy struct {
		Tasks []string `yaml:"tasks"`
	}
	_ = yaml.Unmarshal(raw, &sy)
	offset, _ := strconv.Atoi(string(ctx.QueryArgs().Peek("offset")))
	limit, _ := strconv.Atoi(string(ctx.QueryArgs().Peek("limit")))
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	store := dataset.Open(h.root())
	snap, err := store.Snapshot(id, 0)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "read the version log: "+err.Error())
		return
	}
	out := DatasetTaskList{Benchmark: id, Version: snap.Version, Total: len(sy.Tasks), Offset: offset, Tasks: []DatasetTask{}}
	for i := offset; i < len(sy.Tasks) && len(out.Tasks) < limit; i++ {
		tid := sy.Tasks[i]
		tdir := filepath.Join(dir, "tasks", tid)
		t := DatasetTask{ID: tid, FromRows: fromRows(tdir), RowSHA256: snap.Tasks[tid]}
		if tr, err := os.ReadFile(filepath.Join(tdir, suite.TaskFile)); err == nil {
			var ty struct {
				Title string `yaml:"title"`
				Split string `yaml:"split"`
			}
			if yaml.Unmarshal(tr, &ty) == nil {
				t.Title, t.HeldOut = ty.Title, ty.Split == "held_out"
			}
		}
		if t.RowSHA256 != "" {
			if row, err := store.Row(id, t.RowSHA256); err == nil {
				t.Metadata, t.Chat = row.Metadata, len(row.Messages) > 0
				for _, a := range row.Attachments {
					t.Attachments = append(t.Attachments, a.Name)
				}
			}
		}
		out.Tasks = append(out.Tasks, t)
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

// removeTaskID drops id from the tasks list of a suite.yaml.
func removeTaskID(src []byte, id string) ([]byte, bool, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, false, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, false, errors.New("suite.yaml is not a mapping")
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != "tasks" || m.Content[i+1].Kind != yaml.SequenceNode {
			continue
		}
		seq := m.Content[i+1]
		n := len(seq.Content)
		seq.Content = slices.DeleteFunc(seq.Content, func(x *yaml.Node) bool { return x.Value == id })
		if len(seq.Content) == n {
			return src, false, nil
		}
		out, err := yaml.Marshal(&doc)
		return out, true, err
	}
	return src, false, nil
}

// deleteTask handles DELETE /suites/{id}/dataset/tasks/{task}.
func (h *datasetAPI) deleteTask(ctx *fasthttp.RequestCtx) {
	b := h.build
	if !b.needProjects(ctx) {
		return
	}
	id, dir, ok := b.suiteDirParam(ctx)
	if !ok {
		return
	}
	tid, ok := idParam(ctx, h.env.Mode, "task", "task")
	if !ok {
		return
	}
	store := dataset.Open(h.root())
	unlock := store.Lock(id)
	defer unlock()
	tdir := filepath.Join(dir, "tasks", tid)
	if _, err := os.Stat(tdir); errors.Is(err, fs.ErrNotExist) {
		sendNotFound(ctx, h.env.Mode, "task", id+"/"+tid)
		return
	}
	if !fromRows(tdir) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "task "+tid+" was written by hand, not from rows; remove it on the benchmark's Edit tab")
		return
	}
	raw, err := os.ReadFile(filepath.Join(dir, suite.SuiteFile))
	if err != nil {
		b.sendStoreError(ctx, "read suite.yaml", err)
		return
	}
	edited, changed, err := removeTaskID(raw, tid)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "suite.yaml: "+err.Error())
		return
	}
	if changed {
		if _, err := h.env.Services.Projects.Write(ctx, projectfs.AreaBenchmarks, id+"/"+suite.SuiteFile, edited,
			projectfs.WriteOpts{Actor: actorName(ctx), Note: "dataset: remove task " + tid, IfMatch: shaOf(raw)}); err != nil {
			b.sendStoreError(ctx, "remove the task from suite.yaml", err)
			return
		}
		_ = os.Chmod(filepath.Join(dir, suite.SuiteFile), 0o644)
	}
	if err := os.RemoveAll(tdir); err != nil {
		b.sendStoreError(ctx, "remove task "+tid, err)
		return
	}
	v, err := store.Record(id, nil, nil, nil, []string{tid}, actorName(ctx), "dataset API", h.now())
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "the task was removed but the version was not recorded: "+err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, DatasetTaskDeleted{Benchmark: id, Task: tid, Version: v})
}

// versions handles GET /suites/{id}/dataset/versions, newest first.
func (h *datasetAPI) versions(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.env.Mode, "id", "benchmark")
	if !ok {
		return
	}
	vs, err := dataset.Open(h.root()).Versions(id)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "read the version log: "+err.Error())
		return
	}
	slices.Reverse(vs)
	out := DatasetVersionList{Benchmark: id, Versions: make([]DatasetVersionView, 0, len(vs))}
	names := newActorNames(ctx)
	for _, v := range vs {
		dv := DatasetVersionView{Version: v}
		dv.Actor, dv.ActorName = names.show(v.Actor)
		out.Versions = append(out.Versions, dv)
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

// version handles GET /suites/{id}/dataset/versions/{n}.
func (h *datasetAPI) version(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.env.Mode, "id", "benchmark")
	if !ok {
		return
	}
	n, err := strconv.Atoi(pathParam(ctx, "n"))
	if err != nil || n < 1 {
		sendNotFound(ctx, h.env.Mode, "version", pathParam(ctx, "n"))
		return
	}
	store := dataset.Open(h.root())
	vs, err := store.Versions(id)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "read the version log: "+err.Error())
		return
	}
	i := slices.IndexFunc(vs, func(v dataset.Version) bool { return v.N == n })
	if i < 0 {
		sendNotFound(ctx, h.env.Mode, "version", strconv.Itoa(n))
		return
	}
	snap, err := store.Snapshot(id, n)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "read version "+strconv.Itoa(n)+": "+err.Error())
		return
	}
	out := DatasetVersionDetail{Benchmark: id, Version: vs[i], Tasks: snap.Tasks}
	out.Version.Actor, out.ActorName = newActorNames(ctx).show(out.Version.Actor)
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}
