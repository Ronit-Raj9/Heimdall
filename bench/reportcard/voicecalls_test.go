package reportcard

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func voiceRun(cond string, success int, first, p95 float64) graded {
	return graded{task: "t", condition: cond, success: success, metrics: map[string]schemas.Value{
		metricVoiceFirstAudio: schemas.Observed(first, schemas.EvidenceBoundary),
		metricVoiceTurnP95:    schemas.Observed(p95, schemas.EvidenceBoundary),
		metricVoiceJitterP95:  schemas.Observed(3, schemas.EvidenceBoundary),
		metricVoiceWER:        schemas.Observed(0.1, schemas.EvidenceBoundary),
	}}
}

func TestVoiceCallRows(t *testing.T) {
	text := graded{task: "x", condition: "persona-hurried", success: 1, metrics: map[string]schemas.Value{metricVoiceFirstAudio: schemas.NotApplicable()}}
	children := []childRuns{{arms: map[string][]graded{
		"a": {voiceRun(schemas.ConditionClean, 1, 800, 1200), voiceRun(schemas.ConditionClean, 1, 600, 1000),
			voiceRun("persona-hurried", 0, 900, 2400), voiceRun("noise", 1, 700, 1300), text},
	}}}
	rows := voiceCallRows(children, []string{"a"})
	if len(rows) != 3 {
		t.Fatalf("rows %+v", rows)
	}
	if rows[0].Condition != schemas.ConditionClean || rows[1].Condition != "noise" || rows[2].Condition != "persona-hurried" {
		t.Fatalf("order %v %v %v", rows[0].Condition, rows[1].Condition, rows[2].Condition)
	}
	if rows[0].Runs != 2 || rows[0].SuccessRate != 1 || rows[0].TimeToFirstAudioMS.Value != 700 || rows[0].AudioJitterP95MS.Value != 3 || rows[0].WER == nil || rows[0].WER.Value != 0.1 {
		t.Fatalf("clean %+v", rows[0])
	}
	if rows[2].Runs != 1 || rows[2].SuccessRate != 0 || rows[2].TurnLatencyP95MS.Value != 2400 {
		t.Fatalf("persona %+v", rows[2])
	}
	if rows[0].BargeInHandled != nil {
		t.Fatal("no barge-in observed, none shown")
	}
	clean := []childRuns{{arms: map[string][]graded{"a": {voiceRun(schemas.ConditionClean, 1, 800, 1200)}}}}
	if got := voiceCallRows(clean, []string{"a"}); got != nil {
		t.Fatalf("clean only: %+v", got)
	}
}
