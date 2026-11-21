package handlers

import (
	"bytes"
	"encoding/json"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// RunTrace is the body of GET /api/bench/runs/{id}/trace?format=json: the
// run's step timeline and its canonical event log in one download. It holds
// only what GET /runs/{id}/steps and GET /runs/{id}/events already serve;
// the raw agent stream stays local (DESIGN.md 14).
type RunTrace struct {
	RunID  string          `json:"run_id"`
	Sealed bool            `json:"sealed"`
	Steps  RunSteps        `json:"steps"`
	Events []schemas.Event `json:"events"`
}

// getRunTrace handles GET /api/bench/runs/{id}/trace. format=json (the
// default) sends RunTrace; format=jsonl sends the step timeline one row per
// line; format=otlp is the sealed-run OpenTelemetry export of
// GET /runs/{id}/otlp. Every format is sent as an attachment named after the
// run. An unknown format is 400.
func (h *RunsHandler) getRunTrace(ctx *fasthttp.RequestCtx) {
	format := string(ctx.QueryArgs().Peek("format"))
	switch format {
	case "", "json", "jsonl":
	case "otlp":
		h.getRunOTLP(ctx)
		return
	default:
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "format must be json, jsonl or otlp")
		return
	}
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	runID := rv.Run.RunID
	steps, err := readRunSteps(h.index, runID)
	if err != nil {
		sendDepError(ctx, h.mode, "read step timeline of run "+runID, err)
		return
	}
	var body []byte
	name := runID + ".trace.json"
	contentType := "application/json; charset=utf-8"
	if format == "jsonl" {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		for _, step := range steps.Steps {
			if err := enc.Encode(step); err != nil {
				sendDepError(ctx, h.mode, "encode step timeline of run "+runID, err)
				return
			}
		}
		body, name, contentType = buf.Bytes(), runID+".steps.jsonl", "application/x-ndjson; charset=utf-8"
	} else {
		events, err := readEvents(h.index, runID)
		if err != nil {
			sendDepError(ctx, h.mode, "read events of run "+runID, err)
			return
		}
		if events == nil {
			events = []schemas.Event{}
		}
		body, err = json.Marshal(RunTrace{RunID: runID, Sealed: rv.Sealed, Steps: steps, Events: events})
		if err != nil {
			sendDepError(ctx, h.mode, "encode trace of run "+runID, err)
			return
		}
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType(contentType)
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	// The run id passed idParam (validID), so it needs no quoting.
	ctx.Response.Header.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	ctx.SetBody(body)
}
