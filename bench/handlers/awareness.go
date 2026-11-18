package handlers

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
)

// Eval integrity area (S2): evaluation awareness (docs/eval-awareness.md).

// Run files the awareness scan reads, besides run.json's claim.
const (
	FileGatewayRaw = "gateway.raw.jsonl"
	FileStream     = "agent.stream.jsonl"
	FileRealism    = utils.RealismFile
)

// AwarenessLabel is the text every awareness report carries, so no reader
// takes the count for more than it is.
const AwarenessLabel = "Heuristic phrase scan of the agent's own messages and reasoning summaries. It counts words, not intent; report only, never a gate."

// maxAwarenessExamples caps the examples of one report.
const maxAwarenessExamples = 20

// AwarenessReport is GET /experiments/{id}/awareness: the evaluation
// awareness phrase scan of every counted sealed run of an experiment,
// computed on request from the sealed run files, so it works on runs sealed
// before the eval_awareness_signals metric existed.
type AwarenessReport struct {
	ExpID          string                  `json:"exp_id"`
	Realism        string                  `json:"realism"` // standard | neutral, from the manifest or the runs' realism.json
	Heuristic      bool                    `json:"heuristic"`
	ScannerVersion string                  `json:"scanner_version"`
	Label          string                  `json:"label"`
	PerArm         map[string]AwarenessArm `json:"per_arm"`
	Examples       []AwarenessExample      `json:"examples"`    // first maxAwarenessExamples signals, in slot order
	NotScanned     []AwarenessNotScanned   `json:"not_scanned"` // runs with no agent text to read
}

// AwarenessArm is one arm's counts.
type AwarenessArm struct {
	Runs            int `json:"runs"`              // counted sealed runs
	RunsScanned     int `json:"runs_scanned"`      // of those, runs with agent text to read
	RunsWithSignals int `json:"runs_with_signals"` // of those, runs with at least one signal
	Signals         int `json:"signals"`           // distinct signals over the scanned runs
}

// AwarenessExample is one signal with where it was found.
type AwarenessExample struct {
	RunID     string `json:"run_id"`
	Arm       string `json:"arm"`
	Task      string `json:"task"`
	Condition string `json:"condition"`
	Source    string `json:"source"` // assistant_message | reasoning_summary | final_message | agent_stream
	Phrase    string `json:"phrase"`
	Excerpt   string `json:"excerpt"`
}

// AwarenessNotScanned is a counted run the scan could not read.
type AwarenessNotScanned struct {
	RunID  string `json:"run_id"`
	Reason string `json:"reason"`
}

// AwarenessRouteDoc describes GET /experiments/{id}/awareness for the OpenAPI
// description (APIRoutes).
var AwarenessRouteDoc = RouteDoc{
	Method: "GET", Path: APIPrefix + "/experiments/{id}/awareness", Tag: "analysis",
	Summary:  "Evaluation awareness: a heuristic phrase scan of the agents' own words for remarks that they are being tested; report only",
	Response: AwarenessReport{},
}

// AwarenessHandler serves the evaluation awareness report.
type AwarenessHandler struct {
	mode  schemas.Mode
	index ReadIndex
}

// NewAwarenessHandler creates the awareness handler. index may be nil.
func NewAwarenessHandler(mode schemas.Mode, index ReadIndex) *AwarenessHandler {
	return &AwarenessHandler{mode: mode, index: index}
}

// RegisterRoutes registers GET /experiments/{id}/awareness.
func (h *AwarenessHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/experiments/{id}/awareness", lib.ChainMiddlewares(h.getAwareness, middlewares...))
}

// getAwareness handles GET /api/bench/experiments/{id}/awareness.
func (h *AwarenessHandler) getAwareness(ctx *fasthttp.RequestCtx) {
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "run index")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "experiment")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	rep, err := BuildAwareness(work, h.index, id)
	if err != nil {
		sendDepError(ctx, h.mode, "awareness", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, rep)
}

// BuildAwareness scans every counted sealed run of the experiment: the
// output side of its gateway rows (gateway.raw.jsonl), the message lines of
// its agent stream and its final message, each through
// utils.ScanEvalAwareness. A run with none of the three is listed in
// not_scanned and counts in runs, not in runs_scanned. The realism is the
// manifest's experiment setting, else neutral when a run recorded
// realism.json, else standard. An unknown experiment is schemas.ErrNotFound.
func BuildAwareness(ctx context.Context, index ReadIndex, expID string) (*AwarenessReport, error) {
	m, ok := index.Manifest(expID)
	if !ok {
		return nil, fmt.Errorf("experiment %s: %w", expID, schemas.ErrNotFound)
	}
	rep := &AwarenessReport{ExpID: expID, Realism: schemas.RealismStandard, Heuristic: true,
		ScannerVersion: utils.EvalAwareScannerVersion, Label: AwarenessLabel,
		PerArm: map[string]AwarenessArm{}, Examples: []AwarenessExample{}, NotScanned: []AwarenessNotScanned{}}
	for arm := range m.Arms {
		rep.PerArm[arm] = AwarenessArm{}
	}
	runs := make([]RunView, 0)
	for _, rv := range countedRuns(index.Runs(expID)) {
		if rv.Sealed {
			runs = append(runs, rv)
		}
	}
	slices.SortFunc(runs, func(x, y RunView) int {
		return cmp.Or(cmp.Compare(x.Slot.OrderIndex, y.Slot.OrderIndex), cmp.Compare(x.Slot.SlotID, y.Slot.SlotID))
	})
	neutralRun := false
	for _, rv := range runs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		realism, err := runRealism(index, rv.Run.RunID)
		if err != nil {
			return nil, err
		}
		neutralRun = neutralRun || realism == schemas.RealismNeutral
		texts, reason, err := runAwareTexts(index, rv)
		if err != nil {
			return nil, err
		}
		arm := rep.PerArm[rv.Slot.Arm]
		arm.Runs++
		if len(texts) == 0 {
			rep.NotScanned = append(rep.NotScanned, AwarenessNotScanned{RunID: rv.Run.RunID, Reason: reason})
			rep.PerArm[rv.Slot.Arm] = arm
			continue
		}
		arm.RunsScanned++
		signals := utils.ScanEvalAwareness(texts)
		if len(signals) > 0 {
			arm.RunsWithSignals++
			arm.Signals += len(signals)
		}
		rep.PerArm[rv.Slot.Arm] = arm
		for _, s := range signals {
			if len(rep.Examples) >= maxAwarenessExamples {
				break
			}
			rep.Examples = append(rep.Examples, AwarenessExample{RunID: rv.Run.RunID, Arm: rv.Slot.Arm, Task: rv.Slot.Task,
				Condition: rv.Slot.Condition, Source: s.Source, Phrase: s.Phrase, Excerpt: s.Excerpt}) // already redacted by the scanner
		}
	}
	switch {
	case m.Experiment.Realism != "":
		rep.Realism = m.Experiment.Realism
	case neutralRun:
		rep.Realism = schemas.RealismNeutral
	}
	return rep, nil
}

// runRealism reads a run's realism.json: neutral runs write one, standard
// runs (and every run recorded before realism existed) do not.
func runRealism(index ReadIndex, runID string) (string, error) {
	data, found, err := readAwareFile(index, runID, FileRealism)
	if err != nil || !found {
		return schemas.RealismStandard, err
	}
	r, err := utils.ReadRealism(data)
	if err != nil {
		return "", fmt.Errorf("run %s: %w", runID, err)
	}
	return r, nil
}

// runAwareTexts gathers one run's agent text in scan order: the gateway rows'
// output side, the agent stream's message lines, the final message. With no
// text it returns why.
func runAwareTexts(index ReadIndex, rv RunView) ([]utils.AwareText, string, error) {
	var texts []utils.AwareText
	var missing []string
	raw, found, err := readAwareFile(index, rv.Run.RunID, FileGatewayRaw)
	if err != nil {
		return nil, "", err
	}
	if !found {
		missing = append(missing, "no gateway log")
	} else if got := utils.GatewayRawFileTexts(raw); len(got) > 0 {
		texts = append(texts, got...)
	} else {
		missing = append(missing, "no assistant output in the gateway log")
	}
	stream, found, err := readAwareFile(index, rv.Run.RunID, FileStream)
	if err != nil {
		return nil, "", err
	}
	if got := utils.AgentStreamTexts(stream); len(got) > 0 {
		texts = append(texts, got...)
	} else if found {
		missing = append(missing, "no message lines in the agent stream")
	} else {
		missing = append(missing, "no agent stream")
	}
	if rv.Run.Claim != nil && strings.TrimSpace(rv.Run.Claim.Text) != "" {
		texts = append(texts, utils.AwareText{Source: utils.AwareSourceFinal, Text: rv.Run.Claim.Text})
	} else {
		missing = append(missing, "no final message")
	}
	return texts, strings.Join(missing, ", "), nil
}

// awareFiles are the run files the scan reads. The agent stream and the
// gateway rows are never served as files (runFileTypes): they can hold
// secrets the agent printed. The report carries only short excerpts around a
// match, with secrets redacted.
var awareFiles = map[string]bool{FileGatewayRaw: true, FileStream: true, FileRealism: true}

// readAwareFile reads one of awareFiles; a missing file is not an error.
func readAwareFile(index ReadIndex, runID, name string) ([]byte, bool, error) {
	if !awareFiles[name] {
		return nil, false, fmt.Errorf("read %s of run %s: not an awareness input", name, runID)
	}
	data, err := index.RunFile(runID, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read %s of run %s: %w", name, runID, err)
	}
	return data, true, nil
}
