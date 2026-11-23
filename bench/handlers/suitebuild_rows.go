package handlers

// Row mapper and background imports (lane Y13): the saved column mapping of
// a suite (import-map.yaml), files uploaded with the rows, and imports of
// more than 500 rows, written in batches in the background with progress
// the Build tab polls (GET /suites/{id}/rows/imports/{import}). Progress is
// kept on disk under the project's .heimdall folder, so a status survives a
// restart; an import the server was running when it stopped reads as
// interrupted, with the tasks written so far kept.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// Limits of a background import.
const (
	maxAsyncRowImportBytes = 48 << 20
	maxAsyncImportRows     = 20000
	maxListedProblems      = 1000
	maxUploadTotalBytes    = 40 << 20
	maxUploads             = 200
	rowImportBatch         = 100
	importMapFile          = "import-map.yaml"
)

// Import states (RowImportStatus.Status).
const (
	ImportRunning     = "running"
	ImportDone        = "done"
	ImportFailed      = "failed"
	ImportInterrupted = "interrupted"
)

// RowImportStatus is the progress of a background import.
type RowImportStatus struct {
	ID     string `json:"id"`
	Suite  string `json:"suite"`
	Status string `json:"status"` // running | done | failed | interrupted
	Source string `json:"source"`
	// Rows is how many records the data held; Total how many tasks are to
	// be written; Written how many are in the suite so far.
	Rows         int                   `json:"rows"`
	Total        int                   `json:"total"`
	Written      int                   `json:"written"`
	ProblemCount int                   `json:"problem_count"`
	Problems     []scaffold.RowProblem `json:"problems"`
	Error        string                `json:"error,omitempty"`
	StartedAt    time.Time             `json:"started_at"`
	FinishedAt   *time.Time            `json:"finished_at,omitempty"`
	Next         string                `json:"next,omitempty"`
}

// importRegistry holds the imports this process runs, by project root and
// suite, so one suite takes one import at a time.
type importRegistry struct {
	mu   sync.Mutex
	live map[string]*RowImportStatus // root + "\x00" + import id
	busy map[string]string           // root + "\x00" + suite -> import id
}

var rowImports = &importRegistry{live: map[string]*RowImportStatus{}, busy: map[string]string{}}

// running is the id of the import running into suite, "" when none.
func (r *importRegistry) running(root, suiteID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.busy[root+"\x00"+suiteID]
}

func (r *importRegistry) get(root, id string) (RowImportStatus, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.live[root+"\x00"+id]
	if !ok {
		return RowImportStatus{}, false
	}
	cp := *st
	cp.Problems = append([]scaffold.RowProblem(nil), st.Problems...)
	return cp, true
}

// importsDir is where import progress is kept.
func importsDir(root string) string { return filepath.Join(root, ".heimdall", "row-imports") }

// saveStatus writes st to disk atomically. A failure only loses the copy
// kept for after a restart; the live status still answers.
func saveStatus(root string, st RowImportStatus) {
	dir := importsDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".st-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, st.ID+".json")); err != nil {
		os.Remove(tmp.Name())
	}
}

func newImportID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "imp-" + hex.EncodeToString(b[:])
}

// startImport registers a background import of tasks into suite id and
// starts writing them. It refuses while another import into the suite runs.
func (h *suiteBuild) startImport(id, dir, source, actor string, tasks []scaffold.RowTask, problems []scaffold.RowProblem, rows int) (*RowImportStatus, error) {
	root := h.projects.Root()
	st := &RowImportStatus{ID: newImportID(), Suite: id, Status: ImportRunning, Source: source, Rows: rows, Total: len(tasks),
		ProblemCount: len(problems), Problems: problems, StartedAt: time.Now().UTC()}
	if len(st.Problems) > maxListedProblems {
		st.Problems = st.Problems[:maxListedProblems]
	}
	if st.Problems == nil {
		st.Problems = []scaffold.RowProblem{}
	}
	rowImports.mu.Lock()
	if busy := rowImports.busy[root+"\x00"+id]; busy != "" {
		rowImports.mu.Unlock()
		return nil, fmt.Errorf("an import into %s is still running (%s); wait for it to finish", id, busy)
	}
	rowImports.busy[root+"\x00"+id] = st.ID
	rowImports.live[root+"\x00"+st.ID] = st
	snap := *st
	rowImports.mu.Unlock()
	saveStatus(root, snap)
	go h.runImport(root, id, dir, source, actor, tasks, st)
	return &snap, nil
}

// runImport writes tasks in batches, each batch one versioned suite.yaml
// write, updating st after each.
func (h *suiteBuild) runImport(root, id, dir, source, actor string, tasks []scaffold.RowTask, st *RowImportStatus) {
	ctx := context.Background()
	finish := func(status, msg string) {
		syncDatasetVersion(root, id, dir, actor, "Rows import")
		rowImports.mu.Lock()
		now := time.Now().UTC()
		st.Status, st.Error, st.FinishedAt = status, msg, &now
		switch status {
		case ImportDone:
			st.Next = "Run the readiness checks: each new task must prove its check passes the right answer and fails wrong ones before evaluations use it."
		default:
			st.Next = fmt.Sprintf("%d of %d tasks were written and kept. Fix the problem and import the file again: rows already imported get a new id, so remove them from the file first.", st.Written, st.Total)
		}
		delete(rowImports.busy, root+"\x00"+id)
		snap := *st
		rowImports.mu.Unlock()
		saveStatus(root, snap)
	}
	defer func() {
		if r := recover(); r != nil {
			finish(ImportFailed, fmt.Sprintf("the import stopped: %v", r))
		}
	}()
	for start := 0; start < len(tasks); start += rowImportBatch {
		end := min(start+rowImportBatch, len(tasks))
		raw, err := os.ReadFile(filepath.Join(dir, suite.SuiteFile))
		if err != nil {
			finish(ImportFailed, "read suite.yaml: "+err.Error())
			return
		}
		note := fmt.Sprintf("%s (%d-%d of %d)", source, start+1, end, len(tasks))
		if err := h.writeRowTasks(ctx, id, dir, raw, tasks[start:end], note, actor); err != nil {
			msg := err.Error()
			if errors.Is(err, projectfs.ErrConflict) {
				msg = "suite.yaml changed while the import ran: " + msg
			}
			finish(ImportFailed, msg)
			return
		}
		rowImports.mu.Lock()
		st.Written = end
		snap := *st
		rowImports.mu.Unlock()
		saveStatus(root, snap)
	}
	finish(ImportDone, "")
}

// importStatus handles GET /suites/{id}/rows/imports/{import}.
func (h *suiteBuild) importStatus(ctx *fasthttp.RequestCtx) {
	if !h.needProjects(ctx) {
		return
	}
	id, _, ok := h.suiteDirParam(ctx)
	if !ok {
		return
	}
	impID := pathParam(ctx, "import")
	if !validImportID(impID) {
		sendNotFound(ctx, h.mode, "import", impID)
		return
	}
	root := h.projects.Root()
	st, live := rowImports.get(root, impID)
	if !live {
		b, err := os.ReadFile(filepath.Join(importsDir(root), impID+".json"))
		if err != nil || json.Unmarshal(b, &st) != nil {
			sendNotFound(ctx, h.mode, "import", impID)
			return
		}
		if st.Status == ImportRunning {
			st.Status = ImportInterrupted
			st.Error = "the server restarted while the import ran"
			st.Next = fmt.Sprintf("%d of %d tasks were written and kept. Import the rows that are missing again.", st.Written, st.Total)
		}
	}
	if st.Suite != id {
		sendNotFound(ctx, h.mode, "import", impID)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &st)
}

func validImportID(s string) bool {
	if len(s) != 16 || s[:4] != "imp-" {
		return false
	}
	_, err := hex.DecodeString(s[4:])
	return err == nil
}

// decodeUploads decodes the files uploaded with an import, by safe name.
func decodeUploads(in map[string]RowAttachmentUpload) (map[string]scaffold.Upload, string) {
	if len(in) == 0 {
		return nil, ""
	}
	if len(in) > maxUploads {
		return nil, fmt.Sprintf("attachments: at most %d files", maxUploads)
	}
	out := make(map[string]scaffold.Upload, len(in))
	total := 0
	for name, f := range in {
		safe := scaffold.AttachmentName(name)
		if safe == "" {
			return nil, fmt.Sprintf("attachments: %q is not a file name", name)
		}
		data, err := base64.StdEncoding.DecodeString(f.ContentBase64)
		if err != nil {
			return nil, fmt.Sprintf("attachments: %s is not base64: %v", name, err)
		}
		if len(data) > scaffold.MaxAttachmentBytes {
			return nil, fmt.Sprintf("attachments: %s is over %d MiB", name, scaffold.MaxAttachmentBytes>>20)
		}
		total += len(data)
		if total > maxUploadTotalBytes {
			return nil, fmt.Sprintf("attachments: over %d MiB in all; link large files by URL instead", maxUploadTotalBytes>>20)
		}
		if len(f.MediaType) > 100 {
			return nil, fmt.Sprintf("attachments: %s has a media type longer than 100 characters", name)
		}
		out[safe] = scaffold.Upload{Data: data, MediaType: f.MediaType}
	}
	return out, ""
}

// savedMapping is the suite's saved column mapping, nil when none.
func (h *suiteBuild) savedMapping(dir string) *scaffold.RowMapping {
	b, err := os.ReadFile(filepath.Join(dir, importMapFile))
	if err != nil {
		return nil
	}
	var m scaffold.RowMapping
	if yaml.Unmarshal(b, &m) != nil || m.Validate() != nil || m.Empty() {
		return nil
	}
	return &m
}

// saveMapping writes m as the suite's import-map.yaml.
func (h *suiteBuild) saveMapping(ctx context.Context, id string, m scaffold.RowMapping, actor string) error {
	b, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	b = append([]byte("# Column mapping of the last import into this benchmark (Build tab). The next import of\n# the same export reuses it. Each value is a column, a dotted path into nested records.\n"), b...)
	_, err = h.projects.Write(ctx, projectfs.AreaBenchmarks, id+"/"+importMapFile, b, projectfs.WriteOpts{Actor: actor, Note: "save the import column mapping"})
	return err
}

// mergeMapping lays the fields set in m over the detected mapping d.
func mergeMapping(d, m scaffold.RowMapping) scaffold.RowMapping {
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&d.ID, m.ID)
	set(&d.Title, m.Title)
	set(&d.Question, m.Question)
	set(&d.Messages, m.Messages)
	set(&d.Expected, m.Expected)
	set(&d.Rubric, m.Rubric)
	set(&d.Reference, m.Reference)
	set(&d.Wrong, m.Wrong)
	set(&d.Match, m.Match)
	set(&d.Category, m.Category)
	set(&d.Split, m.Split)
	set(&d.UserTurns, m.UserTurns)
	set(&d.Attachments, m.Attachments)
	set(&d.GradeOn, m.GradeOn)
	if len(m.Metadata) > 0 {
		d.Metadata = m.Metadata
	}
	return d
}
