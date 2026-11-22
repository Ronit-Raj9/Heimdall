package reportcard

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestBuildVoice: the voice section averages each voice metric per arm over
// the runs that observed it; an arm with no voice runs has no row.
func TestBuildVoice(t *testing.T) {
	src := newSource()
	src.add("e1", "voicebench", []string{"a", "b"}, []fixRun{
		{arm: "a", task: "t1", outcome: schemas.OutcomePass, metrics: map[string]float64{metricFirstAudio: 400, metricTurnP50: 900, metricTurnP95: 1500, metricDeadAir: 0, metricTalkOvers: 1}},
		{arm: "a", task: "t2", outcome: schemas.OutcomeFail, metrics: map[string]float64{metricFirstAudio: 600, metricTurnP50: 1100, metricTurnP95: 2500, metricDeadAir: 3, metricTalkOvers: 0}},
		{arm: "a", task: "t1", cond: "interrupt", outcome: schemas.OutcomePass, metrics: map[string]float64{metricBargeIn: 1}},
		pass("b", "t1", 1, nil),
	})
	rep, err := Build(context.Background(), src, abEval(schemas.EvaluationChild{Benchmark: "voicebench", ExperimentID: "e1"}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Voice) != 1 {
		t.Fatalf("voice rows %+v", rep.Voice)
	}
	v := rep.Voice[0]
	if v.Arm != "A" || v.TimeToFirstAudioMS.Value != 500 || v.TimeToFirstAudioMS.Runs != 2 || v.TurnLatencyP95MS.Value != 2000 ||
		v.DeadAirS.Value != 1.5 || v.TalkOvers.Value != 0.5 || v.BargeInHandled.Value != 1 || v.BargeInHandled.Runs != 1 || v.STTWER != nil {
		t.Fatalf("row %+v", v)
	}
}
