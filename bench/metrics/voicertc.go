package metrics

import (
	"context"
	"os"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/voice"
)

// Real-time voice calls (docs/voice-realtime.md). audio_jitter_p95_ms reads
// the voice caller's timeline: how evenly the agent's audio arrived while
// it spoke, which is what a WebRTC or SIP leg (and the agent's own TTS
// streaming) adds to a call. Boundary evidence, like the other voice
// metrics: the bench's caller recorded every arrival.
const AudioJitterP95MS = "audio_jitter_p95_ms"

type audioJitterMetric struct{ _ byte }

func newAudioJitterMetric() schemas.Metric { return &audioJitterMetric{} }

func (*audioJitterMetric) GetName() string    { return AudioJitterP95MS }
func (*audioJitterMetric) GetVersion() string { return version }
func (*audioJitterMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: AudioJitterP95MS, Version: version, Label: "Audio jitter p95 (ms)", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceBoundary,
		Description: "95th percentile of how far each agent audio frame's arrival strayed from the previous frame's arrival plus its length, inside the agent's utterances (pauses of 200 ms or more are speech, not jitter), from the voice caller's timeline, averaged over voice runs in which the agent spoke.",
	}
}

func (*audioJitterMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if rc == nil || !voice.IsVoiceTask(rc.Task) {
		return schemas.NotApplicable(), nil
	}
	raw, v, ok := voiceTimelineRaw(rc, ev)
	if !ok {
		return v, nil
	}
	st, err := voice.ParseCallStats(raw)
	if err != nil {
		return schemas.Unknown("voice timeline: "+err.Error(), schemas.EvidenceBoundary), nil
	}
	if st.JitterP95MS == nil {
		return schemas.Unknown("the agent sent no audio inside an utterance", schemas.EvidenceBoundary), nil
	}
	return schemas.Observed(*st.JitterP95MS, schemas.EvidenceBoundary), nil
}

// voiceTimelineRaw reads the caller's exported timeline, checked against
// its recorded sha256, or the unknown value to return.
func voiceTimelineRaw(rc *schemas.RunContext, ev *schemas.Evidence) ([]byte, schemas.Value, bool) {
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
	return raw, schemas.Value{}, true
}
