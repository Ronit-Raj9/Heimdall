package metrics

import (
	"context"
	"math"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X7: GPU metrics (docs/gpu-tasks.md). Three read the run's hardware record,
// which the host-side sampler and the engine's clock fill while the agent
// runs (Run.Hardware): gpu_seconds, peak_vram_mb and gpu_util. Three read
// what the clean-room verifier measured on the GPU and wrote as reward
// dimensions of the same names: speedup, heldout_score and time_to_target_s.
// Every one is not applicable on a run that holds no GPU, unknown with the
// reason when its witness is missing, never zero.

// GPU metric names.
const (
	GPUSeconds     = "gpu_seconds"
	PeakVRAMMB     = "peak_vram_mb"
	GPUUtil        = "gpu_util"
	Speedup        = "speedup"
	HeldoutScore   = "heldout_score"
	TimeToTargetS  = "time_to_target_s"
	reasonNoGPURec = "the run of a GPU task has no hardware record"
)

// gpuMetric is one of the six GPU metrics; compute does the work.
type gpuMetric struct {
	d       schemas.MetricDescriptor
	compute func(rc *schemas.RunContext, ev *schemas.Evidence) schemas.Value
}

func (m *gpuMetric) GetName() string                      { return m.d.Name }
func (m *gpuMetric) GetVersion() string                   { return version }
func (m *gpuMetric) Descriptor() schemas.MetricDescriptor { return m.d }
func (m *gpuMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	return m.compute(rc, ev), nil
}

// gpuRun reports whether the run is a GPU run: its task asks for a GPU or
// its record says it held one.
func gpuRun(rc *schemas.RunContext) bool {
	if rc == nil {
		return false
	}
	return rc.Task.GPUCount() > 0 || (rc.Run != nil && rc.Run.Hardware != nil)
}

// sampler returns the run's sampler summary, or the value to report when
// there is none.
func sampler(rc *schemas.RunContext) (*schemas.GPUSamplerSummary, schemas.Value, bool) {
	if !gpuRun(rc) {
		return nil, schemas.NotApplicable(), false
	}
	if rc.Run == nil || rc.Run.Hardware == nil {
		return nil, schemas.Unknown(reasonNoGPURec, schemas.EvidenceOutcome), false
	}
	s := rc.Run.Hardware.Sampler
	if s == nil {
		return nil, schemas.Unknown("the GPU sampler was off or never ran (gpu.sample_ms, or the agent never started)", schemas.EvidenceOutcome), false
	}
	return s, schemas.Value{}, true
}

func newGPUSecondsMetric() *gpuMetric {
	return &gpuMetric{d: schemas.MetricDescriptor{Name: GPUSeconds, Version: version, Label: "GPU seconds", Kind: schemas.KindDuration,
		MinEvidence: schemas.EvidenceOutcome,
		Description: "Seconds the run held its GPUs while the agent ran times the number of GPUs, summed over sessions from the host-side sampler's clock (else the engine's), averaged over runs that held a GPU."},
		compute: func(rc *schemas.RunContext, _ *schemas.Evidence) schemas.Value {
			if s, v, ok := sampler(rc); ok && s.AgentSeconds > 0 {
				return schemas.Observed(s.GPUSeconds, schemas.EvidenceOutcome)
			} else if !gpuRun(rc) {
				return v
			}
			if rc.Run == nil || rc.Run.Hardware == nil || len(rc.Run.Hardware.GPUs) == 0 {
				return schemas.Unknown(reasonNoGPURec, schemas.EvidenceOutcome)
			}
			e := rc.Exec
			if e == nil || e.Started.IsZero() || e.Ended.IsZero() || e.Ended.Before(e.Started) {
				return schemas.Unknown("no agent start and end time", schemas.EvidenceOutcome)
			}
			return schemas.Observed(e.Ended.Sub(e.Started).Seconds()*float64(len(rc.Run.Hardware.GPUs)), schemas.EvidenceOutcome)
		}}
}

func newPeakVRAMMetric() *gpuMetric {
	return &gpuMetric{d: schemas.MetricDescriptor{Name: PeakVRAMMB, Version: version, Label: "Peak VRAM (MiB)", Kind: schemas.KindCount,
		MinEvidence: schemas.EvidenceOutcome,
		Description: "Highest GPU memory in use on any of the run's GPUs while the agent ran minus that GPU's reading just before it started, in MiB from host-side nvidia-smi samples, averaged over runs that held a GPU and were sampled."},
		compute: func(rc *schemas.RunContext, _ *schemas.Evidence) schemas.Value {
			s, v, ok := sampler(rc)
			switch {
			case !ok:
				return v
			case s.Samples == 0:
				return schemas.Unknown("no GPU sample while the agent ran: "+s.Error, schemas.EvidenceOutcome)
			case strings.Contains(s.Error, "baseline"):
				return schemas.Unknown("no baseline reading before the agent started: "+s.Error, schemas.EvidenceOutcome)
			}
			return schemas.Observed(s.PeakVRAMMB, schemas.EvidenceOutcome)
		}}
}

func newGPUUtilMetric() *gpuMetric {
	return &gpuMetric{d: schemas.MetricDescriptor{Name: GPUUtil, Version: version, Label: "GPU utilization", Kind: schemas.KindRate, HigherIsBetter: true,
		MinEvidence: schemas.EvidenceOutcome,
		Description: "Mean of nvidia-smi's utilization.gpu over every host-side sample of the run's GPUs while the agent ran, as a fraction, averaged over runs that held a GPU and were sampled."},
		compute: func(rc *schemas.RunContext, _ *schemas.Evidence) schemas.Value {
			s, v, ok := sampler(rc)
			switch {
			case !ok:
				return v
			case s.Samples == 0:
				return schemas.Unknown("no GPU sample while the agent ran: "+s.Error, schemas.EvidenceOutcome)
			}
			return schemas.Observed(math.Min(math.Max(s.MeanUtilPct/100, 0), 1), schemas.EvidenceOutcome)
		}}
}

// verifierDimension reads a number the clean-room verifier measured on the
// GPU and wrote as a reward dimension. It is not applicable on a run that
// holds no GPU and when a decided grade does not carry it (the verifier did
// not measure it for this task), unknown when the grade itself is missing or
// undecided.
func verifierDimension(name string) func(rc *schemas.RunContext, ev *schemas.Evidence) schemas.Value {
	return func(rc *schemas.RunContext, ev *schemas.Evidence) schemas.Value {
		if !gpuRun(rc) {
			return schemas.NotApplicable()
		}
		g := orEmpty(ev).Grade
		if v, bad := gradeProblem(g); bad {
			return v
		}
		if g.ResultFormat != schemas.VerifierResultReward {
			return schemas.NotApplicable()
		}
		v, ok := g.Dimensions[name]
		if !ok {
			return schemas.Value{State: schemas.ValueNotApplicable, Reason: "the verifier did not report " + name + " for this run"}
		}
		if !isFinite(v) {
			return schemas.Unknown("the verifier reported a non-finite "+name, schemas.EvidenceOutcome)
		}
		return schemas.Observed(v, schemas.EvidenceOutcome)
	}
}

func newSpeedupMetric() *gpuMetric {
	return &gpuMetric{d: schemas.MetricDescriptor{Name: Speedup, Version: version, Label: "Speedup", Kind: schemas.KindCount, HigherIsBetter: true,
		MinEvidence: schemas.EvidenceOutcome,
		Description: "Reference time over the agent's time as the clean-room verifier measured them on the run's GPU after warm-up (the median ratio of repeats, its interval in reward_speedup_lo and reward_speedup_hi), averaged over GPU runs whose verifier measured it."},
		compute: verifierDimension(Speedup)}
}

func newHeldoutScoreMetric() *gpuMetric {
	return &gpuMetric{d: schemas.MetricDescriptor{Name: HeldoutScore, Version: version, Label: "Held-out score", Kind: schemas.KindRate, HigherIsBetter: true,
		MinEvidence: schemas.EvidenceOutcome,
		Description: "The agent's model scored by the clean-room verifier on held-out data generated only on the verifier side, as a fraction, averaged over GPU runs whose verifier measured it."},
		compute: verifierDimension(HeldoutScore)}
}

func newTimeToTargetMetric() *gpuMetric {
	return &gpuMetric{d: schemas.MetricDescriptor{Name: TimeToTargetS, Version: version, Label: "Time to target", Kind: schemas.KindDuration,
		MinEvidence: schemas.EvidenceOutcome,
		Description: "Seconds the agent's training needed, run by the clean-room verifier on the run's GPU, to reach the task's target, averaged over GPU runs that reached it."},
		compute: verifierDimension(TimeToTargetS)}
}
