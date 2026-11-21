// Package learning computes learning-over-time metrics of stream runs
// (RESEARCH_R2 rec 9): the learning curve and its area (AULC), the gain from
// the start to the end of the stream, backward transfer from replay runs,
// forward transfer against a fresh-memory control, and how many episodes the
// agent needs to reach a success threshold.
//
// It is pure compute over points; it reads and writes nothing.
package learning

import (
	"cmp"
	"math"
	"slices"
)

// DefaultWindow is the rolling window EpisodesToThreshold averages over.
const DefaultWindow = 3

// Point is one graded run of a stream: its chain (arm, condition and trial),
// its 1-based place in the chain, its task and its verified success (0..1).
// Replay marks a replay run added at the end of the chain.
type Point struct {
	Chain   string
	Index   int
	Task    string
	Success float64
	Replay  bool
}

// CurvePoint is the mean success at one stream index over the chains that
// reached it.
type CurvePoint struct {
	Index   int     `json:"index"`
	Success float64 `json:"success"`
	Runs    int     `json:"runs"`
}

// Summary is the learning summary of one arm on one benchmark. A pointer
// field is nil when the runs cannot support it.
type Summary struct {
	AULC                *float64     `json:"aulc"`
	FinalGain           *float64     `json:"final_gain"`
	BWT                 *float64     `json:"bwt"`
	FWT                 *float64     `json:"fwt"`
	EpisodesToThreshold *int         `json:"episodes_to_threshold"`
	Threshold           float64      `json:"threshold"`
	Curve               []CurvePoint `json:"curve"`
	Chains              int          `json:"chains"`
}

// Summarize builds the summary of carry's points; fresh are the fresh-memory
// control's points (nil for none) and threshold the success rate
// episodes_to_threshold waits for.
func Summarize(carry, fresh []Point, threshold float64) Summary {
	s := Summary{Threshold: threshold, Curve: Curve(carry), Chains: chains(carry)}
	s.AULC = AULC(carry)
	s.FinalGain = FinalGain(carry)
	s.BWT = BWT(carry)
	s.FWT = FWT(carry, fresh)
	s.EpisodesToThreshold = EpisodesToThreshold(carry, threshold, DefaultWindow)
	if s.Curve == nil {
		s.Curve = []CurvePoint{}
	}
	return s
}

func chains(points []Point) int {
	seen := map[string]bool{}
	for _, p := range points {
		if !p.Replay {
			seen[p.Chain] = true
		}
	}
	return len(seen)
}

// Curve is the mean first-pass success per index across chains, in index
// order. Replay points are left out.
func Curve(points []Point) []CurvePoint {
	sum := map[int]float64{}
	n := map[int]int{}
	for _, p := range points {
		if p.Replay || p.Index < 1 {
			continue
		}
		sum[p.Index] += p.Success
		n[p.Index]++
	}
	var out []CurvePoint
	for i, c := range n {
		out = append(out, CurvePoint{Index: i, Success: round(sum[i] / float64(c)), Runs: c})
	}
	slices.SortFunc(out, func(a, b CurvePoint) int { return cmp.Compare(a.Index, b.Index) })
	return out
}

// AULC is the normalized area under the mean learning curve: the trapezoid
// area over the index span divided by the span, in 0..1. A curve with one
// index is its one value. Nil without first-pass points.
func AULC(points []Point) *float64 {
	c := Curve(points)
	switch len(c) {
	case 0:
		return nil
	case 1:
		return ptr(c[0].Success)
	}
	area := 0.0
	for i := 1; i < len(c); i++ {
		w := float64(c[i].Index - c[i-1].Index)
		area += w * (c[i].Success + c[i-1].Success) / 2
	}
	span := float64(c[len(c)-1].Index - c[0].Index)
	return ptr(area / span)
}

// FinalGain is the mean success of the last third of the curve minus the
// first third. Nil with fewer than three indexes.
func FinalGain(points []Point) *float64 {
	c := Curve(points)
	if len(c) < 3 {
		return nil
	}
	k := len(c) / 3
	return ptr(meanCurve(c[len(c)-k:]) - meanCurve(c[:k]))
}

func meanCurve(c []CurvePoint) float64 {
	t, n := 0.0, 0
	for _, p := range c {
		t += p.Success * float64(p.Runs)
		n += p.Runs
	}
	if n == 0 {
		return 0
	}
	return t / float64(n)
}

// BWT is backward transfer: the mean, over replay points, of the replay's
// success minus the same chain's first-pass success on that task. Negative
// is forgetting. Nil when no replay point has a first pass to compare.
func BWT(points []Point) *float64 {
	first := map[[2]string]float64{}
	for _, p := range points {
		if !p.Replay {
			k := [2]string{p.Chain, p.Task}
			if _, ok := first[k]; !ok {
				first[k] = p.Success
			}
		}
	}
	t, n := 0.0, 0
	for _, p := range points {
		if !p.Replay {
			continue
		}
		if f, ok := first[[2]string{p.Chain, p.Task}]; ok {
			t += p.Success - f
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return ptr(t / float64(n))
}

// FWT is forward transfer: the mean first-pass success of carry minus that
// of fresh, over the tasks both ran past the first index (the first task of
// a chain has no memory to transfer). Nil when fresh has no such task.
func FWT(carry, fresh []Point) *float64 {
	mean := func(points []Point) map[string]float64 {
		sum := map[string]float64{}
		n := map[string]int{}
		for _, p := range points {
			if p.Replay || p.Index <= 1 {
				continue
			}
			sum[p.Task] += p.Success
			n[p.Task]++
		}
		out := map[string]float64{}
		for t, c := range n {
			out[t] = sum[t] / float64(c)
		}
		return out
	}
	c, f := mean(carry), mean(fresh)
	t, n := 0.0, 0
	for task, cv := range c {
		if fv, ok := f[task]; ok {
			t += cv - fv
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return ptr(t / float64(n))
}

// EpisodesToThreshold is the first index at which the rolling mean of the
// curve over window indexes (fewer at the start is not enough) reaches
// threshold. Nil when it never does.
func EpisodesToThreshold(points []Point, threshold float64, window int) *int {
	if window < 1 {
		window = 1
	}
	c := Curve(points)
	for i := window - 1; i < len(c); i++ {
		if meanCurve(c[i-window+1:i+1]) >= threshold-1e-9 {
			idx := c[i].Index
			return &idx
		}
	}
	return nil
}

func ptr(v float64) *float64 {
	r := round(v)
	return &r
}

func round(v float64) float64 { return math.Round(v*1e4) / 1e4 }
