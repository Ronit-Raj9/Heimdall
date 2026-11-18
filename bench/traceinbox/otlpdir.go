package traceinbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/maximhq/bifrost/bench/traceimport"
)

// The OTLP folder source reads the files an OpenTelemetry collector's file
// exporter writes (one ExportTraceServiceRequest as JSON per line, the
// format traceimport reads). It remembers a byte offset per file, so each
// pull reads only lines added since the last one; a file that shrank was
// rotated and is read from the start; at most 64 MiB are read per pull, and
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
		chunk, next, err := readNewLines(filepath.Join(src.Path, name), b.Cursor.Files[name], budget)
		if err != nil {
			b.Warnings = append(b.Warnings, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		budget -= int64(len(chunk))
		b.Cursor.Files[name] = next
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

// readNewLines reads the complete lines of a file after offset, at most
// limit bytes, and returns them with the offset after the last full line.
func readNewLines(path string, offset, limit int64) ([]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, offset, err
	}
	if info.Size() < offset {
		offset = 0 // rotated or truncated: read from the start
	}
	if info.Size() == offset {
		return nil, offset, nil
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, offset, err
	}
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		if int64(len(data)) >= limit {
			return nil, offset, fmt.Errorf("a line longer than %d bytes", limit)
		}
		return nil, offset, nil // a line still being written
	}
	return data[:end+1], offset + int64(end+1), nil
}
