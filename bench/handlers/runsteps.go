package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Caps on the step timeline one response carries. A longer timeline is cut
// at a whole row and the response says so (truncated); it is never cut in
// the middle of a row.
const (
	maxStepsBytes = 16 << 20 // bytes of steps.jsonl read
	maxStepRows   = 20000    // rows sent
)

// RunSteps is the response of GET /api/bench/runs/{id}/steps: the run's
// unified step timeline and the status of its trusted observers
// (docs/steps-and-evidence.md). Available is false for a run recorded
// before the step timeline existed; its lists are then empty, never absent.
// Every string in a row is agent-influenced display text.
type RunSteps struct {
	RunID     string                   `json:"run_id"`
	Available bool                     `json:"available"`
	Steps     []schemas.StepRecord     `json:"steps"`
	Observers []schemas.ObserverStatus `json:"observers"`
	// Truncated is true when the timeline was longer than one response
	// carries; the first rows are sent.
	Truncated bool `json:"truncated,omitempty"`
	// SkippedLines counts lines of steps.jsonl that did not parse as a row.
	SkippedLines int `json:"skipped_lines,omitempty"`
}

// getRunSteps handles GET /api/bench/runs/{id}/steps. The timeline is read
// from steps.jsonl, or from evidence.json's steps when the file is absent;
// the observer status from observers.json, or from evidence.json's
// observers. A run that recorded neither answers 200 with available false.
func (h *RunsHandler) getRunSteps(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	out, err := readRunSteps(h.index, rv.Run.RunID)
	if err != nil {
		sendDepError(ctx, h.mode, "read step timeline of run "+rv.Run.RunID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// readRunSteps assembles the RunSteps of one run from its folder.
func readRunSteps(index ReadIndex, runID string) (RunSteps, error) {
	out := RunSteps{RunID: runID, Steps: []schemas.StepRecord{}, Observers: []schemas.ObserverStatus{}}
	data, stepsFound, err := readInternal(index, runID, schemas.StepsFile)
	if err != nil {
		return out, err
	}
	if stepsFound {
		out.Steps, out.Truncated, out.SkippedLines = parseStepLines(data)
	}
	obsData, obsFound, err := readInternal(index, runID, schemas.ObserversFile)
	if err != nil {
		return out, err
	}
	if obsFound {
		var obs []schemas.ObserverStatus
		if err := json.Unmarshal(obsData, &obs); err != nil {
			return out, fmt.Errorf("parse %s of run %s: %w", schemas.ObserversFile, runID, err)
		}
		if obs != nil {
			out.Observers = obs
		}
	}
	if !stepsFound || !obsFound {
		ev, err := readStepEvidence(index, runID)
		if err != nil {
			return out, err
		}
		if !stepsFound && ev.Steps != nil {
			stepsFound = true
			out.Steps = ev.Steps
			if len(out.Steps) > maxStepRows {
				out.Steps, out.Truncated = out.Steps[:maxStepRows], true
			}
		}
		if !obsFound && ev.Observers != nil {
			obsFound = true
			out.Observers = ev.Observers
		}
	}
	out.Available = stepsFound || obsFound
	return out, nil
}

// stepEvidence is the part of evidence.json the step timeline reads.
type stepEvidence struct {
	Steps     []schemas.StepRecord     `json:"steps"`
	Observers []schemas.ObserverStatus `json:"observers"`
}

// readStepEvidence reads the steps and observers of evidence.json. A run
// without evidence yet has none.
func readStepEvidence(index ReadIndex, runID string) (stepEvidence, error) {
	var ev stepEvidence
	data, found, err := readInternal(index, runID, FileEvidence)
	if err != nil || !found {
		return ev, err
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return ev, fmt.Errorf("parse %s of run %s: %w", FileEvidence, runID, err)
	}
	return ev, nil
}

// parseStepLines decodes steps.jsonl, one StepRecord per line. A line that
// does not parse is skipped and counted, so one bad row never hides the
// rest. Input past maxStepsBytes, or rows past maxStepRows, are left out and
// reported as truncated.
func parseStepLines(data []byte) (steps []schemas.StepRecord, truncated bool, skipped int) {
	steps = []schemas.StepRecord{}
	if len(data) > maxStepsBytes {
		data, truncated = data[:maxStepsBytes], true
		// Keep whole lines only: the cut line is dropped, not counted as bad.
		if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
			data = data[:i]
		} else {
			data = nil
		}
	}
	for len(data) > 0 {
		var line []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			line, data = data, nil
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if len(steps) == maxStepRows {
			return steps, true, skipped
		}
		var s schemas.StepRecord
		if err := json.Unmarshal(line, &s); err != nil {
			skipped++
			continue
		}
		steps = append(steps, s)
	}
	return steps, truncated, skipped
}
