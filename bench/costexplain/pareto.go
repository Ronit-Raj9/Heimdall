package costexplain

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Pareto x axes.
const (
	XCostPerSuccess = "cost_per_success" // total cost over verified successes, failures included
	XCostPerRun     = "cost_per_run"     // median cost per run, as the board reports it
)

// Metric ids of a leaderboard entry the plane reads.
const (
	entryCostPerSuccess = "cost_per_success"
	entryCostUSD        = "cost_usd"
)

// Pareto is every config on the cost against verified success plane with
// the frontier marked (GET .../pareto): a config is on the frontier when no
// other config is at least as cheap and at least as successful, and better
// on one of them. The frontier is drawn from point estimates; the success
// interval on each point says how far apart two points really are.
type Pareto struct {
	X        string        `json:"x"` // cost_per_success | cost_per_run
	XLabel   string        `json:"x_label"`
	Y        string        `json:"y"` // verified_success
	Points   []ParetoPoint `json:"points"`
	Frontier []string      `json:"frontier"` // ids on the frontier, cheapest first
	Note     string        `json:"note"`
}

// ParetoPoint is one config. X and Y are the values the board reports; Y
// carries the success interval as its bounds. A point whose cost or success
// is not observed is listed with Placed false and the reason.
type ParetoPoint struct {
	ID          string        `json:"id"` // arm id on a matrix, config hash on the board
	Label       string        `json:"label"`
	Agent       string        `json:"agent"`
	Model       string        `json:"model"`
	X           schemas.Value `json:"x"`
	Y           schemas.Value `json:"y"`
	Placed      bool          `json:"placed"`
	Frontier    bool          `json:"frontier"`
	DominatedBy []string      `json:"dominated_by"` // frontier ids that are at least as cheap and as successful
	Reason      string        `json:"reason"`       // why the point is not placed, or empty
}

// ParetoInput is one config to place.
type ParetoInput struct {
	ID, Label, Agent, Model string
	Entry                   schemas.LeaderboardEntry
}

// ParetoFromEntries places leaderboard entries (a board's entries or a
// matrix's rows) on the plane with x as the cost axis. It is pure.
func ParetoFromEntries(in []ParetoInput, x string) (*Pareto, error) {
	x = cmp.Or(strings.TrimSpace(x), XCostPerSuccess)
	out := &Pareto{X: x, Y: "verified_success", Points: []ParetoPoint{}, Frontier: []string{}}
	switch x {
	case XCostPerSuccess:
		out.XLabel = "Cost per verified success (USD, failures included)"
	case XCostPerRun:
		out.XLabel = "Median cost per run (USD)"
	default:
		return nil, fmt.Errorf("%w: x %q is not %s or %s", ErrBadQuery, x, XCostPerSuccess, XCostPerRun)
	}
	for _, p := range in {
		pt := ParetoPoint{ID: p.ID, Label: p.Label, Agent: p.Agent, Model: p.Model, Y: p.Entry.Success, DominatedBy: []string{}}
		if x == XCostPerSuccess {
			pt.X = p.Entry.Metrics[entryCostPerSuccess]
		} else {
			pt.X = p.Entry.Metrics[entryCostUSD]
		}
		out.Points = append(out.Points, pt)
	}
	out.Points = Frontier(out.Points)
	for _, p := range out.Points {
		if p.Frontier {
			out.Frontier = append(out.Frontier, p.ID)
		}
	}
	placed := 0
	for _, p := range out.Points {
		if p.Placed {
			placed++
		}
	}
	out.Note = fmt.Sprintf("%d of %d configs placed; %d on the frontier. The frontier is read from point estimates: where success intervals overlap, the order between two points is not established.",
		placed, len(out.Points), len(out.Frontier))
	return out, nil
}

// Frontier marks which points are placed (an observed, finite cost and
// success) and which placed points are on the frontier (minimum cost,
// maximum success). Each dominated point lists the frontier points that
// dominate it. Points come back sorted by cost, then success descending,
// then id; unplaced points last. It is pure.
func Frontier(points []ParetoPoint) []ParetoPoint {
	out := slices.Clone(points)
	for i := range out {
		p := &out[i]
		p.Frontier, p.DominatedBy = false, []string{}
		x, okX := p.X.Float()
		y, okY := p.Y.Float()
		switch {
		case !okX && p.X.State == schemas.ValueNotApplicable:
			p.Placed, p.Reason = false, "no cost on this axis: "+cmp.Or(p.X.Reason, "not applicable")
		case !okX || math.IsNaN(x) || math.IsInf(x, 0):
			p.Placed, p.Reason = false, "cost unknown: "+cmp.Or(p.X.Reason, "not observed")
		case !okY || math.IsNaN(y) || math.IsInf(y, 0):
			p.Placed, p.Reason = false, "success unknown: "+cmp.Or(p.Y.Reason, "not observed")
		default:
			p.Placed, p.Reason = true, ""
		}
	}
	for i := range out {
		if !out[i].Placed {
			continue
		}
		xi, _ := out[i].X.Float()
		yi, _ := out[i].Y.Float()
		dominated := false
		for j := range out {
			if i == j || !out[j].Placed {
				continue
			}
			xj, _ := out[j].X.Float()
			yj, _ := out[j].Y.Float()
			if xj <= xi && yj >= yi && (xj < xi || yj > yi) {
				dominated = true
				break
			}
		}
		out[i].Frontier = !dominated
	}
	for i := range out {
		if !out[i].Placed || out[i].Frontier {
			continue
		}
		xi, _ := out[i].X.Float()
		yi, _ := out[i].Y.Float()
		for j := range out {
			if !out[j].Frontier {
				continue
			}
			xj, _ := out[j].X.Float()
			yj, _ := out[j].Y.Float()
			if xj <= xi && yj >= yi {
				out[i].DominatedBy = append(out[i].DominatedBy, out[j].ID)
			}
		}
		slices.Sort(out[i].DominatedBy)
	}
	slices.SortStableFunc(out, func(a, b ParetoPoint) int {
		if a.Placed != b.Placed {
			return compareTrueFirst(a.Placed, b.Placed)
		}
		if !a.Placed {
			return cmp.Compare(a.ID, b.ID)
		}
		xa, _ := a.X.Float()
		xb, _ := b.X.Float()
		ya, _ := a.Y.Float()
		yb, _ := b.Y.Float()
		return cmp.Or(cmp.Compare(xa, xb), cmp.Compare(yb, ya), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// ParetoFromMatrix places every arm of a model matrix, by arm id.
func ParetoFromMatrix(mx *schemas.ModelMatrix, x string) (*Pareto, error) {
	in := make([]ParetoInput, 0, len(mx.Rows))
	for _, r := range mx.Rows {
		in = append(in, ParetoInput{ID: r.Arm, Label: cmp.Or(r.Label, r.Arm), Agent: r.Agent, Model: r.Model, Entry: r.LeaderboardEntry})
	}
	return ParetoFromEntries(in, x)
}

// ParetoFromLeaderboard places every entry of a board, by config hash.
func ParetoFromLeaderboard(lb *schemas.Leaderboard, x string) (*Pareto, error) {
	in := make([]ParetoInput, 0, len(lb.Entries))
	for _, e := range lb.Entries {
		in = append(in, ParetoInput{ID: e.ConfigHash, Label: cmp.Or(e.Label, e.Agent), Agent: e.Agent, Model: e.Model, Entry: e})
	}
	return ParetoFromEntries(in, x)
}
