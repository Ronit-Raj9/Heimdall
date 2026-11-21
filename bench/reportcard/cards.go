package reportcard

import (
	"math"
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Run-level metric ids the fault layers write into metrics.json. A run
// without one of them is unknown on that metric, never zero.
const (
	metricRecovered           = "recovered"
	metricTimeToRecover       = "time_to_recover_s"
	metricRetryAfterRespected = "retry_after_respected"
)

// ReliabilityCard is how repeatable one arm is on one benchmark's clean
// runs. Pass1 is the mean success rate; PassAtK and PassHatK are the
// unbiased pass@k and pass^k at K, the largest trial count every curve point
// reaches; Curve holds both for k = 1..K with task-cluster bootstrap bands.
// Consistency is stats.OutcomeConsistency (1: trials of a task always
// agree). FlipRate is the share of tasks whose trials disagreed.
// CostPerSolved is total cost over verified passes. A nil pointer is unknown.
type ReliabilityCard struct {
	K             int                `json:"k"`
	Tasks         int                `json:"tasks"`
	Pass1         *float64           `json:"pass1"`
	PassAtK       *float64           `json:"pass_at_k"`
	PassHatK      *float64           `json:"pass_hat_k"`
	Curve         []stats.CurvePoint `json:"curve"`
	Consistency   *float64           `json:"consistency"`
	FlipRate      *float64           `json:"flip_rate"`
	CostPerSolved *float64           `json:"cost_per_solved"`
}

// ResilienceArm is one arm under one fault against its own clean runs on
// the same tasks (paired by benchmark and task). RecoveryRate is
// P(success under fault | success clean); DegradationRatio is the fault
// success rate over the clean one; CostInflation is fault cost over clean
// cost on tasks with cost observed on both sides. Intervals are 95% paired
// task bootstraps. TimeToRecoverS is the median observed time_to_recover_s;
// RecoveredShare and RetryAfterRespected are shares of the runs that
// reported them. A nil pointer is unknown.
type ResilienceArm struct {
	Tasks               int      `json:"tasks"`
	SuccessClean        float64  `json:"success_clean"`
	SuccessFault        float64  `json:"success_fault"`
	RecoveryRate        *float64 `json:"recovery_rate"`
	DegradationRatio    *float64 `json:"degradation_ratio"`
	DegradationLow      *float64 `json:"degradation_ratio_low"`
	DegradationHigh     *float64 `json:"degradation_ratio_high"`
	CostInflation       *float64 `json:"cost_inflation"`
	CostInflationLow    *float64 `json:"cost_inflation_low"`
	CostInflationHigh   *float64 `json:"cost_inflation_high"`
	TimeToRecoverS      *float64 `json:"time_to_recover_s"`
	RecoveredShare      *float64 `json:"recovered_share"`
	RetryAfterRespected *float64 `json:"retry_after_respected"`
}

// Resilience is one fault's scorecard across arms.
type Resilience struct {
	Fault      string                   `json:"fault"`
	Benchmarks []string                 `json:"benchmarks"`
	Arms       map[string]ResilienceArm `json:"arms"`
}

func ptr(x float64) *float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return nil
	}
	r := round(x)
	return &r
}

// reliabilityCard builds the card from one arm's clean runs; nil when there
// are none.
func reliabilityCard(runs []graded) *ReliabilityCard {
	cs := clustersOf(runs, "")
	if len(cs) == 0 {
		return nil
	}
	succ := make([]int, len(cs))
	trials := make([]int, len(cs))
	kmax, total := 0, 0
	for i, c := range cs {
		succ[i], trials[i] = c.succ, c.runs
		kmax = max(kmax, c.runs)
		total += c.succ
	}
	curve := stats.PassCurve(succ, trials, kmax)
	for i := range curve {
		p := &curve[i]
		p.PassAt, p.PassAtLow, p.PassAtHigh = round(p.PassAt), round(p.PassAtLow), round(p.PassAtHigh)
		p.PassHat, p.PassHatLow, p.PassHatHi = round(p.PassHat), round(p.PassHatLow), round(p.PassHatHi)
	}
	card := &ReliabilityCard{Tasks: len(cs), Curve: curve,
		Pass1:       ptr(float64(total) / float64(len(runs))),
		Consistency: ptr(stats.OutcomeConsistency(succ, trials)),
		FlipRate:    ptr(stats.FlipRate(succ, trials)),
	}
	if len(curve) > 0 {
		last := curve[len(curve)-1]
		card.K = last.K
		card.PassAtK, card.PassHatK = ptr(last.PassAt), ptr(last.PassHat)
	}
	// Cost per solved needs every run's cost: a partial sum would read low.
	if cost := observed(runs, metricCostUSD); total > 0 && len(cost) == len(runs) {
		t := 0.0
		for _, x := range cost {
			t += x
		}
		card.CostPerSolved = ptr(t / float64(total))
	}
	return card
}

// taskSide is one task's runs under one condition.
type taskSide struct {
	succ, runs       int
	cost             float64
	costN            int
	recovered, rar   []float64
	timeToRecoverObs []float64
}

func (s *taskSide) add(r graded) {
	s.runs++
	s.succ += r.success
	if v, ok := r.metrics[metricCostUSD].Float(); ok {
		s.cost += v
		s.costN++
	}
	if v, ok := r.metrics[metricRecovered].Float(); ok {
		s.recovered = append(s.recovered, v)
	}
	if v, ok := r.metrics[metricRetryAfterRespected].Float(); ok {
		s.rar = append(s.rar, v)
	}
	if v, ok := r.metrics[metricTimeToRecover].Float(); ok {
		s.timeToRecoverObs = append(s.timeToRecoverObs, v)
	}
}

// resilience scores each fault per arm against the arm's clean runs on the
// same tasks. Fault ids come from faultOf, so any number of faults works.
func resilience(children []childRuns, labels []string, faultOf func(childRuns, string) string) []Resilience {
	type key struct{ fault, label string }
	// fault/label -> task key -> sides
	clean := map[string]map[string]*taskSide{} // label -> task -> clean side
	faulty := map[key]map[string]*taskSide{}
	benches := map[string][]string{}
	for _, cr := range children {
		for _, l := range labels {
			for _, r := range cr.arms[l] {
				tk := cr.child.Benchmark + "\x00" + r.task
				if r.condition == schemas.ConditionClean {
					if clean[l] == nil {
						clean[l] = map[string]*taskSide{}
					}
					if clean[l][tk] == nil {
						clean[l][tk] = &taskSide{}
					}
					clean[l][tk].add(r)
					continue
				}
				f := faultOf(cr, r.condition)
				if f == "" || f == Attack { // an attack is scored under safety, never as a fault
					continue
				}
				k := key{f, l}
				if faulty[k] == nil {
					faulty[k] = map[string]*taskSide{}
				}
				if faulty[k][tk] == nil {
					faulty[k][tk] = &taskSide{}
				}
				faulty[k][tk].add(r)
				if !slices.Contains(benches[f], cr.child.Benchmark) {
					benches[f] = append(benches[f], cr.child.Benchmark)
				}
			}
		}
	}
	faults := make([]string, 0, len(benches))
	for f := range benches {
		faults = append(faults, f)
	}
	slices.Sort(faults)
	out := make([]Resilience, 0, len(faults))
	for _, f := range faults {
		row := Resilience{Fault: f, Benchmarks: slices.Sorted(slices.Values(benches[f])), Arms: map[string]ResilienceArm{}}
		for _, l := range labels {
			if a, ok := resilienceArm(clean[l], faulty[key{f, l}]); ok {
				row.Arms[l] = a
			}
		}
		out = append(out, row)
	}
	return out
}

func resilienceArm(clean, fault map[string]*taskSide) (ResilienceArm, bool) {
	tasks := make([]string, 0, len(fault))
	for tk := range fault {
		if clean[tk] != nil && clean[tk].runs > 0 && fault[tk].runs > 0 {
			tasks = append(tasks, tk)
		}
	}
	if len(tasks) == 0 {
		return ResilienceArm{}, false
	}
	slices.Sort(tasks)
	var cRate, fRate, cCost, fCost, ttr, rec, rar []float64
	both := 0.0
	for _, tk := range tasks {
		c, fs := clean[tk], fault[tk]
		cr := float64(c.succ) / float64(c.runs)
		fr := float64(fs.succ) / float64(fs.runs)
		cRate, fRate = append(cRate, cr), append(fRate, fr)
		both += cr * fr
		if c.costN == c.runs && fs.costN == fs.runs {
			cCost = append(cCost, c.cost/float64(c.runs))
			fCost = append(fCost, fs.cost/float64(fs.runs))
		}
		ttr = append(ttr, fs.timeToRecoverObs...)
		rec = append(rec, fs.recovered...)
		rar = append(rar, fs.rar...)
	}
	sumC, sumF := total(cRate), total(fRate)
	n := float64(len(tasks))
	a := ResilienceArm{Tasks: len(tasks), SuccessClean: round(sumC / n), SuccessFault: round(sumF / n)}
	if sumC > 0 {
		// Within a task, trials are exchangeable, so P(fault pass and clean
		// pass) is c_i*f_i; conditioning on clean success weights by c_i.
		a.RecoveryRate = ptr(both / sumC)
		a.DegradationRatio = ptr(sumF / sumC)
		if lo, hi, ok := stats.BootstrapRatio(fRate, cRate, 0xdead); ok {
			a.DegradationLow, a.DegradationHigh = ptr(lo), ptr(hi)
		}
	}
	if sc := total(cCost); len(cCost) > 0 && sc > 0 {
		a.CostInflation = ptr(total(fCost) / sc)
		if lo, hi, ok := stats.BootstrapRatio(fCost, cCost, 0xc057); ok {
			a.CostInflationLow, a.CostInflationHigh = ptr(lo), ptr(hi)
		}
	}
	if len(ttr) > 0 {
		a.TimeToRecoverS = ptr(stats.Median(ttr))
	}
	if len(rec) > 0 {
		a.RecoveredShare = ptr(stats.Mean(rec))
	}
	if len(rar) > 0 {
		a.RetryAfterRespected = ptr(stats.Mean(rar))
	}
	return a, true
}

func total(xs []float64) float64 {
	t := 0.0
	for _, x := range xs {
		t += x
	}
	return t
}
