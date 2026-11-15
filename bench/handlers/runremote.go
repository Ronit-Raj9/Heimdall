package handlers

import (
	"encoding/json"
	"fmt"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// RunRemote is the response of GET /api/bench/runs/{id}/remote (Remote
// coverage, docs/remote-execution.md): for a run of a remote_http agent,
// which of the three channels were active and what is therefore trusted,
// agent-reported or unknown. Available is false for a run of any other
// agent, or one recorded before remote coverage; Remote is then null.
// Every string in Remote.Reported is the agent's own word: inert display
// text.
type RunRemote struct {
	RunID     string                  `json:"run_id"`
	Available bool                    `json:"available"`
	Remote    *schemas.RemoteEvidence `json:"remote"`
}

// getRunRemote handles GET /api/bench/runs/{id}/remote.
func (h *RunsHandler) getRunRemote(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	out, err := readRunRemote(h.index, rv.Run.RunID)
	if err != nil {
		sendDepError(ctx, h.mode, "read remote coverage of run "+rv.Run.RunID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// readRunRemote reads the remote summary from the run's evidence.json.
func readRunRemote(index ReadIndex, runID string) (RunRemote, error) {
	out := RunRemote{RunID: runID}
	data, found, err := readInternal(index, runID, FileEvidence)
	if err != nil || !found {
		return out, err
	}
	var ev struct {
		Remote *schemas.RemoteEvidence `json:"remote"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return out, fmt.Errorf("parse %s of run %s: %w", FileEvidence, runID, err)
	}
	out.Remote, out.Available = ev.Remote, ev.Remote != nil
	return out, nil
}
