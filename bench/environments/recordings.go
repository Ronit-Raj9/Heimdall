package environments

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Recordings and the live screen of browser and desktop services (Z03,
// schemas/recordings.go). Recordings land in recordings/<service>/ in the
// run folder, sealed with the run but outside state/, so the clean-room
// verifier never sees them (graders/cleanroom.go mounts state/ only).

// recordingStopTimeout bounds a service's stop command.
const recordingStopTimeout = 30 * time.Second

// liveFrameTimeout and liveInputTimeout bound one live command.
const (
	liveFrameTimeout = 5 * time.Second
	liveInputTimeout = 20 * time.Second
)

var (
	recordingIDRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`)
	recordingNameRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

// recordingExts are the file extensions copied for each kind, with their
// media type. Anything else in a recording folder is skipped.
var recordingExts = map[string]map[string]string{
	schemas.RecordingVideo:   {".webm": "video/webm", ".mp4": "video/mp4"},
	schemas.RecordingHAR:     {".har": "application/json", ".json": "application/json"},
	schemas.RecordingConsole: {".jsonl": "application/x-ndjson", ".json": "application/json", ".txt": "text/plain", ".log": "text/plain"},
	schemas.RecordingLog:     {".jsonl": "application/x-ndjson", ".json": "application/json", ".txt": "text/plain", ".log": "text/plain"},
}

// RecordingMime is the media type a recording file is served with, from its
// kind and name; empty when the name is not one the kind records.
func RecordingMime(kind, name string) string {
	return recordingExts[kind][strings.ToLower(path.Ext(name))]
}

// checkRecordings validates a service's recordings and live blocks. It is pure.
func checkRecordings(s schemas.ServiceSpec) error {
	if r := s.Recordings; r != nil {
		if len(r.Files) == 0 {
			return fmt.Errorf("service %s recordings has no files", s.ID)
		}
		if r.WaitS < 0 || r.WaitS > schemas.MaxRecordingWaitS {
			return fmt.Errorf("service %s recordings wait_s must be 0 to %d", s.ID, schemas.MaxRecordingWaitS)
		}
		seen := map[string]bool{}
		for _, f := range r.Files {
			switch {
			case !recordingIDRe.MatchString(f.ID) || seen[f.ID]:
				return fmt.Errorf("service %s recording id %q is invalid or repeated", s.ID, f.ID)
			case !slices.Contains(schemas.RecordingKinds, f.Kind):
				return fmt.Errorf("service %s recording %s kind %q: want one of %s", s.ID, f.ID, f.Kind, strings.Join(schemas.RecordingKinds, ", "))
			case !path.IsAbs(f.Path) || path.Clean(f.Path) != f.Path || f.Path == "/":
				return fmt.Errorf("service %s recording %s path %q must be an absolute clean container path", s.ID, f.ID, f.Path)
			case f.MaxBytes < 0 || f.MaxBytes > schemas.MaxRunRecordingBytes:
				return fmt.Errorf("service %s recording %s max_bytes must be at most %d", s.ID, f.ID, schemas.MaxRunRecordingBytes)
			}
			seen[f.ID] = true
		}
	}
	if l := s.Live; l != nil && len(l.Frame) == 0 {
		return fmt.Errorf("service %s live needs a frame command", s.ID)
	}
	return nil
}

// exportRecordings copies every service's recordings into the run folder
// and writes the index. Best effort: a recording that fails is noted in its
// record and the run goes on; recordings never change a grade.
func (e *dockerWorkspace) exportRecordings(ctx context.Context, runID, dir string, svcs []runService) {
	var recs []schemas.RecordingRecord
	var total int64
	for _, s := range svcs {
		r := s.spec.Recordings
		if r == nil {
			continue
		}
		container := serviceContainer(runID, s.spec.ID)
		e.stopForRecordings(ctx, runID, container, r)
		for _, f := range r.Files {
			got := e.copyRecording(ctx, container, dir, s.spec.ID, f, &total)
			recs = append(recs, got...)
		}
	}
	if len(recs) == 0 {
		return
	}
	if err := writeRecordingIndex(dir, recs); err != nil {
		e.cfg.Logger.Warn("recordings index not written", "run", runID, "error", err.Error())
	}
}

// stopForRecordings runs the stop command and waits for the container to
// stop, up to the spec's wait.
func (e *dockerWorkspace) stopForRecordings(ctx context.Context, runID, container string, r *schemas.RecordingsSpec) {
	if len(r.Stop) == 0 {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, recordingStopTimeout)
	cmd := exec.CommandContext(sctx, dockerBinary(e.cfg.Docker), append([]string{"exec", container}, r.Stop...)...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	cancel()
	if err == nil {
		return // the command finished the files itself
	}
	// A stop command that stops its own container ends its exec with an
	// error; that is expected. Wait for the container to stop.
	e.cfg.Logger.Debug("recording stop command ended", "run", runID, "error", err.Error(), "output", truncate(out.String(), 200))
	wait := time.Duration(r.WaitS) * time.Second
	if r.WaitS == 0 {
		wait = schemas.DefaultRecordingWaitS * time.Second
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		running, _, err := e.containerState(ctx, container)
		if err != nil || !running {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// copyRecording copies one recording (a file or a folder) out of the
// container into recordings/<service>/ and returns its records. total is
// the run's running byte count, kept under MaxRunRecordingBytes.
func (e *dockerWorkspace) copyRecording(ctx context.Context, container, dir, service string, f schemas.RecordingFile, total *int64) []schemas.RecordingRecord {
	fail := func(msg string) []schemas.RecordingRecord {
		return []schemas.RecordingRecord{{Service: service, ID: f.ID, Kind: f.Kind, Error: msg}}
	}
	limit := f.MaxBytes
	if limit <= 0 {
		limit = schemas.DefaultRecordingBytes
	}
	outDir := filepath.Join(dir, schemas.RecordingsDir, service)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fail("create recordings folder: " + err.Error())
	}
	spool := filepath.Join(outDir, "."+f.ID+".tar")
	defer os.Remove(spool)
	cctx, cancel := context.WithTimeout(ctx, exportTimeout)
	defer cancel()
	fh, err := os.OpenFile(spool, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fail("open spool: " + err.Error())
	}
	w := &cappedWriter{w: fh, limit: limit + 1<<20}
	var errBuf strings.Builder
	cmd := exec.CommandContext(cctx, dockerBinary(e.cfg.Docker), "cp", container+":"+f.Path, "-")
	cmd.Stdout, cmd.Stderr = w, &errBuf
	runErr := cmd.Run()
	if err := errors.Join(w.err, fh.Close()); err != nil {
		return fail("write spool: " + err.Error())
	}
	switch {
	case runErr != nil && recordingPathMissing(errBuf.String()):
		return fail(recordingMissingNote(f.Kind, f.Path))
	case runErr != nil:
		return fail("not written: " + truncate(strings.TrimSpace(errBuf.String()), 200))
	case w.truncated:
		return fail(fmt.Sprintf("over its %d byte cap", limit))
	}
	recs, err := unpackRecording(spool, outDir, service, f, limit, total)
	if err != nil {
		return fail("unpack: " + err.Error())
	}
	if len(recs) == 0 {
		return fail(recordingMissingNote(f.Kind, f.Path))
	}
	return recs
}

// recordingPathMissing reports whether docker cp failed because the path is
// not in the container (as opposed to a daemon or timeout error).
func recordingPathMissing(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "could not find the file") || strings.Contains(s, "no such file") || strings.Contains(s, "no such container:path")
}

// recordingMissingNote is the plain note of a recording the service did not
// write. The common cause is an image that cannot record (a browser image
// without Heimdall's record.js launcher, a Playwright MCP server that
// ignores contextOptions, a desktop image without ffmpeg) or an agent that
// never used the screen. It never fails the run (docs/recordings.md).
func recordingMissingNote(kind, p string) string {
	switch kind {
	case schemas.RecordingVideo:
		return "no video at " + p + ": the agent may not have opened a page or screen, or the service image cannot record (a browser image needs Heimdall's record.js launcher and @playwright/mcp 0.0.83 or later, a desktop image needs ffmpeg). Rebuild the image from its template; see docs/recordings.md"
	case schemas.RecordingHAR:
		return "no HAR at " + p + ": the agent may not have opened a page, or the browser image cannot record (it needs Heimdall's record.js launcher and @playwright/mcp 0.0.83 or later). Rebuild the image from its template; see docs/recordings.md"
	}
	return "no " + kind + " file at " + p + ": the service did not write it"
}

// unpackRecording reads the docker cp tar of one recording into outDir:
// regular files whose extension the kind records, flattened to
// "<id>-<name>", with a "<name>.start" sidecar read as the start time. Text
// recordings (HAR, console, log) are redacted of secrets before they are
// written, since the run folder is sealed only when it holds none.
func unpackRecording(spool, outDir, service string, f schemas.RecordingFile, limit int64, total *int64) ([]schemas.RecordingRecord, error) {
	in, err := os.Open(spool)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	tr := tar.NewReader(in)
	starts := map[string]time.Time{}
	type pending struct {
		name, mime string
		data       []byte
	}
	var files []pending
	var recs []schemas.RecordingRecord
	var used int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := path.Base(hdr.Name)
		if stem, ok := strings.CutSuffix(base, ".start"); ok && hdr.Size < 128 {
			b, _ := io.ReadAll(io.LimitReader(tr, 128))
			if t, ok := parseStart(string(b)); ok {
				starts[stem] = t
			}
			continue
		}
		mime := RecordingMime(f.Kind, base)
		if mime == "" {
			continue
		}
		used += hdr.Size
		if used > limit || *total+hdr.Size > schemas.MaxRunRecordingBytes {
			recs = append(recs, schemas.RecordingRecord{Service: service, ID: f.ID, Kind: f.Kind, Error: fmt.Sprintf("%s is over the recording cap", base)})
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
		if err != nil {
			return nil, err
		}
		if f.Kind != schemas.RecordingVideo && utf8.Valid(data) {
			data = []byte(redactRecording(string(data)))
		}
		*total += int64(len(data))
		files = append(files, pending{name: base, mime: mime, data: data})
	}
	slices.SortFunc(files, func(a, b pending) int { return strings.Compare(a.name, b.name) })
	for _, p := range files {
		name := recordingFileName(f.ID, p.name)
		out := filepath.Join(outDir, name)
		if err := os.WriteFile(out, p.data, 0o644); err != nil {
			return nil, err
		}
		recs = append(recs, schemas.RecordingRecord{
			Service: service, ID: f.ID, Kind: f.Kind, File: name, Mime: p.mime,
			Bytes: int64(len(p.data)), SHA256: utils.SHA256Bytes(p.data), StartedAt: starts[p.name],
		})
	}
	return recs, nil
}

// harSecretHeader is a HAR header (or cookie) entry whose value is a
// credential: {"name": "Authorization", "value": "..."}.
var harSecretHeader = regexp.MustCompile(`(?i)("name"\s*:\s*"(?:authorization|proxy-authorization|cookie|set-cookie|x-api-key|api-key|x-auth-token)"\s*,\s*"value"\s*:\s*)"(?:[^"\\]|\\.)*"`)

// redactRecording removes credentials from a text recording: HAR
// credential headers, the key-value forms utils.RedactSecrets knows, and
// well-known token formats (sk- keys, AWS key ids, GitHub and Slack tokens).
func redactRecording(s string) string {
	s = harSecretHeader.ReplaceAllString(s, `$1"[redacted]"`)
	s = utils.RedactSecrets(s)
	for _, p := range utils.SecretTokenPatterns {
		s = p.RE.ReplaceAllStringFunc(s, func(m string) string {
			if len(m) <= p.Keep {
				return m
			}
			return m[:p.Keep] + "[redacted]"
		})
	}
	return s
}

// recordingFileName is the run folder name of a recorded file: the
// recording id and the file's own name, reduced to safe characters.
func recordingFileName(id, base string) string {
	clean := strings.Trim(recordingNameRe.ReplaceAllString(base, "_"), "._")
	if clean == "" {
		clean = "file"
	}
	name := id + "-" + clean
	if len(name) > 120 {
		ext := path.Ext(name)
		name = name[:120-len(ext)] + ext
	}
	return name
}

// parseStart reads a start sidecar: epoch milliseconds or RFC 3339.
func parseStart(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil && ms > 0 {
		return time.UnixMilli(ms).UTC(), true
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

// writeRecordingIndex writes recordings/recordings.jsonl.
func writeRecordingIndex(dir string, recs []schemas.RecordingRecord) error {
	var buf bytes.Buffer
	for _, r := range recs {
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	out := filepath.Join(dir, schemas.RecordingsDir)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, schemas.RecordingsIndexFile), buf.Bytes(), 0o644)
}

// LiveServices implements schemas.LiveScreen: the run's services with a
// live block, suite first.
func (e *dockerWorkspace) LiveServices(rc *schemas.RunContext) []schemas.LiveService {
	svcs, err := runServices(rc)
	if err != nil {
		return nil
	}
	var out []schemas.LiveService
	for _, s := range svcs {
		if s.spec.Live != nil && len(s.spec.Live.Frame) > 0 {
			out = append(out, schemas.LiveService{ID: s.spec.ID, Takeover: len(s.spec.Live.Input) > 0})
		}
	}
	return out
}

// liveSpec finds a service's live block.
func liveSpec(rc *schemas.RunContext, service string) (*schemas.LiveSpec, error) {
	svcs, err := runServices(rc)
	if err != nil {
		return nil, err
	}
	for _, s := range svcs {
		if s.spec.ID == service && s.spec.Live != nil {
			return s.spec.Live, nil
		}
	}
	return nil, fmt.Errorf("service %q has no live block", service)
}

// LiveFrame implements schemas.LiveScreen: one screen image of the service,
// from its frame command, capped at MaxLiveFrameBytes.
func (e *dockerWorkspace) LiveFrame(ctx context.Context, rc *schemas.RunContext, service string) ([]byte, string, error) {
	_, runID, err := runIDs(rc)
	if err != nil {
		return nil, "", err
	}
	l, err := liveSpec(rc, service)
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := context.WithTimeout(ctx, liveFrameTimeout)
	defer cancel()
	var out bytes.Buffer
	w := &cappedWriter{w: &out, limit: schemas.MaxLiveFrameBytes}
	cmd := exec.CommandContext(cctx, dockerBinary(e.cfg.Docker), append([]string{"exec", serviceContainer(runID, service)}, l.Frame...)...)
	cmd.Stdout = w
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("live frame of %s: %w", service, err)
	}
	if w.truncated {
		return nil, "", fmt.Errorf("live frame of %s is over %d bytes", service, schemas.MaxLiveFrameBytes)
	}
	mime := ImageMime(out.Bytes())
	if mime == "" {
		return nil, "", fmt.Errorf("live frame of %s is not a PNG or JPEG image", service)
	}
	return out.Bytes(), mime, nil
}

// LiveInput implements schemas.LiveScreen: one person's input, as JSON on
// the stdin of the service's input command.
func (e *dockerWorkspace) LiveInput(ctx context.Context, rc *schemas.RunContext, service string, a schemas.HumanAction) error {
	_, runID, err := runIDs(rc)
	if err != nil {
		return err
	}
	l, err := liveSpec(rc, service)
	if err != nil {
		return err
	}
	if len(l.Input) == 0 {
		return fmt.Errorf("service %s takes no input", service)
	}
	body, err := json.Marshal(a)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, liveInputTimeout)
	defer cancel()
	var errBuf strings.Builder
	cmd := exec.CommandContext(cctx, dockerBinary(e.cfg.Docker), append([]string{"exec", "-i", serviceContainer(runID, service)}, l.Input...)...)
	cmd.Stdin = bytes.NewReader(body)
	cmd.Stderr = &cappedWriter{w: &errBuf, limit: 1024}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("input to %s: %v: %s", service, err, truncate(strings.TrimSpace(errBuf.String()), 200))
	}
	return nil
}

// ImageMime is image/png or image/jpeg from the data's magic bytes, else
// empty.
func ImageMime(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	}
	return ""
}
