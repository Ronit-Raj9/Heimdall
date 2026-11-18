package handlers

import (
	"encoding/json"
	"fmt"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// RunA2A is the response of GET /api/bench/runs/{id}/a2a (B1,
// docs/a2a-agents.md): for a run of an a2a agent, Bifrost's agent log
// witness: the registrations, the operations, the hops of a team, the LLM
// and MCP rows linked to each A2A task, and whether the graded answer is the
// one the gateway recorded. Available is false for a run of any other agent,
// or one recorded before a2a agents; A2A is then null. Request and message
// text in it is the agent's: inert display text.
type RunA2A struct {
	RunID     string               `json:"run_id"`
	Available bool                 `json:"available"`
	A2A       *schemas.A2AEvidence `json:"a2a"`
}

// getRunA2A handles GET /api/bench/runs/{id}/a2a.
func (h *RunsHandler) getRunA2A(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	out, err := readRunA2A(h.index, rv.Run.RunID)
	if err != nil {
		sendDepError(ctx, h.mode, "read agent gateway witness of run "+rv.Run.RunID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// readRunA2A reads the agent gateway witness from the run's evidence.json.
func readRunA2A(index ReadIndex, runID string) (RunA2A, error) {
	out := RunA2A{RunID: runID}
	data, found, err := readInternal(index, runID, FileEvidence)
	if err != nil || !found {
		return out, err
	}
	var ev struct {
		A2A *schemas.A2AEvidence `json:"a2a"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return out, fmt.Errorf("parse %s of run %s: %w", FileEvidence, runID, err)
	}
	out.A2A, out.Available = ev.A2A, ev.A2A != nil
	return out, nil
}
