package reportcard

import (
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Voice calls by condition (docs/voice-realtime.md): each arm's task
// success, graded on the call's transcript by the benchmark's own
// verifier, and its call quality under each condition of the voice
// benchmarks: clean, every caller persona (accent, pace, interruptions,
// noise, phone line) and every voice probe the evaluation ran. A voice run
// is one that measured time to first audio or turn latency.

// Voice metric ids beyond the per-arm Voice section's (metrics/voicertc.go).
const (
	metricVoiceFirstAudio = "time_to_first_audio_ms"
	metricVoiceTurnP95    = "turn_latency_p95_ms"
	metricVoiceBargeIn    = "barge_in_handled"
	metricVoiceJitterP95  = "audio_jitter_p95_ms"
	metricVoiceWER        = "stt_wer"
)

// VoiceCallRow is one arm under one condition.
type VoiceCallRow struct {
	Arm                string  `json:"arm"`
	Condition          string  `json:"condition"`
	Runs               int     `json:"runs"`
	Successes          int     `json:"successes"`
	SuccessRate        float64 `json:"success_rate"`
	TimeToFirstAudioMS *Rate   `json:"time_to_first_audio_ms,omitempty"`
	TurnLatencyP95MS   *Rate   `json:"turn_latency_p95_ms,omitempty"`
	BargeInHandled     *Rate   `json:"barge_in_handled,omitempty"`
	AudioJitterP95MS   *Rate   `json:"audio_jitter_p95_ms,omitempty"`
	WER                *Rate   `json:"stt_wer,omitempty"`
}

func isVoiceRun(r graded) bool {
	for _, id := range []string{metricVoiceFirstAudio, metricVoiceTurnP95} {
		if v, ok := r.metrics[id]; ok && v.State != schemas.ValueNotApplicable && v.State != "" {
			return true
		}
	}
	return false
}

// voiceCallRows lists the rows arm by arm, clean first and then the other
// conditions in name order; nil when no run was a voice call or every voice
// run was clean (the Voice section already covers that).
func voiceCallRows(children []childRuns, labels []string) []VoiceCallRow {
	var out []VoiceCallRow
	nonClean := false
	for _, l := range labels {
		byCond := map[string][]graded{}
		for _, cr := range children {
			for _, r := range cr.arms[l] {
				if r.replay || !isVoiceRun(r) {
					continue
				}
				byCond[r.condition] = append(byCond[r.condition], r)
			}
		}
		conds := make([]string, 0, len(byCond))
		for c := range byCond {
			conds = append(conds, c)
			if c != schemas.ConditionClean {
				nonClean = true
			}
		}
		slices.SortFunc(conds, func(a, b string) int {
			switch {
			case a == b:
				return 0
			case a == schemas.ConditionClean:
				return -1
			case b == schemas.ConditionClean:
				return 1
			case a < b:
				return -1
			}
			return 1
		})
		for _, c := range conds {
			runs := byCond[c]
			n := successes(runs)
			out = append(out, VoiceCallRow{Arm: l, Condition: c, Runs: len(runs), Successes: n,
				SuccessRate:        round(float64(n) / float64(len(runs))),
				TimeToFirstAudioMS: meanRate(runs, metricVoiceFirstAudio),
				TurnLatencyP95MS:   meanRate(runs, metricVoiceTurnP95),
				BargeInHandled:     meanRate(runs, metricVoiceBargeIn),
				AudioJitterP95MS:   meanRate(runs, metricVoiceJitterP95),
				WER:                meanRate(runs, metricVoiceWER),
			})
		}
	}
	if !nonClean {
		return nil
	}
	return out
}
