package monitorcert

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestAlwaysAllowMonitor(t *testing.T) {
	tests := []struct {
		name string
		n    int
	}{{"one", 1}, {"several", 5}, {"none", 0}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resps, err := newAlwaysAllowMonitor().Score(context.Background(), make([]schemas.MonitorRequest, tc.n))
			if err != nil || len(resps) != tc.n {
				t.Fatalf("%d answers, err %v", len(resps), err)
			}
			for _, r := range resps {
				if r.Decision != schemas.MonitorAllow || r.Score == nil || *r.Score != 0 {
					t.Errorf("answer %+v", r)
				}
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newAlwaysAllowMonitor().Score(ctx, make([]schemas.MonitorRequest, 1)); err == nil {
		t.Error("cancelled context accepted")
	}
}
