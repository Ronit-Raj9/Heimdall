package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/maximhq/bifrost/bench/schemas"
)

// FileToolLedger is the tool proxy's ledger in a run folder
// (docs/scenarios-design.md). It is read for the run detail and never served
// raw: tool results are content the services and the agent produced.
const FileToolLedger = "tools.jsonl"

// fillScenario adds a scenario run's evidence to its detail: tool calls,
// sessions, answer, exported state, services and artifact files.
// evidence.json holds them once the run is sealed; before that the tool
// ledger is read from its own file when it exists. A plain diff run has none,
// and its detail is unchanged. Artifact files are listed only; their bytes
// are served by getRunArtifact.
func fillScenario(index ReadIndex, runID string, out *schemas.RunDetail) error {
	var ev struct {
		AdapterFault *schemas.AdapterFaultEvidence `json:"adapter_fault"`
		ToolCalls    []schemas.ToolCall            `json:"tool_calls"`
		Sessions     []schemas.SessionResult       `json:"sessions"`
		Answer       *schemas.Answer               `json:"answer"`
		State        []schemas.StateFile           `json:"state"`
		Services     []schemas.ServiceResult       `json:"services"`
		Artifacts    []schemas.ArtifactFile        `json:"artifacts"`
	}
	data, found, err := readInternal(index, runID, FileEvidence)
	if err != nil {
		return err
	}
	if found {
		if err := json.Unmarshal(data, &ev); err != nil {
			return fmt.Errorf("parse %s of run %s: %w", FileEvidence, runID, err)
		}
	}
	if len(ev.ToolCalls) == 0 {
		if ev.ToolCalls, err = readToolLedger(index, runID); err != nil {
			return err
		}
	}
	out.ToolCalls, out.Sessions, out.Answer, out.State, out.Services = ev.ToolCalls, ev.Sessions, ev.Answer, ev.State, ev.Services
	out.AdapterFault = ev.AdapterFault
	out.Artifacts = ev.Artifacts
	return nil
}

// readInternal reads a run file the API uses but never serves. A missing
// file returns found false and no error.
func readInternal(index ReadIndex, runID, name string) ([]byte, bool, error) {
	data, err := index.RunFile(runID, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read %s of run %s: %w", name, runID, err)
	}
	return data, true, nil
}

// readToolLedger reads the tool proxy's ledger, one schemas.ToolCall per
// line. A run without one has no calls.
func readToolLedger(index ReadIndex, runID string) ([]schemas.ToolCall, error) {
	data, found, err := readInternal(index, runID, FileToolLedger)
	if err != nil || !found {
		return nil, err
	}
	var out []schemas.ToolCall
	for i, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var c schemas.ToolCall
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("parse %s line %d of run %s: %w", FileToolLedger, i+1, runID, err)
		}
		out = append(out, c)
	}
	return out, nil
}
