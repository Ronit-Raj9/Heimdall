package schemas

import "testing"

func TestRequestLogTally(t *testing.T) {
	tokens := &Tokens{Fresh: 10, Out: 2}
	seven := int64(7)
	tests := []struct {
		name string
		log  *RequestLog
		want *GatewayTally
	}{
		{"nil log", nil, nil},
		{"empty complete log", &RequestLog{Complete: true}, &GatewayTally{Complete: true}},
		{"error and non-message rows are left out", &RequestLog{Complete: true, Dropped: &seven, Requests: []GatewayRequest{
			{Kind: "message", Status: "success", Tokens: tokens},
			{Kind: "message", Status: "success"},
			{Kind: "message", Status: "processing"},
			{Kind: "message", Status: "error"},
			{Kind: "count_tokens", Status: "success"},
		}}, &GatewayTally{Requests: 3, WithUsage: 1, Complete: true, Dropped: &seven}},
		{"incomplete log keeps its rows", &RequestLog{Requests: []GatewayRequest{{Kind: "message", Status: "success", Tokens: tokens}}},
			&GatewayTally{Requests: 1, WithUsage: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.log.Tally()
			switch {
			case tt.want == nil && got != nil:
				t.Fatalf("got %+v, want nil", got)
			case tt.want == nil:
			case got == nil || got.Requests != tt.want.Requests || got.WithUsage != tt.want.WithUsage || got.Complete != tt.want.Complete ||
				(got.Dropped == nil) != (tt.want.Dropped == nil) || (got.Dropped != nil && *got.Dropped != *tt.want.Dropped):
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
