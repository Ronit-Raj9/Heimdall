package learning

import "testing"

func chain(id string, succ ...float64) []Point {
	var out []Point
	for i, s := range succ {
		out = append(out, Point{Chain: id, Index: i + 1, Task: string(rune('a' + i)), Success: s})
	}
	return out
}

func eq(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: nil, want %v", name, want)
	}
	if d := *got - want; d > 1e-4 || d < -1e-4 {
		t.Fatalf("%s: %v, want %v", name, *got, want)
	}
}

func TestCurveAndAULC(t *testing.T) {
	pts := append(chain("x", 0, 1, 1), chain("y", 0, 0, 1)...)
	c := Curve(pts)
	if len(c) != 3 || c[1].Success != 0.5 || c[1].Runs != 2 {
		t.Fatalf("curve %+v", c)
	}
	// trapezoids: (0+0.5)/2 + (0.5+1)/2 = 1.0 over span 2
	eq(t, "aulc", AULC(pts), 0.5)
	if AULC(nil) != nil {
		t.Fatal("aulc of nothing must be nil")
	}
	eq(t, "aulc single", AULC(chain("x", 0.7)), 0.7)
}

func TestFinalGain(t *testing.T) {
	eq(t, "gain", FinalGain(chain("x", 0, 0, 1, 1, 1, 1)), 1)
	if FinalGain(chain("x", 0, 1)) != nil {
		t.Fatal("two points: nil")
	}
}

func TestBWT(t *testing.T) {
	pts := chain("x", 1, 1, 0)
	if BWT(pts) != nil {
		t.Fatal("no replay: nil")
	}
	pts = append(pts, Point{Chain: "x", Index: 4, Task: "a", Success: 0, Replay: true}, Point{Chain: "x", Index: 5, Task: "b", Success: 1, Replay: true})
	eq(t, "bwt", BWT(pts), -0.5)
	if len(Curve(pts)) != 3 {
		t.Fatal("replay points stay out of the curve")
	}
}

func TestFWT(t *testing.T) {
	carry := chain("x", 0, 1, 1)
	fresh := chain("f", 0, 0, 1)
	eq(t, "fwt", FWT(carry, fresh), 0.5)
	if FWT(carry, nil) != nil {
		t.Fatal("no fresh: nil")
	}
}

func TestEpisodesToThreshold(t *testing.T) {
	got := EpisodesToThreshold(chain("x", 0, 1, 1, 1, 1), 0.8, 3)
	if got == nil || *got != 4 {
		t.Fatalf("got %v", got)
	}
	if EpisodesToThreshold(chain("x", 0, 0, 0), 0.8, 3) != nil {
		t.Fatal("never reached: nil")
	}
}

func TestSummarize(t *testing.T) {
	s := Summarize(append(chain("x", 0, 1, 1), chain("y", 1, 1, 1)...), nil, 0.8)
	if s.Chains != 2 || s.FWT != nil || s.AULC == nil || s.Threshold != 0.8 || len(s.Curve) != 3 {
		t.Fatalf("%+v", s)
	}
	if e := Summarize(nil, nil, 0.8); e.Curve == nil || e.AULC != nil {
		t.Fatalf("empty %+v", e)
	}
}
