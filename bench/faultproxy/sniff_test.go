package faultproxy

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestSnifferSSE(t *testing.T) {
	stream := sseFirst + sseRest
	cases := []struct {
		name  string
		chunk int
	}{
		{"one write", len(stream)},
		{"byte at a time", 1},
		{"odd chunks", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sn := newSniffer(true)
			for i := 0; i < len(stream); i += tc.chunk {
				end := min(i+tc.chunk, len(stream))
				if n, err := sn.Write([]byte(stream[i:end])); err != nil || n != end-i {
					t.Fatalf("Write = %d, %v", n, err)
				}
			}
			id, tok := sn.result()
			want := schemas.Tokens{Fresh: 11, CacheRead: 5, CacheWrite: 7, Out: 42}
			if id != "msg_up123" || tok == nil || *tok != want {
				t.Fatalf("id %q tokens %+v", id, tok)
			}
		})
	}
}

func TestSnifferSkipsHugeLines(t *testing.T) {
	sn := newSniffer(true)
	huge := "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"" + strings.Repeat("x", maxSSELine+10) + "\"}}\n\n"
	for _, part := range []string{huge, sseFirst} {
		if _, err := sn.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if id, _ := sn.result(); id != "msg_up123" {
		t.Fatalf("id after huge line = %q", id)
	}
}

func TestSnifferJSON(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantID   string
		wantToks bool
	}{
		{"message", upstreamJSON, "msg_json9", true},
		{"truncated message", upstreamJSON[:40], "msg_json9", false},
		{"error envelope", `{"type":"error","error":{"type":"overloaded_error","message":"x"}}`, "", false},
		{"count tokens", `{"input_tokens":5}`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sn := newSniffer(false)
			if _, err := sn.Write([]byte(tc.body)); err != nil {
				t.Fatal(err)
			}
			id, tok := sn.result()
			if id != tc.wantID || (tok != nil) != tc.wantToks {
				t.Fatalf("id %q tokens %+v", id, tok)
			}
		})
	}
}
