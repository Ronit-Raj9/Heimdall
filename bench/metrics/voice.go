package metrics

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/voice"
)

// X8 voice agents (docs/voice-agents.md). They apply to a run of a task
// with a voice: block and are not applicable elsewhere. Latency, barge-in
// and dead air come from the voice caller's timeline (state/<caller>/
// timeline.jsonl), written by a bench-owned service that plays the agent's
// audio on a real-time clock, so they are boundary evidence the agent
// cannot edit. stt_wer reads the gateway's transcription rows when the
// agent transcribed through Bifrost, and otherwise the agent's own reported
// transcripts, labelled agent_reported. voice_cost_per_minute divides the
// run's cost ledger, audio rows included (evidence/cost.go), by the call's
// length.
const (
	TimeToFirstAudioMS = "time_to_first_audio_ms"
	TurnLatencyP50MS   = "turn_latency_p50_ms"
	TurnLatencyP95MS   = "turn_latency_p95_ms"
	BargeInHandled     = "barge_in_handled"
	DeadAirS           = "dead_air_s"
	STTWER             = "stt_wer"
	VoiceCostPerMinute = "voice_cost_per_minute"
	TalkOvers          = "talk_overs"
)

var (
	_ schemas.Metric = (*voiceMetric)(nil)
)

// voiceMetric is one voice metric: a descriptor and a function of the
// analysed call.
type voiceMetric struct {
	d       schemas.MetricDescriptor
	compute func(rc *schemas.RunContext, ev *schemas.Evidence, a *voice.Analysis) schemas.Value
}

func (m *voiceMetric) GetName() string                      { return m.d.Name }
func (m *voiceMetric) GetVersion() string                   { return version }
func (m *voiceMetric) Descriptor() schemas.MetricDescriptor { return m.d }
func (m *voiceMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if rc == nil || !voice.IsVoiceTask(rc.Task) {
		return schemas.NotApplicable(), nil
	}
	a, v, ok := voiceAnalysis(rc, ev)
	if !ok {
		return v, nil
	}
	return m.compute(rc, ev, a), nil
}

var timelineCache sync.Map // sha256 of the timeline file -> *voice.Analysis

// voiceAnalysis reads the run's caller timeline, or the unknown value to return.
func voiceAnalysis(rc *schemas.RunContext, ev *schemas.Evidence) (*voice.Analysis, schemas.Value, bool) {
	spec := voice.Effective(rc.Task.Voice)
	var file *schemas.StateFile
	for i, f := range orEmpty(ev).State {
		if f.Service == spec.Caller && f.ID == voice.TimelineExport {
			file = &orEmpty(ev).State[i]
		}
	}
	switch {
	case file == nil:
		return nil, schemas.Unknown("the voice caller's timeline was not exported", schemas.EvidenceBoundary), false
	case file.Error != "":
		return nil, schemas.Unknown("the voice caller's timeline export failed: "+file.Error, schemas.EvidenceBoundary), false
	case rc.Dir == "":
		return nil, schemas.Unknown("no run folder to read the voice timeline from", schemas.EvidenceBoundary), false
	}
	key := fmt.Sprintf("%s|%d|%d", file.SHA256, spec.BargeInBoundMS, spec.DeadAirMS)
	if a, ok := timelineCache.Load(key); ok && file.SHA256 != "" {
		return a.(*voice.Analysis), schemas.Value{}, true
	}
	path, err := utils.ResolveInside(rc.Dir, file.Path)
	if err != nil {
		return nil, schemas.Unknown("voice timeline path: "+err.Error(), schemas.EvidenceBoundary), false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, schemas.Unknown("voice timeline: "+err.Error(), schemas.EvidenceBoundary), false
	}
	if file.SHA256 != "" && utils.SHA256Bytes(raw) != file.SHA256 {
		return nil, schemas.Unknown("voice timeline does not match its recorded sha256", schemas.EvidenceBoundary), false
	}
	events, err := voice.ParseTimeline(raw)
	if err != nil {
		return nil, schemas.Unknown("voice timeline: "+err.Error(), schemas.EvidenceBoundary), false
	}
	a, err := voice.Analyze(events, spec)
	if err != nil {
		return nil, schemas.Unknown("voice timeline: "+err.Error(), schemas.EvidenceBoundary), false
	}
	if file.SHA256 != "" {
		timelineCache.Store(key, a)
	}
	return a, schemas.Value{}, true
}

func latencyPercentile(p float64) func(*schemas.RunContext, *schemas.Evidence, *voice.Analysis) schemas.Value {
	return func(_ *schemas.RunContext, _ *schemas.Evidence, a *voice.Analysis) schemas.Value {
		l := a.Latencies()
		if len(l) == 0 {
			return schemas.Unknown("the agent answered no caller turn", schemas.EvidenceBoundary)
		}
		return schemas.Observed(math.Round(voice.Percentile(l, p)), schemas.EvidenceBoundary)
	}
}

// newVoiceMetric returns the named voice metric (one call per metric in Builtin).
func newVoiceMetric(name string) schemas.Metric {
	for _, m := range voiceMetrics() {
		if m.d.Name == name {
			return m
		}
	}
	panic("metrics: no voice metric " + name)
}

func voiceMetrics() []*voiceMetric {
	return []*voiceMetric{
		&voiceMetric{d: schemas.MetricDescriptor{
			Name: TimeToFirstAudioMS, Version: version, Label: "Time to first audio (ms)", Kind: schemas.KindCount,
			HigherIsBetter: false, MinEvidence: schemas.EvidenceBoundary,
			Description: "Milliseconds from the call connecting to the first voiced agent audio the caller played, from the voice caller's timeline, averaged over voice runs in which the agent spoke.",
		}, compute: func(_ *schemas.RunContext, _ *schemas.Evidence, a *voice.Analysis) schemas.Value {
			if a.FirstAudioMS == nil {
				return schemas.Unknown("the agent sent no voiced audio", schemas.EvidenceBoundary)
			}
			return schemas.Observed(math.Round(*a.FirstAudioMS), schemas.EvidenceBoundary)
		}},
		&voiceMetric{d: schemas.MetricDescriptor{
			Name: TurnLatencyP50MS, Version: version, Label: "Turn latency p50 (ms)", Kind: schemas.KindCount,
			HigherIsBetter: false, MinEvidence: schemas.EvidenceBoundary,
			Description: "Median milliseconds from the end of a caller line to the start of the agent's spoken answer, as the caller played it, from the voice caller's timeline, averaged over voice runs with at least one answered turn.",
		}, compute: latencyPercentile(50)},
		&voiceMetric{d: schemas.MetricDescriptor{
			Name: TurnLatencyP95MS, Version: version, Label: "Turn latency p95 (ms)", Kind: schemas.KindCount,
			HigherIsBetter: false, MinEvidence: schemas.EvidenceBoundary,
			Description: "95th percentile (linear interpolation) of the milliseconds from the end of a caller line to the start of the agent's spoken answer, from the voice caller's timeline, averaged over voice runs with at least one answered turn.",
		}, compute: latencyPercentile(95)},
		&voiceMetric{d: schemas.MetricDescriptor{
			Name: BargeInHandled, Version: version, Label: "Barge-in handled", Kind: schemas.KindRate,
			HigherIsBetter: true, MinEvidence: schemas.EvidenceBoundary,
			Description: "Share of the caller's interruptions after which the agent's played audio stopped within the task's bound (barge_in_bound_ms, default 1000), from the voice caller's timeline, averaged over voice runs in which the caller interrupted.",
		}, compute: func(_ *schemas.RunContext, _ *schemas.Evidence, a *voice.Analysis) schemas.Value {
			if len(a.BargeIns) == 0 {
				return schemas.NotApplicable()
			}
			n := 0
			for _, b := range a.BargeIns {
				if b.Handled {
					n++
				}
			}
			return schemas.Observed(float64(n)/float64(len(a.BargeIns)), schemas.EvidenceBoundary)
		}},
		&voiceMetric{d: schemas.MetricDescriptor{
			Name: TalkOvers, Version: version, Label: "Talk-overs per call", Kind: schemas.KindCount,
			HigherIsBetter: false, MinEvidence: schemas.EvidenceBoundary,
			Description: "Times per call the agent started speaking while the caller was still talking (not counting the caller's own interruptions), from the voice caller's timeline, averaged over voice runs whose call finished.",
		}, compute: func(_ *schemas.RunContext, _ *schemas.Evidence, a *voice.Analysis) schemas.Value {
			if !a.Complete {
				return schemas.Unknown("the voice timeline has no call_end: the call did not finish cleanly", schemas.EvidenceBoundary)
			}
			return schemas.Observed(float64(a.TalkOvers), schemas.EvidenceBoundary)
		}},
		&voiceMetric{d: schemas.MetricDescriptor{
			Name: DeadAirS, Version: version, Label: "Dead air (s)", Kind: schemas.KindDuration,
			HigherIsBetter: false, MinEvidence: schemas.EvidenceBoundary,
			Description: "Seconds of silence on the agent's floor (after the call connected or a caller line ended, before the agent spoke) summed over the gaps longer than the task's dead_air_ms (default 2000), from the voice caller's timeline, averaged over voice runs.",
		}, compute: func(_ *schemas.RunContext, _ *schemas.Evidence, a *voice.Analysis) schemas.Value {
			if !a.Complete {
				return schemas.Unknown("the voice timeline has no call_end: the call did not finish cleanly", schemas.EvidenceBoundary)
			}
			return schemas.Observed(math.Round(a.DeadAirMS)/1000, schemas.EvidenceBoundary)
		}},
		&voiceMetric{d: schemas.MetricDescriptor{
			Name: STTWER, Version: version, Label: "Agent STT word error rate", Kind: schemas.KindCount,
			HigherIsBetter: false, MinEvidence: schemas.EvidenceNative,
			Description: "Word error rate of the agent's transcription of the caller's known lines (edits over reference words, number words not normalized), from the gateway's transcription rows when the agent transcribed through Bifrost and otherwise from the agent's own reported transcripts (labelled agent_reported), averaged over voice runs with at least one transcribed caller line.",
		}, compute: computeWER},
		&voiceMetric{d: schemas.MetricDescriptor{
			Name: VoiceCostPerMinute, Version: version, Label: "Voice cost per minute (USD)", Kind: schemas.KindUSD,
			HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
			Description: "US dollars per minute of call, the run's cost ledger (model rows at pinned prices, speech, transcription and Realtime rows at Bifrost's own price) divided by the call's length from the voice caller's timeline, averaged over voice runs whose every row was priced.",
		}, compute: computeVoiceCost},
	}
}

func computeWER(rc *schemas.RunContext, ev *schemas.Evidence, a *voice.Analysis) schemas.Value {
	rows, err := voice.ReadAudioRows(filepath.Join(rc.Dir, voice.RawRowsFile))
	if err == nil {
		if hyps := voice.GatewayHypotheses(rows, a.WallStartMS); len(hyps) > 0 {
			if r, ok := voice.WER(a.Lines, hyps); ok {
				v := schemas.Observed(r.WER, schemas.EvidenceGateway)
				v.Source = schemas.EvidenceSourceBifrostLog
				return v
			}
		}
	}
	if hyps := voice.ReportedHypotheses(a); len(hyps) > 0 {
		if r, ok := voice.WER(a.Lines, hyps); ok {
			v := schemas.Observed(r.WER, schemas.EvidenceNative)
			v.Source = schemas.EvidenceSourceAgentReported
			return v
		}
	}
	return schemas.Unknown("no transcript of a caller line: the agent made no transcription call through the gateway and reported none", schemas.EvidenceNative)
}

func computeVoiceCost(_ *schemas.RunContext, ev *schemas.Evidence, a *voice.Analysis) schemas.Value {
	ev = orEmpty(ev)
	if a.CallMS <= 0 {
		return schemas.Unknown("the call has no length", schemas.EvidenceGateway)
	}
	if len(ev.Cost) == 0 {
		if log := ev.Requests; log != nil && log.Complete && len(log.Requests) == 0 {
			// As cost_usd: a witnessed complete log with no request is a
			// call that spent nothing.
			return schemas.Observed(0, schemas.EvidenceGateway)
		}
		return schemas.Unknown(noCostReason(ev.Requests), schemas.EvidenceGateway)
	}
	total := 0.0
	for _, row := range ev.Cost {
		if v, ok := row.USD.Float(); ok {
			total += v
		} else if row.USD.State != schemas.ValueNotApplicable {
			return schemas.Unknown(fmt.Sprintf("cost row %s (%s) is unpriced", row.RequestID, row.Model), schemas.EvidenceGateway)
		}
	}
	if v, cut := incompleteCost(ev.Requests, total); cut {
		return v
	}
	return schemas.Observed(total/(a.CallMS/60000), schemas.EvidenceGateway)
}
