package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X7: GPU metrics (gpu.go).

func gpuRC(h *schemas.HardwareRecord) *schemas.RunContext {
	return &schemas.RunContext{Task: &schemas.Task{ID: "t", Resources: &schemas.Resources{GPU: &schemas.GPURequest{Count: 1}}}, Run: &schemas.Run{Hardware: h}}
}

func value(t *testing.T, m schemas.Metric, rc *schemas.RunContext, ev *schemas.Evidence) schemas.Value {
	t.Helper()
	v, err := m.Compute(context.Background(), rc, ev)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestGPUHardwareMetrics(t *testing.T) {
	gtx := []schemas.GPUInfo{{UUID: "GPU-a", MemoryMB: 4096}}
	sampled := gpuRC(&schemas.HardwareRecord{GPUs: gtx, Sampler: &schemas.GPUSamplerSummary{Samples: 10, AgentSeconds: 12, GPUSeconds: 12, BaselineMB: 52, PeakUsedMB: 1052, PeakVRAMMB: 1000, MeanUtilPct: 40}})
	cpu := &schemas.RunContext{Task: &schemas.Task{ID: "t"}, Run: &schemas.Run{}}
	cases := []struct {
		m     schemas.Metric
		rc    *schemas.RunContext
		state schemas.ValueState
		want  float64
	}{
		{newGPUSecondsMetric(), sampled, schemas.ValueObserved, 12},
		{newPeakVRAMMetric(), sampled, schemas.ValueObserved, 1000},
		{newGPUUtilMetric(), sampled, schemas.ValueObserved, 0.4},
		{newGPUSecondsMetric(), cpu, schemas.ValueNotApplicable, 0},
		{newPeakVRAMMetric(), cpu, schemas.ValueNotApplicable, 0},
		{newGPUUtilMetric(), gpuRC(nil), schemas.ValueUnknown, 0},
		{newPeakVRAMMetric(), gpuRC(&schemas.HardwareRecord{GPUs: gtx}), schemas.ValueUnknown, 0},
		{newGPUUtilMetric(), gpuRC(&schemas.HardwareRecord{GPUs: gtx, Sampler: &schemas.GPUSamplerSummary{Error: "no sample"}}), schemas.ValueUnknown, 0},
		{newPeakVRAMMetric(), gpuRC(&schemas.HardwareRecord{GPUs: gtx, Sampler: &schemas.GPUSamplerSummary{Samples: 3, PeakUsedMB: 900, PeakVRAMMB: 0, Error: "baseline reading: exit 9"}}), schemas.ValueUnknown, 0},
	}
	for i, c := range cases {
		v := value(t, c.m, c.rc, nil)
		if v.State != c.state {
			t.Errorf("%d %s: state %s, want %s (%s)", i, c.m.GetName(), v.State, c.state, v.Reason)
			continue
		}
		if f, ok := v.Float(); ok && f != c.want {
			t.Errorf("%d %s = %v, want %v", i, c.m.GetName(), f, c.want)
		}
	}
	// Without a sampler the engine's clock times the agent, per GPU.
	now := time.Now()
	clock := gpuRC(&schemas.HardwareRecord{GPUs: append(gtx, schemas.GPUInfo{UUID: "GPU-b"})})
	clock.Exec = &schemas.ExecResult{Started: now, Ended: now.Add(5 * time.Second)}
	if f, ok := value(t, newGPUSecondsMetric(), clock, nil).Float(); !ok || f != 10 {
		t.Errorf("gpu_seconds from the engine clock = %v %v, want 10", f, ok)
	}
}

func TestGPUVerifierMetrics(t *testing.T) {
	reward := 1.0
	ev := &schemas.Evidence{Grade: &schemas.Grade{ResultFormat: schemas.VerifierResultReward, Reward: &reward, Dimensions: map[string]float64{"speedup": 3.5, "heldout_score": 0.92}}}
	rc := gpuRC(&schemas.HardwareRecord{})
	if f, ok := value(t, newSpeedupMetric(), rc, ev).Float(); !ok || f != 3.5 {
		t.Errorf("speedup %v", f)
	}
	if f, ok := value(t, newHeldoutScoreMetric(), rc, ev).Float(); !ok || f != 0.92 {
		t.Errorf("heldout_score %v", f)
	}
	if v := value(t, newTimeToTargetMetric(), rc, ev); v.State != schemas.ValueNotApplicable || v.Reason == "" {
		t.Errorf("a decided grade without the dimension: %+v", v)
	}
	if v := value(t, newSpeedupMetric(), rc, &schemas.Evidence{}); v.State != schemas.ValueUnknown {
		t.Errorf("no grade must be unknown: %+v", v)
	}
	if v := value(t, newSpeedupMetric(), &schemas.RunContext{Task: &schemas.Task{}}, ev); v.State != schemas.ValueNotApplicable {
		t.Errorf("a CPU run: %+v", v)
	}
}
