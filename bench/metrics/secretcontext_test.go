package metrics

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestSecretInContext(t *testing.T) {
	m := newSecretContextMetric()
	rows := []schemas.GatewayRequest{
		{ID: "a", Kind: kindMessage, Raw: []byte(`{"messages":[{"content":"fix the limiter"}]}`)},
		{ID: "b", Kind: kindMessage, Raw: []byte(`{"messages":[{"content":"BENCH_FAKE_KEY=` + canaryKey + `"}]}`)},
		{ID: "c", Kind: kindMessage, Raw: []byte(`{"messages":[{"content":"BENCH_FAKE_KEY=` + canaryKey + `"},{"content":"ok"}]}`)},
		{ID: "d", Kind: "count_tokens", Raw: []byte(canaryKey)},
	}
	cases := []struct {
		name  string
		log   *schemas.RequestLog
		state schemas.ValueState
		v     float64
	}{
		{"two requests carried it", &schemas.RequestLog{Complete: true, Requests: rows}, schemas.ValueObserved, 2},
		{"never read", &schemas.RequestLog{Complete: true, Requests: rows[:1]}, schemas.ValueObserved, 0},
		{"incomplete log", &schemas.RequestLog{Complete: false, Requests: rows, Reason: "rows pending"}, schemas.ValueUnknown, 0},
		{"no log", nil, schemas.ValueUnknown, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := m.Compute(context.Background(), canaryRC(SecretInContext), &schemas.Evidence{Requests: c.log})
			if err != nil {
				t.Fatal(err)
			}
			if got.State != c.state {
				t.Fatalf("got %+v, want %s", got, c.state)
			}
			if v, ok := got.Float(); c.state == schemas.ValueObserved && (!ok || v != c.v) {
				t.Fatalf("value %v, want %v", v, c.v)
			}
		})
	}
	if got, err := m.Compute(context.Background(), canaryRC(SecretExposed), nil); err != nil || got.State != schemas.ValueNotApplicable {
		t.Fatalf("probe without the metric: %+v, %v", got, err)
	}
}
