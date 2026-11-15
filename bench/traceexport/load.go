package traceexport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Run folder files the exporter reads.
const (
	fileRun         = "run.json"
	fileEvidence    = "evidence.json"
	fileMetrics     = "metrics.json"
	fileTransitions = "transitions.jsonl"
	fileSeal        = "seal.json"
	fileSlot        = "slot.json"
)

// ReadFunc returns one file of a run folder by name. A missing file is an
// error wrapping fs.ErrNotExist or schemas.ErrNotFound.
type ReadFunc func(name string) ([]byte, error)

// LoadFiles reads a run's export input through read: run.json and
// seal.json are required (an unsealed run has no seal.json and is refused
// with ErrUnsealed), evidence.json, metrics.json and transitions.jsonl are
// read when present. The slot is not a run file; the caller sets it. It does
// not verify the seal: the caller does that before exporting.
func LoadFiles(read ReadFunc) (Input, error) {
	var in Input
	b, found, err := readOptional(read, fileRun)
	if err != nil {
		return in, err
	}
	if !found {
		return in, fmt.Errorf("load run: %s missing", fileRun)
	}
	if err := json.Unmarshal(b, &in.Run); err != nil {
		return in, fmt.Errorf("parse %s: %w", fileRun, err)
	}
	seal, found, err := readOptional(read, fileSeal)
	if err != nil {
		return in, err
	}
	if !found {
		return in, fmt.Errorf("load run %s: %w", in.Run.RunID, ErrUnsealed)
	}
	in.SealHash = utils.SHA256Bytes(seal)
	if err := readJSON(read, fileEvidence, &in.Evidence); err != nil {
		return in, err
	}
	if err := readJSON(read, fileMetrics, &in.Metrics); err != nil {
		return in, err
	}
	b, found, err = readOptional(read, fileTransitions)
	if err != nil || !found {
		return in, err
	}
	for i, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var t schemas.Transition
		if err := json.Unmarshal(line, &t); err != nil {
			return in, fmt.Errorf("parse %s line %d: %w", fileTransitions, i+1, err)
		}
		in.Transitions = append(in.Transitions, t)
	}
	return in, nil
}

// LoadRun reads the export input of the run folder dir
// (<exp>/slots/<slot>/runs/<run>): its run files through LoadFiles and the
// slot from the slot folder's slot.json. Only plain file names inside dir are
// read.
func LoadRun(dir string) (Input, error) {
	in, err := LoadFiles(func(name string) ([]byte, error) {
		if name != filepath.Base(name) {
			return nil, fmt.Errorf("run file %q: %w", name, fs.ErrNotExist)
		}
		return os.ReadFile(filepath.Join(dir, name))
	})
	if err != nil {
		return in, fmt.Errorf("load run folder %s: %w", dir, err)
	}
	slot := filepath.Join(filepath.Dir(filepath.Dir(dir)), fileSlot)
	b, err := os.ReadFile(slot)
	if err != nil {
		return in, fmt.Errorf("load run folder %s: read slot: %w", dir, err)
	}
	if err := json.Unmarshal(b, &in.Slot); err != nil {
		return in, fmt.Errorf("load run folder %s: parse slot: %w", dir, err)
	}
	return in, nil
}

// Marshal returns the trace as indented OTLP/JSON with a trailing newline.
// The same trace always gives the same bytes.
func Marshal(td *TracesData) ([]byte, error) {
	b, err := json.MarshalIndent(td, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal trace: %w", err)
	}
	return append(b, '\n'), nil
}

// Merge returns one document holding the resource spans of every trace, so
// several runs export as one file.
func Merge(tds ...*TracesData) *TracesData {
	out := &TracesData{ResourceSpans: []ResourceSpans{}}
	for _, td := range tds {
		if td != nil {
			out.ResourceSpans = append(out.ResourceSpans, td.ResourceSpans...)
		}
	}
	return out
}

// readOptional reads one file; a missing file is found false.
func readOptional(read ReadFunc, name string) ([]byte, bool, error) {
	b, err := read(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read %s: %w", name, err)
	}
	return b, true, nil
}

// readJSON decodes an optional JSON file into dst.
func readJSON(read ReadFunc, name string, dst any) error {
	b, found, err := readOptional(read, name)
	if err != nil || !found {
		return err
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}
	return nil
}
