package main

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestPoolStatus(t *testing.T) {
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	rs := []schemas.RunnerView{
		{Pool: "p1", LastSeen: now.Add(-time.Hour)},
		{Pool: "p1", LastSeen: now.Add(-2 * time.Minute)},
		{Pool: "p2", LastSeen: now.Add(-time.Hour)},
	}
	if s := poolStatus(rs, "p1", now); !s.Known || !s.Online || s.LastSeen == nil || !s.LastSeen.Equal(now.Add(-2*time.Minute)) {
		t.Fatalf("p1 %+v", s)
	}
	if s := poolStatus(rs, "p2", now); !s.Known || s.Online {
		t.Fatalf("p2 %+v", s)
	}
	if s := poolStatus(rs, "none", now); s.Known {
		t.Fatalf("none %+v", s)
	}
}
