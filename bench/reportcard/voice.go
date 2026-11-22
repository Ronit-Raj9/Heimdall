package reportcard

// Voice section of the report card: each arm's call quality on the voice
// benchmarks, from the voice metrics (metrics/voice.go) every voice run
// computes on the bench's caller timeline. Means over the runs that
// observed each metric; a metric no run observed is nil.

// Voice metric ids (metrics/voice.go; reportcard does not import metrics).
const (
	metricFirstAudio   = "time_to_first_audio_ms"
	metricTurnP50      = "turn_latency_p50_ms"
	metricTurnP95      = "turn_latency_p95_ms"
	metricBargeIn      = "barge_in_handled"
	metricDeadAir      = "dead_air_s"
	metricTalkOvers    = "talk_overs"
	metricSTTWER       = "stt_wer"
	metricVoiceCostMin = "voice_cost_per_minute"
)

// VoiceRow is one arm's voice numbers.
type VoiceRow struct {
	Arm                string `json:"arm"`
	TimeToFirstAudioMS *Rate  `json:"time_to_first_audio_ms,omitempty"`
	TurnLatencyP50MS   *Rate  `json:"turn_latency_p50_ms,omitempty"`
	TurnLatencyP95MS   *Rate  `json:"turn_latency_p95_ms,omitempty"`
	BargeInHandled     *Rate  `json:"barge_in_handled,omitempty"`
	DeadAirS           *Rate  `json:"dead_air_s,omitempty"`
	TalkOvers          *Rate  `json:"talk_overs,omitempty"`
	STTWER             *Rate  `json:"stt_wer,omitempty"`
	CostPerMinuteUSD   *Rate  `json:"voice_cost_per_minute,omitempty"`
}

func voiceRows(children []childRuns, labels []string) []VoiceRow {
	var out []VoiceRow
	for _, l := range labels {
		var runs []graded
		for _, cr := range children {
			runs = append(runs, cr.arms[l]...)
		}
		row := VoiceRow{Arm: l,
			TimeToFirstAudioMS: meanRate(runs, metricFirstAudio),
			TurnLatencyP50MS:   meanRate(runs, metricTurnP50),
			TurnLatencyP95MS:   meanRate(runs, metricTurnP95),
			BargeInHandled:     meanRate(runs, metricBargeIn),
			DeadAirS:           meanRate(runs, metricDeadAir),
			TalkOvers:          meanRate(runs, metricTalkOvers),
			STTWER:             meanRate(runs, metricSTTWER),
			CostPerMinuteUSD:   meanRate(runs, metricVoiceCostMin),
		}
		for _, r := range []*Rate{row.TimeToFirstAudioMS, row.TurnLatencyP50MS, row.TurnLatencyP95MS, row.BargeInHandled, row.DeadAirS, row.TalkOvers, row.STTWER, row.CostPerMinuteUSD} {
			if r != nil {
				out = append(out, row)
				break
			}
		}
	}
	return out
}
