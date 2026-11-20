package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/voice"
	"github.com/valyala/fasthttp"
)

// X8 voice agents (docs/voice-agents.md): a run's call, read from the voice
// caller's exports.

// VoiceReader is optionally implemented by a ReadIndex. RunVoiceFile returns
// state/<service>/<name> of a run folder for timeline.jsonl, caller.wav and
// agent.wav only, or an error wrapping schemas.ErrNotFound. Without it the
// voice routes answer 503.
type VoiceReader interface {
	RunVoiceFile(runID, service, name string) ([]byte, error)
}

// VoiceCall is GET /api/bench/runs/{id}/voice: the caller's timeline read
// the way the voice metrics read it, with the task's own barge-in and dead air
// bounds when the frozen suite version still loads (BoundsSource "task"),
// else the defaults (BoundsSource "default"), and which audio tracks the run
// kept.
type VoiceCall struct {
	RunID    string          `json:"run_id"`
	Caller   string          `json:"caller"`
	Called   bool            `json:"called"`
	Reason   string          `json:"reason,omitempty"`
	Analysis *voice.Analysis `json:"analysis,omitempty"`
	Audio    []VoiceTrack    `json:"audio"`
	Witness  string          `json:"witness"`
	// BoundsSource says which thresholds classified the call: "task" (the
	// task's voice settings, as the sealed metrics used) or "default".
	BoundsSource string `json:"bounds_source"`
}

// Voice bounds sources (VoiceCall.BoundsSource).
const (
	VoiceBoundsTask    = "task"
	VoiceBoundsDefault = "default"
)

// voiceSpec is the task's voice settings for a run, read from the suite only
// when it still loads at the version the experiment froze, so the Voice tab
// classifies the call with the same bounds as the run's metrics. Nil (the
// defaults) otherwise.
func (h *RunsHandler) voiceSpec(ctx context.Context, rv *RunView) (*schemas.VoiceSpec, string) {
	if h.suites == nil {
		return nil, VoiceBoundsDefault
	}
	m, ok := h.index.Manifest(rv.Slot.ExpID)
	if !ok || m == nil || rv.Slot.Suite == "" {
		return nil, VoiceBoundsDefault
	}
	s, err := h.suites.LoadSuite(ctx, rv.Slot.Suite)
	if err != nil || s == nil || s.Version != m.SuiteVersion {
		return nil, VoiceBoundsDefault
	}
	for _, t := range s.Tasks {
		if t != nil && t.ID == rv.Slot.Task && t.Voice != nil && (rv.Slot.TaskVersion == "" || t.Version == rv.Slot.TaskVersion) {
			return t.Voice, VoiceBoundsTask
		}
	}
	return nil, VoiceBoundsDefault
}

// VoiceTrack is one kept audio track of the call.
type VoiceTrack struct {
	Track  string `json:"track"` // caller | agent
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	URL    string `json:"url"`
}

const voiceWitness = "voice caller timeline (bench-owned service; boundary evidence)"

// voiceState finds the caller's exports in the run's evidence: the service
// that exported a timeline.jsonl.
func voiceState(index ReadIndex, runID string) (string, map[string]schemas.StateFile, error) {
	data, found, err := readInternal(index, runID, FileEvidence) // read for its state file records, never served raw
	if err != nil || !found {
		return "", nil, err
	}
	var ev schemas.Evidence
	if err := json.Unmarshal(data, &ev); err != nil {
		return "", nil, fmt.Errorf("parse %s of run %s: %w", FileEvidence, runID, err)
	}
	caller := ""
	for _, f := range ev.State {
		if f.ID == voice.TimelineExport {
			caller = f.Service
			break
		}
	}
	files := map[string]schemas.StateFile{}
	for _, f := range ev.State {
		if f.Service == caller {
			files[f.ID] = f
		}
	}
	return caller, files, nil
}

// voiceFile reads one caller export and checks it against the evidence
// record and, for a sealed run, the seal.
func (h *RunsHandler) voiceFile(rv *RunView, caller string, rec schemas.StateFile) ([]byte, error) {
	reader, ok := h.index.(VoiceReader)
	if !ok {
		return nil, errVoiceReader
	}
	data, err := reader.RunVoiceFile(rv.Run.RunID, caller, rec.ID)
	if err != nil {
		return nil, err
	}
	got := utils.SHA256Bytes(data)
	if rec.SHA256 != "" && got != bareHash(rec.SHA256) {
		return nil, fmt.Errorf("%s hashes to %s, the evidence says %s: %w", rec.Path, got, rec.SHA256, schemas.ErrSealMismatch)
	}
	if rv.Sealed {
		sealed, err := sealedFileHash(h.index, rv.Run.RunID, rec.Path)
		if err != nil {
			return nil, err
		}
		if got != bareHash(sealed) {
			return nil, fmt.Errorf("%s hashes to %s, the seal says %s: %w", rec.Path, got, sealed, schemas.ErrSealMismatch)
		}
	}
	return data, nil
}

var errVoiceReader = errors.New("voice file reader")

func (h *RunsHandler) sendVoiceError(ctx *fasthttp.RequestCtx, runID string, err error) {
	switch {
	case errors.Is(err, errVoiceReader):
		sendUnavailable(ctx, h.mode, "voice file reader")
	case errors.Is(err, schemas.ErrSealMismatch):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "voice call of run "+runID+" does not match its evidence: "+err.Error())
	default:
		sendDepError(ctx, h.mode, "read voice call of run "+runID, err)
	}
}

// getRunVoice handles GET /api/bench/runs/{id}/voice.
func (h *RunsHandler) getRunVoice(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	runID := rv.Run.RunID
	caller, files, err := voiceState(h.index, runID)
	if err != nil {
		sendDepError(ctx, h.mode, "read evidence of run "+runID, err)
		return
	}
	rec, ok := files[voice.TimelineExport]
	if caller == "" || !ok {
		sendNotFound(ctx, h.mode, "voice call of run", runID)
		return
	}
	out := VoiceCall{RunID: runID, Caller: caller, Audio: []VoiceTrack{}, Witness: voiceWitness, BoundsSource: VoiceBoundsDefault}
	if rec.Error != "" {
		out.Reason = "the timeline export failed: " + rec.Error
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
		return
	}
	data, err := h.voiceFile(rv, caller, rec)
	if err != nil {
		h.sendVoiceError(ctx, runID, err)
		return
	}
	events, err := voice.ParseTimeline(data)
	switch {
	case errors.Is(err, voice.ErrNoCall):
		out.Reason = err.Error()
	case err != nil:
		out.Reason = "the timeline does not parse: " + err.Error()
	default:
		spec, source := h.voiceSpec(ctx, rv)
		a, aerr := voice.Analyze(events, spec)
		out.BoundsSource = source
		if aerr != nil {
			out.Reason = aerr.Error()
			break
		}
		out.Called, out.Analysis = true, a
		for _, track := range []string{"caller", "agent"} {
			f, kept := files[track+".wav"]
			if kept && f.Error == "" && f.Bytes > 44 {
				out.Audio = append(out.Audio, VoiceTrack{Track: track, SHA256: f.SHA256, Bytes: f.Bytes, URL: APIPrefix + "/runs/" + runID + "/voice/audio/" + track})
			}
		}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// getRunVoiceAudio handles GET /api/bench/runs/{id}/voice/audio/{track}: the
// caller's or the agent's track as audio/wav, from a sealed run only, after
// its bytes hash to both the evidence record and the seal.
func (h *RunsHandler) getRunVoiceAudio(ctx *fasthttp.RequestCtx) {
	track := pathParam(ctx, "track")
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	runID := rv.Run.RunID
	if track != "caller" && track != "agent" {
		sendNotFound(ctx, h.mode, "voice track of run "+runID, track)
		return
	}
	if !rv.Sealed {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "run "+runID+" is not sealed: call audio is served only from a sealed run")
		return
	}
	caller, files, err := voiceState(h.index, runID)
	if err != nil {
		sendDepError(ctx, h.mode, "read evidence of run "+runID, err)
		return
	}
	rec, ok := files[track+".wav"]
	if caller == "" || !ok || rec.Error != "" {
		sendNotFound(ctx, h.mode, "voice track of run "+runID, track)
		return
	}
	data, err := h.voiceFile(rv, caller, rec)
	if err != nil {
		h.sendVoiceError(ctx, runID, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("audio/wav")
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	ctx.Response.Header.Set("Content-Security-Policy", "sandbox")
	ctx.SetBody(data)
}
