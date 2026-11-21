package handlers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Screen replay of computer-use and browser runs: the screenshots the agent
// got through the tool proxy, saved as frames/NNNN.png with one line per
// frame in frames/frames.jsonl (schemas.FrameRecord).

// FrameReader is optionally implemented by a ReadIndex. RunFrame returns the
// bytes of frames/<name> in a run folder, for frames.jsonl or a frame image
// name (FrameNameOK) only, or an error wrapping schemas.ErrNotFound. Without
// it the frame routes answer an empty list and 404.
type FrameReader interface {
	RunFrame(runID, name string) ([]byte, error)
}

// maxFrameBytes caps one served frame image.
const maxFrameBytes = 16 << 20

// maxFrames caps the frames listed for one run.
const maxFrames = 5000

var frameNameRE = regexp.MustCompile(`^[0-9]{4,6}\.(png|jpg|jpeg)$`)

// FrameNameOK reports whether name is a frame image name: 4 to 6 digits and
// .png, .jpg or .jpeg. Nothing else is read from the frames folder except
// schemas.FramesIndexFile.
func FrameNameOK(name string) bool { return frameNameRE.MatchString(name) }

// frameView is one row of the frame list.
type frameView struct {
	Index     int    `json:"index"`
	Seq       int    `json:"seq,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	OffsetMS  int64  `json:"offset_ms"`
	Tool      string `json:"tool,omitempty"`
	Action    string `json:"action,omitempty"`
	Image     string `json:"image"`
	URL       string `json:"url"`
}

// readFrames reads the run's frame index. A run without one has no frames.
// Lines that do not parse, or name a bad file, are skipped.
func (h *RunsHandler) readFrames(runID string) ([]frameView, error) {
	reader, ok := h.index.(FrameReader)
	if !ok {
		return []frameView{}, nil
	}
	data, err := reader.RunFrame(runID, schemas.FramesIndexFile)
	if err != nil {
		if errors.Is(err, schemas.ErrNotFound) {
			return []frameView{}, nil
		}
		return nil, err
	}
	out := []frameView{}
	var first int64
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() && len(out) < maxFrames {
		var rec schemas.FrameRecord
		if json.Unmarshal(sc.Bytes(), &rec) != nil || !FrameNameOK(rec.File) {
			continue
		}
		ms := rec.Time.UnixMilli()
		if len(out) == 0 {
			first = ms
		}
		v := frameView{Index: len(out), Seq: rec.Seq, OffsetMS: max(ms-first, 0), Tool: rec.Tool, Action: rec.Action, Image: "frames/" + rec.File,
			URL: APIPrefix + "/runs/" + runID + "/frames/" + strconv.Itoa(len(out))}
		if !rec.Time.IsZero() {
			v.Timestamp = rec.Time.UTC().Format("2006-01-02T15:04:05.000Z07:00")
		}
		out = append(out, v)
	}
	return out, nil
}

// getRunFrames handles GET /api/bench/runs/{id}/frames: the screen replay
// frames of the run in order, live or sealed. A run without frames answers
// an empty list.
func (h *RunsHandler) getRunFrames(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	frames, err := h.readFrames(rv.Run.RunID)
	if err != nil {
		sendDepError(ctx, h.mode, "read frames of run "+rv.Run.RunID, err)
		return
	}
	ctx.Response.Header.Set("Cache-Control", "no-store")
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, map[string]any{"run_id": rv.Run.RunID, "live": !rv.Sealed, "frames": frames})
}

// getRunFrame handles GET /api/bench/runs/{id}/frames/{n}: the image of
// frame n (0-based), inline as image/png or image/jpeg, read only from the
// run's frames folder.
func (h *RunsHandler) getRunFrame(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	runID := rv.Run.RunID
	n, err := strconv.Atoi(pathParam(ctx, "n"))
	if err != nil || n < 0 {
		sendNotFound(ctx, h.mode, "frame of run "+runID, pathParam(ctx, "n"))
		return
	}
	frames, err := h.readFrames(runID)
	if err != nil {
		sendDepError(ctx, h.mode, "read frames of run "+runID, err)
		return
	}
	if n >= len(frames) {
		sendNotFound(ctx, h.mode, "frame of run "+runID, strconv.Itoa(n))
		return
	}
	name := frames[n].Image[len("frames/"):]
	data, err := h.index.(FrameReader).RunFrame(runID, name)
	if err != nil {
		if errors.Is(err, schemas.ErrNotFound) {
			sendNotFound(ctx, h.mode, "frame of run "+runID, name)
			return
		}
		sendDepError(ctx, h.mode, "read frame of run "+runID, err)
		return
	}
	if len(data) > maxFrameBytes {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, "frame "+name+" of run "+runID+" is over the 16 MiB cap")
		return
	}
	ctype := ""
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		ctype = "image/png"
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		ctype = "image/jpeg"
	default:
		sendNotFound(ctx, h.mode, "frame image of run "+runID, name)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType(ctype)
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Cache-Control", "private, max-age=3600")
	ctx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	ctx.Response.Header.Set("Content-Security-Policy", "sandbox")
	ctx.SetBody(data)
}
