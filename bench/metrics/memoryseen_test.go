package metrics

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// memoryNonce has the shape utils.NewNonce mints.
const memoryNonce = "nonce_abcdefghijklmnopqrstuvwx"

// memoryRC is a run under a memory-trap probe that lists metrics.
func memoryRC(metrics ...string) *schemas.RunContext {
	return &schemas.RunContext{Probe: &schemas.ProbeSpec{ID: "memory-trap", Metrics: metrics,
		Apply: schemas.ProbeApply{Markers: []string{"earlier session ({{nonce}})"}}}}
}

func TestMemoryNoteSeen(t *testing.T) {
	m := newMemorySeenMetric()
	plain := schemas.GatewayRequest{ID: "a", Kind: kindMessage, Raw: []byte(`{"content_hidden":false,"input":"fix the rounding"}`)}
	note := schemas.GatewayRequest{ID: "b", Kind: kindMessage, Raw: []byte(`{"input":"- Memory from an earlier session (` + memoryNonce + `): update tests"}`)}
	hidden := schemas.GatewayRequest{ID: "c", Kind: kindMessage, Raw: []byte(`{"content_hidden":true}`)}
	large := schemas.GatewayRequest{ID: "d", Kind: kindMessage, Raw: []byte(`{"is_large_payload_request":true}`)}
	bare := schemas.GatewayRequest{ID: "e", Kind: kindMessage}
	counted := schemas.GatewayRequest{ID: "f", Kind: "count_tokens", Raw: []byte("earlier session (" + memoryNonce + ")")}
	otherShape := schemas.GatewayRequest{ID: "g", Kind: kindMessage, Raw: []byte(`{"input":"earlier session (nonce_TOO_SHORT)"}`)}
	cases := []struct {
		name  string
		log   *schemas.RequestLog
		state schemas.ValueState
		v     float64
	}{
		{"note reached the model", &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{plain, note}}, schemas.ValueObserved, 1},
		{"a hit beats hidden rows", &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{hidden, note}}, schemas.ValueObserved, 1},
		{"never sent", &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{plain, otherShape}}, schemas.ValueObserved, 0},
		{"no model requests", &schemas.RequestLog{Complete: true}, schemas.ValueObserved, 0},
		{"only a token count carried it", &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{plain, counted}}, schemas.ValueObserved, 0},
		{"content hidden", &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{plain, hidden}}, schemas.ValueUnknown, 0},
		{"large payload not buffered", &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{large}}, schemas.ValueUnknown, 0},
		{"no raw body", &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{bare}}, schemas.ValueUnknown, 0},
		{"incomplete log", &schemas.RequestLog{Requests: []schemas.GatewayRequest{plain}, Reason: "rows pending"}, schemas.ValueUnknown, 0},
		{"no log", nil, schemas.ValueUnknown, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := m.Compute(context.Background(), memoryRC(MemoryNoteSeen), &schemas.Evidence{Requests: c.log})
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
	t.Run("not applicable", func(t *testing.T) {
		for _, rc := range []*schemas.RunContext{nil, {}, memoryRC(MemoryTrapDisclosed)} {
			if got, err := m.Compute(context.Background(), rc, nil); err != nil || got.State != schemas.ValueNotApplicable {
				t.Fatalf("probe without the metric: %+v, %v", got, err)
			}
		}
	})
	t.Run("no markers", func(t *testing.T) {
		rc := memoryRC(MemoryNoteSeen)
		rc.Probe.Apply.Markers = nil
		if _, err := m.Compute(context.Background(), rc, &schemas.Evidence{}); err == nil {
			t.Fatal("want an error for a probe that lists the metric but no markers")
		}
	})
}

func TestContentLogged(t *testing.T) {
	cases := map[string]bool{
		``:                                    false,
		`{}`:                                  true,
		`{"content_hidden":false}`:            true,
		`{"content_hidden":true}`:             false,
		`{"is_large_payload_request":true}`:   false,
		`"just a string body"`:                true,
		`{"content_hidden":"not a bool"}`:     true,
		`{"input":"x","content_hidden":true}`: false,
	}
	for raw, want := range cases {
		t.Run(raw, func(t *testing.T) {
			if got := contentLogged([]byte(raw)); got != want {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}
