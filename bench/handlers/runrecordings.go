package handlers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Recordings of browser and desktop runs (Z03, schemas/recordings.go): the
// video, HAR and console files a service wrote, sealed with the run in
// recordings/<service>/ and listed in recordings/recordings.jsonl. They are
// display evidence only; the verifier never read them. The list carries the
// tool calls of the recorded services with their start times, so the run
// page can jump the video to a step and highlight the step under the
// playhead.

// RecordingReader is optionally implemented by a ReadIndex: the host path
// of a recording file (store/runrecordings.go), or of the index when
// service is empty. Without it the recording routes answer an empty list
// and 404.
type RecordingReader interface {
	RunRecordingPath(runID, service, name string) (string, error)
}

// maxRecordings caps the recordings listed for one run.
const maxRecordings = 200

// maxRecordingSteps caps the steps listed with them.
const maxRecordingSteps = 5000

// RecordingView is one row of the recording list.
type RecordingView struct {
	Index     int    `json:"index"`
	Service   string `json:"service"`
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	File      string `json:"file,omitempty"`
	Mime      string `json:"mime,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	Error     string `json:"error,omitempty"`
	URL       string `json:"url,omitempty"`
}

// RecordingStep is one tool call of a recorded service, for syncing the
// video with the trace.
type RecordingStep struct {
	Seq       int    `json:"seq"`
	Service   string `json:"service"`
	Tool      string `json:"tool"`
	Label     string `json:"label"`
	Started   string `json:"started"`
	LatencyMS int64  `json:"latency_ms"`
	IsError   bool   `json:"is_error,omitempty"`
}

// RunRecordings answers GET /runs/{id}/recordings.
type RunRecordings struct {
	RunID      string          `json:"run_id"`
	Sealed     bool            `json:"sealed"`
	Recordings []RecordingView `json:"recordings"`
	Steps      []RecordingStep `json:"steps"`
	// HumanAssisted says a person took control of this debug run; Actions
	// are their inputs (human_actions.jsonl).
	HumanAssisted bool                  `json:"human_assisted,omitempty"`
	Actions       []schemas.HumanAction `json:"actions,omitempty"`
}

// readRecordings reads the run's recording index. A run without one has
// none; lines that do not parse are skipped.
func (h *RunsHandler) readRecordings(runID string) ([]schemas.RecordingRecord, error) {
	reader, ok := h.index.(RecordingReader)
	if !ok {
		return nil, nil
	}
	p, err := reader.RunRecordingPath(runID, "", schemas.RecordingsIndexFile)
	if err != nil {
		if errors.Is(err, schemas.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var out []schemas.RecordingRecord
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() && len(out) < maxRecordings {
		var rec schemas.RecordingRecord
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Service == "" {
			continue
		}
		if rec.File != "" && environments.RecordingMime(rec.Kind, rec.File) == "" {
			continue // a name the kind never records is not served
		}
		out = append(out, rec)
	}
	return out, nil
}

// getRunRecordings handles GET /api/bench/runs/{id}/recordings.
func (h *RunsHandler) getRunRecordings(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	runID := rv.Run.RunID
	recs, err := h.readRecordings(runID)
	if err != nil {
		sendDepError(ctx, h.mode, "read recordings of run "+runID, err)
		return
	}
	out := RunRecordings{RunID: runID, Sealed: rv.Sealed, Recordings: []RecordingView{}, Steps: []RecordingStep{}, HumanAssisted: rv.Run.HumanAssisted}
	services := map[string]bool{}
	for i, r := range recs {
		v := RecordingView{Index: i, Service: r.Service, ID: r.ID, Kind: r.Kind, File: r.File, Mime: r.Mime, Bytes: r.Bytes, SHA256: r.SHA256, Error: r.Error}
		if !r.StartedAt.IsZero() {
			v.StartedAt = r.StartedAt.UTC().Format(time.RFC3339Nano)
		}
		if r.File != "" {
			v.Mime = environments.RecordingMime(r.Kind, r.File)
			v.URL = APIPrefix + "/runs/" + runID + "/recordings/" + strconv.Itoa(i)
		}
		services[r.Service] = true
		out.Recordings = append(out.Recordings, v)
	}
	if len(services) > 0 {
		calls, err := readToolLedger(h.index, runID)
		if err != nil {
			sendDepError(ctx, h.mode, "read tool calls of run "+runID, err)
			return
		}
		for _, c := range calls {
			if !services[c.Server] || c.Kind != "" || c.Started.IsZero() || len(out.Steps) >= maxRecordingSteps {
				continue
			}
			out.Steps = append(out.Steps, RecordingStep{Seq: c.Seq, Service: c.Server, Tool: c.Tool, Label: StepLabel(c.Tool, c.Arguments),
				Started: c.Started.UTC().Format(time.RFC3339Nano), LatencyMS: c.LatencyMS, IsError: c.IsError || c.Fault != ""})
		}
	}
	if data, found, err := readInternal(h.index, runID, schemas.HumanActionsFile); err == nil && found {
		names := newActorNames(ctx)
		for _, line := range bytes.Split(data, []byte("\n")) {
			var a schemas.HumanAction
			if json.Unmarshal(line, &a) == nil && a.Action != "" {
				a.By, a.ByName = names.show(a.By)
				out.Actions = append(out.Actions, a)
			}
		}
	}
	ctx.Response.Header.Set("Cache-Control", "no-store")
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// stepLabelKeys are the argument keys a step label shows, in order.
var stepLabelKeys = []string{"url", "element", "text", "combo", "key", "direction", "path"}

// StepLabel is a short label of a tool call: the tool and its most telling
// arguments, at most 120 bytes. It is pure.
func StepLabel(tool, args string) string {
	label := tool
	var m map[string]any
	if json.Unmarshal([]byte(args), &m) == nil {
		var parts []string
		for _, k := range stepLabelKeys {
			if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
				parts = append(parts, strings.TrimSpace(v))
			}
		}
		if x, ok := m["x"].(float64); ok {
			if y, ok := m["y"].(float64); ok {
				parts = append(parts, fmt.Sprintf("(%d, %d)", int(x), int(y)))
			}
		}
		if len(parts) > 0 {
			label += " " + strings.Join(parts, " ")
		}
	}
	label = strings.Join(strings.Fields(label), " ")
	if len(label) > 120 {
		label = strings.ToValidUTF8(label[:117], "") + "..."
	}
	return label
}

// getRunRecording handles GET /api/bench/runs/{id}/recordings/{n}: recording
// n of the list, with its media type, in byte ranges when the request asks
// (a video player seeks by range).
func (h *RunsHandler) getRunRecording(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	runID := rv.Run.RunID
	n, err := strconv.Atoi(pathParam(ctx, "n"))
	if err != nil || n < 0 {
		sendNotFound(ctx, h.mode, "recording of run "+runID, pathParam(ctx, "n"))
		return
	}
	recs, err := h.readRecordings(runID)
	if err != nil {
		sendDepError(ctx, h.mode, "read recordings of run "+runID, err)
		return
	}
	if n >= len(recs) || recs[n].File == "" {
		sendNotFound(ctx, h.mode, "recording of run "+runID, strconv.Itoa(n))
		return
	}
	rec := recs[n]
	p, err := h.index.(RecordingReader).RunRecordingPath(runID, rec.Service, rec.File)
	if err != nil {
		if errors.Is(err, schemas.ErrNotFound) {
			sendNotFound(ctx, h.mode, "recording of run "+runID, rec.File)
			return
		}
		sendDepError(ctx, h.mode, "read recording of run "+runID, err)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		sendDepError(ctx, h.mode, "open recording of run "+runID, err)
		return
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		sendDepError(ctx, h.mode, "stat recording of run "+runID, err)
		return
	}
	size := info.Size()
	start, length, partial, ok := ParseRange(string(ctx.Request.Header.Peek("Range")), size)
	if !ok {
		f.Close()
		ctx.Response.Header.Set("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestedRangeNotSatisfiable, lib.ErrTypeBadRequest, "range not satisfiable for "+rec.File)
		return
	}
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Accept-Ranges", "bytes")
	ctx.Response.Header.Set("Cache-Control", "private, max-age=3600")
	ctx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	ctx.Response.Header.Set("Content-Security-Policy", "sandbox")
	ctx.Response.Header.Set("Content-Disposition", `inline; filename="`+rec.File+`"`)
	ctx.SetContentType(environments.RecordingMime(rec.Kind, rec.File))
	if partial {
		ctx.SetStatusCode(fasthttp.StatusPartialContent)
		ctx.Response.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, size))
	} else {
		ctx.SetStatusCode(fasthttp.StatusOK)
	}
	ctx.SetBodyStream(&sectionCloser{SectionReader: io.NewSectionReader(f, start, length), f: f}, int(length))
}

// sectionCloser closes the file once the body stream is sent.
type sectionCloser struct {
	*io.SectionReader
	f *os.File
}

func (s *sectionCloser) Close() error { return s.f.Close() }

// ParseRange reads one HTTP byte range against a file of size bytes. An
// empty header is the whole file (partial false). It returns ok false for
// a range that cannot be served; several ranges are served as the whole
// file. It is pure.
func ParseRange(header string, size int64) (start, length int64, partial, ok bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0, size, false, true
	}
	spec, found := strings.CutPrefix(header, "bytes=")
	if !found {
		return 0, 0, false, false
	}
	if strings.Contains(spec, ",") {
		return 0, size, false, true
	}
	a, b, found := strings.Cut(strings.TrimSpace(spec), "-")
	if !found {
		return 0, 0, false, false
	}
	switch {
	case a == "" && b == "":
		return 0, 0, false, false
	case a == "": // the last b bytes
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil || n <= 0 || size == 0 {
			return 0, 0, false, false
		}
		n = min(n, size)
		return size - n, n, true, true
	}
	s, err := strconv.ParseInt(a, 10, 64)
	if err != nil || s < 0 || s >= size {
		return 0, 0, false, false
	}
	end := size - 1
	if b != "" {
		e, err := strconv.ParseInt(b, 10, 64)
		if err != nil || e < s {
			return 0, 0, false, false
		}
		end = min(e, size-1)
	}
	return s, end - s + 1, true, true
}
