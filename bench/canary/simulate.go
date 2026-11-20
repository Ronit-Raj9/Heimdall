package canary

import (
	"fmt"
	"math"
	"math/rand/v2"
)

// SimConfig is a Monte Carlo study of the canary's alarm rule: per-task
// success drawn from a fixed spread of true rates, cost, tokens and latency
// with log-normal run noise, the same Compare and NextState the canary runs,
// and a confirmation look after every first flag.
type SimConfig struct {
	Tasks       int     // tasks in the canary
	Trials      int     // trials per task
	Triggers    int     // scheduled triggers per replication (a confirmation look is extra)
	Reps        int     // replications, each with a fresh baseline
	Sensitivity string  // low | normal | high
	DropPoints  float64 // true success drop on every task from the first trigger on, 0..1 (0: no drift)
	ShiftRel    float64 // true relative rise of latency from the first trigger on (0: none)
	RunNoise    float64 // log-normal sigma of cost, tokens and latency per run
	// Spread draws true per-task success rates over 0.15..0.85 instead of
	// 0.75..0.95. A canary should pin tasks its agent passes reliably
	// (docs/drift-canary.md); Spread is the pessimistic case.
	Spread bool
	Seed   uint64
}

// SimResult is what the study measured.
type SimResult struct {
	Config            SimConfig `json:"config"`
	Alpha             float64   `json:"alpha"`
	Triggers          int       `json:"triggers"`            // scheduled triggers simulated
	Looks             int       `json:"looks"`               // triggers plus confirmation looks
	Flags             int       `json:"flags"`               // looks that flagged
	Alarms            int       `json:"alarms"`              // transitions into drifted
	AlarmPerTrigger   float64   `json:"alarm_per_trigger"`   // alarms over triggers
	FlagPerLook       float64   `json:"flag_per_look"`       // the single-look flag rate, before confirmation
	ConfirmPerTrigger float64   `json:"confirm_per_trigger"` // extra confirmation looks per trigger (the cost overhead)
	AnyAlarm30        float64   `json:"any_alarm_30"`        // chance of at least one alarm in 30 daily triggers
	AnyAlarm365       float64   `json:"any_alarm_365"`       // the same over a year
	// Measured30 and Measured365 say the horizon chance above is the share
	// of replications that alarmed within it, which keeps the correlation
	// of looks sharing one baseline. False means the replications were
	// shorter than the horizon and the value is 1-(1-p)^N from the
	// per-trigger rate, which assumes independent triggers.
	Measured30  bool    `json:"any_alarm_30_measured"`
	Measured365 bool    `json:"any_alarm_365_measured"`
	DetectFirst float64 `json:"detect_first_trigger"` // with a drift: share of replications alarmed on the first trigger
	DetectBy3   float64 `json:"detect_by_3"`          // with a drift: alarmed within three triggers
}

// Simulate runs the study. With DropPoints or ShiftRel zero for both, every
// alarm is false and AlarmPerTrigger is the false-alarm rate per trigger.
func Simulate(c SimConfig) SimResult {
	if c.Tasks <= 0 {
		c.Tasks = 8
	}
	if c.Trials <= 0 {
		c.Trials = 1
	}
	if c.Triggers <= 0 {
		c.Triggers = 50
	}
	if c.Reps <= 0 {
		c.Reps = 200
	}
	if c.RunNoise <= 0 {
		c.RunNoise = 0.15
	}
	d := &Definition{Sensitivity: c.Sensitivity}
	res := SimResult{Config: c, Alpha: d.Alpha()}
	rng := rand.New(rand.NewPCG(c.Seed, 0x5eed))
	// True per-task rates evenly over 0.75..0.95 (reliable tasks), or over
	// 0.15..0.85 with Spread, where binary outcomes flip most often.
	lo, width := 0.75, 0.20
	if c.Spread {
		lo, width = 0.15, 0.70
	}
	truth := make([]float64, c.Tasks)
	for i := range truth {
		truth[i] = lo + width*float64(i)/math.Max(1, float64(c.Tasks-1))
	}
	labels := map[string]string{"A": "sim"}
	draw := func(drift bool) []RunObs {
		var out []RunObs
		for t := 0; t < c.Tasks; t++ {
			p := truth[t]
			shift := 1.0
			if drift {
				p = math.Max(0, p-c.DropPoints)
				shift = 1 + c.ShiftRel
			}
			for k := 0; k < c.Trials; k++ {
				s := 0.0
				if rng.Float64() < p {
					s = 1
				}
				cost := 0.02 * math.Exp(c.RunNoise*rng.NormFloat64())
				tok := 20000 * math.Exp(c.RunNoise*rng.NormFloat64())
				lat := 1500 * shift * math.Exp(c.RunNoise*rng.NormFloat64())
				out = append(out, RunObs{Task: fmt.Sprintf("t%02d", t), Arm: "A", Condition: "clean", Trial: k + 1,
					Success: &s, CostUSD: &cost, Tokens: &tok, LatencyMS: &lat})
			}
		}
		return out
	}
	drifting := c.DropPoints > 0 || c.ShiftRel > 0
	noAlarm30, noAlarm365, first, by3 := 0, 0, 0, 0
	for rep := 0; rep < c.Reps; rep++ {
		base := draw(false)
		state, flagged := "", []string(nil)
		alarmAt := -1
		look := func() bool {
			cmp := Compare(base, draw(drifting), labels, Metrics, res.Alpha, d.Floor)
			res.Looks++
			outcome := OutcomeClear
			if len(cmp.Flagged) > 0 {
				outcome = OutcomeFlagged
				res.Flags++
			}
			next := NextState(state, flagged, outcome, cmp.Flagged)
			alarm := next == StateDrifted && state != StateDrifted
			state, flagged = next, cmp.Flagged
			return alarm
		}
		for trig := 0; trig < c.Triggers; trig++ {
			res.Triggers++
			alarm := look()
			if !alarm && state == StateWatching {
				alarm = look()
				res.ConfirmPerTrigger++
			}
			if alarm {
				res.Alarms++
				if alarmAt < 0 {
					alarmAt = trig
				}
				// A team re-pins after an alarm: a fresh baseline.
				base, state, flagged = draw(drifting), "", nil
			}
		}
		if alarmAt < 0 || alarmAt >= 30 {
			noAlarm30++
		}
		if alarmAt < 0 || alarmAt >= 365 {
			noAlarm365++
		}
		if alarmAt == 0 {
			first++
		}
		if alarmAt >= 0 && alarmAt < 3 {
			by3++
		}
	}
	res.AlarmPerTrigger = float64(res.Alarms) / float64(res.Triggers)
	res.FlagPerLook = float64(res.Flags) / float64(res.Looks)
	res.ConfirmPerTrigger /= float64(res.Triggers)
	// Chance of at least one alarm over a horizon: measured as the share of
	// replications that alarmed within it when they are long enough, else
	// extrapolated from the per-trigger rate under independence (flagged by
	// Measured30 and Measured365).
	horizon := func(n, noAlarm int) (float64, bool) {
		if c.Triggers >= n {
			return 1 - float64(noAlarm)/float64(c.Reps), true
		}
		return 1 - math.Pow(1-res.AlarmPerTrigger, float64(n)), false
	}
	res.AnyAlarm30, res.Measured30 = horizon(30, noAlarm30)
	res.AnyAlarm365, res.Measured365 = horizon(365, noAlarm365)
	if drifting {
		res.DetectFirst = float64(first) / float64(c.Reps)
		res.DetectBy3 = float64(by3) / float64(c.Reps)
	}
	return res
}
