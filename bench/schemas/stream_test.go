package schemas

import (
	"testing"
	"time"
)

func TestStreamSpecValidate(t *testing.T) {
	arms := map[string]ArmSpec{"A": {}, "B": {}}
	good := []*StreamSpec{nil, {Memory: "carry"}, {Order: "seeded", Memory: "fresh", FreshArm: "B", Replay: 2, Threshold: 0.6}}
	for _, s := range good {
		if err := s.Validate(arms); err != nil {
			t.Fatalf("%+v: %v", s, err)
		}
	}
	bad := []*StreamSpec{{}, {Memory: "keep"}, {Memory: "carry", Order: "random"}, {Memory: "carry", FreshArm: "C"}, {Memory: "carry", Replay: -1}, {Memory: "carry", Threshold: 2}}
	for _, s := range bad {
		if err := s.Validate(arms); err == nil {
			t.Fatalf("%+v must be refused", s)
		}
	}
	s := &StreamSpec{Memory: "carry", FreshArm: "B"}
	if !s.Carries("A") || s.Carries("B") || (&StreamSpec{Memory: "fresh"}).Carries("A") || (*StreamSpec)(nil).Carries("A") {
		t.Fatal("Carries")
	}
	if s.ThresholdOrDefault() != DefaultStreamThreshold {
		t.Fatal("default threshold")
	}
}

func TestParseGap(t *testing.T) {
	cases := map[string]time.Duration{"7d": 7 * 24 * time.Hour, "36h": 36 * time.Hour, "90m": 90 * time.Minute, "1.5d": 36 * time.Hour}
	for in, want := range cases {
		if got, err := ParseGap(in); err != nil || got != want {
			t.Fatalf("%s: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"", "x", "-1d", "7w"} {
		if _, err := ParseGap(in); err == nil {
			t.Fatalf("%q must be refused", in)
		}
	}
}

func TestWorldSpec(t *testing.T) {
	w := &WorldSpec{StartDate: "2026-01-05", SessionGap: "7d", Events: []WorldEvent{{AfterSession: 1, Service: "shop", Action: "price_change"}, {AfterSession: 2, Service: "shop", Action: "ship_order"}}}
	svcs := map[string]bool{"shop": true}
	if err := w.Validate(3, svcs); err != nil {
		t.Fatal(err)
	}
	if err := w.Validate(1, svcs); err == nil {
		t.Fatal("one session must be refused")
	}
	if err := w.Validate(2, svcs); err == nil {
		t.Fatal("event after the last session must be refused")
	}
	if err := w.Validate(3, map[string]bool{}); err == nil {
		t.Fatal("unknown service must be refused")
	}
	d, err := w.SessionDate(2)
	if err != nil || d != "2026-01-19T00:00:00Z" {
		t.Fatalf("date %q %v", d, err)
	}
	if ev := w.EventsAfter(1); len(ev) != 1 || ev[0].Action != "price_change" {
		t.Fatalf("events %+v", ev)
	}
}

func TestProcessFaultValidate(t *testing.T) {
	if (&ProcessFault{AtModelCall: 3}).Validate() != nil || (&ProcessFault{AfterS: 10}).Validate() != nil {
		t.Fatal("good rule refused")
	}
	for _, p := range []*ProcessFault{nil, {}, {AtModelCall: -1, AfterS: 2}} {
		if p.Validate() == nil {
			t.Fatalf("%+v must be refused", p)
		}
	}
}
