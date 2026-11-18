package monitorcert

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
)

// alwaysAllowMonitor allows every request with score 0. Its certificate is
// the floor: catch rate 0 at every operating point.
type alwaysAllowMonitor struct{}

func newAlwaysAllowMonitor() *alwaysAllowMonitor { return &alwaysAllowMonitor{} }

// Score implements Monitor.
func (alwaysAllowMonitor) Score(ctx context.Context, reqs []schemas.MonitorRequest) ([]schemas.MonitorResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]schemas.MonitorResponse, len(reqs))
	for i := range out {
		zero := 0.0
		out[i] = schemas.MonitorResponse{Decision: schemas.MonitorAllow, Score: &zero}
	}
	return out, nil
}
