package traceinbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/traceimport"
)

// The OTLP folder source reads the files an OpenTelemetry collector's file
// exporter writes (one ExportTraceServiceRequest as JSON per line, the
// format traceimport reads). It remembers a byte offset per file, so each
// pull reads only lines added since the last one; a file that shrank, or
// whose first bytes changed, was rotated and is read from the start; at most 64 MiB are read per pull, and
// the rest waits for the next. A session is flagged when a span has
// status ERROR or a tool call failed. Spans of one trace that land in two
// pulls are read as two partial sessions; the collector's batch settings
// decide how often that happens.

const (
	otlpMaxBytesPerPull = 64 << 20
	otlpDefaultPattern  = "*.json*"
)

type otlpDirFetcher struct{}

// matchPattern reports whether name matches a file pattern.
func matchPattern(pattern, name string) (bool, error) {
	return filepath.Match(pattern, name)
}

// Fetch implements Fetcher.
func (f *otlpDirFetcher) Fetch(ctx context.Context, src *Source, cur Cursor, now time.Time) (*Batch, error) {
	pattern := src.Pattern
	if pattern == "" {
		pattern = otlpDefaultPattern
	}
	entries, err := os.ReadDir(src.Path)
	if err != nil {
		return nil, fmt.Errorf("otlp folder %s: %w", src.Path, err)
	}
	b := &Batch{Cursor: cur}
	b.Cursor.Since = now
	if b.Cursor.Files == nil {
		b.Cursor.Files = map[string]int64{}
	}
	if b.Cursor.FileHeads == nil {
		b.Cursor.FileHeads = map[string]string{}
	}
	var names []string
	for _, e := range entries {
		if ok, _ := matchPattern(pattern, e.Name()); ok && e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	budget := int64(otlpMaxBytesPerPull)
	var lines [][]byte
	bad := 0
	for _, name := range names {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if budget <= 0 {
			b.Truncated = true
			break
		}
		chunk, next, head, err := readNewLines(filepath.Join(src.Path, name), b.Cursor.Files[name], b.Cursor.FileHeads[name], budget)
		if err != nil {
			b.Warnings = append(b.Warnings, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		budget -= int64(len(chunk))
		b.Cursor.Files[name] = next
		b.Cursor.FileHeads[name] = head
		for _, line := range bytes.Split(chunk, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			if !json.Valid(line) {
				bad++
				continue
			}
			lines = append(lines, line)
		}
	}
	for name := range b.Cursor.Files {
		if _, err := os.Stat(filepath.Join(src.Path, name)); errors.Is(err, os.ErrNotExist) {
			delete(b.Cursor.Files, name)
			delete(b.Cursor.FileHeads, name)
		}
	}
	if bad > 0 {
		b.Warnings = append(b.Warnings, fmt.Sprintf("%d lines were not JSON and were skipped", bad))
	}
	if len(lines) == 0 {
		return b, nil
	}
	tr, err := traceimport.Parse(bytes.Join(lines, []byte("\n")), traceimport.FormatOTLP)
	if err != nil {
		b.Warnings = append(b.Warnings, err.Error())
		return b, nil
	}
	for _, s := range tr.Sessions {
		if s.Outcome.Status != traceimport.OutcomeError && s.ToolErrors() == 0 {
			continue
		}
		reasons := []string{}
		for _, h := range s.Outcome.Hints {
			reasons = append(reasons, oneLine(h, 200))
		}
		b.Sessions = append(b.Sessions, Flagged{Session: s, TraceID: s.ID, At: firstTime(s.Ended, s.Started, now), Reasons: reasons, Kinds: []string{FlagError}})
	}
	return b, nil
}

// otlpHeadBytes bounds the prefix whose hash identifies an OTLP file.
const otlpHeadBytes = 4096

// readNewLines reads the complete lines of a file after offset, at most
// limit bytes, and returns them with the offset after the last full line
// and the hash of the file's head up to that offset. head is the hash
// saved with offset (fileMark): a file that shrank, or whose head or bytes
// just before the offset changed, is a replacement and is read from the
// start (audit 5-C-04, QC2-26-07). A cursor saved
// before heads were kept has none and is trusted as it was.
func readNewLines(path string, offset int64, head string, limit int64) ([]byte, int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, head, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, offset, head, err
	}
	if info.Size() < offset {
		offset, head = 0, "" // rotated or truncated: read from the start
	}
	if offset > 0 && head != "" {
		got, err := fileMark(f, offset)
		if err != nil {
			return nil, offset, head, err
		}
		if !strings.Contains(head, ".") {
			got, _, _ = strings.Cut(got, ".") // a mark saved before tails were kept
		}
		if got != head {
			offset, head = 0, "" // replaced by a file at least as long
		}
	}
	if info.Size() == offset {
		return nil, offset, head, nil
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, head, err
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, offset, head, err
	}
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		if int64(len(data)) >= limit {
			return nil, offset, head, fmt.Errorf("a line longer than %d bytes", limit)
		}
		return nil, offset, head, nil // a line still being written
	}
	next := offset + int64(end+1)
	if head, err = fileMark(f, next); err != nil {
		return nil, offset, "", err
	}
	return data[:end+1], next, head, nil
}

// fileMark identifies the first n bytes of f: the hash of its head and,
// after a dot, the hash of the otlpHeadBytes before n. A replacement that
// shares the head (a collector's common first batch) differs near the
// checkpoint (QC2-26-07).
func fileMark(f *os.File, n int64) (string, error) {
	h, err := headHash(f, n)
	if err != nil {
		return "", err
	}
	start := max(n-otlpHeadBytes, 0)
	buf := make([]byte, n-start)
	if _, err := f.ReadAt(buf, start); err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	return h + "." + hex.EncodeToString(sum[:]), nil
}

// headHash is the sha256 of the first min(n, otlpHeadBytes) bytes of f.
func headHash(f *os.File, n int64) (string, error) {
	buf := make([]byte, min(n, otlpHeadBytes))
	if _, err := f.ReadAt(buf, 0); err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}
