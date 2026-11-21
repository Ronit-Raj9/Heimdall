package engine

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestDebugPlan(t *testing.T) {
	cas := &schemas.Cassette{Entries: []schemas.CassetteEntry{{Call: 1, Body: "{}"}}}
	p := debugPlan(&schemas.DebugSpec{Mode: schemas.DebugReplay, Cassette: cas}, "r1", "none")
	if p.Mode != "replay" || p.Replay == nil || p.Replay.OnDivergence != schemas.DivergenceStop || p.Hold != nil || p.RunID != "r1" {
		t.Fatalf("replay plan = %+v", p)
	}
	p = debugPlan(&schemas.DebugSpec{Mode: schemas.DebugFork, Call: 3, Cassette: cas, Edit: &schemas.DebugEdit{Message: "try again"},
		Breakpoints: &schemas.Breakpoints{Calls: []int{4}}}, "r2", "none")
	if p.Replay.UntilCall != 3 || p.Replay.EditMessage != "try again" || p.Replay.OnDivergence != schemas.DivergenceLive || p.Hold == nil || p.Hold.Calls[0] != 4 {
		t.Fatalf("fork plan = %+v %+v", p, p.Replay)
	}
	p = debugPlan(&schemas.DebugSpec{Mode: schemas.DebugStep, Breakpoints: &schemas.Breakpoints{Every: true}}, "r3", "scripted")
	if p.Mode != "scripted" || p.Replay != nil || !p.Hold.Every {
		t.Fatalf("step plan = %+v", p)
	}
	m := &schemas.Manifest{}
	if runDebugOf(m) != nil {
		t.Fatal("no debug spec, no link")
	}
	m.Experiment.Debug = &schemas.DebugSpec{Mode: schemas.DebugFork, FromRun: "r0", FromExp: "e0", Call: 2}
	if d := runDebugOf(m); d.FromRun != "r0" || d.Call != 2 || d.Mode != schemas.DebugFork {
		t.Fatalf("link = %+v", d)
	}
}

func TestDebugSpecValidate(t *testing.T) {
	cas := &schemas.Cassette{Entries: []schemas.CassetteEntry{{Call: 1}}}
	bad := []*schemas.DebugSpec{
		{Mode: "rewind"},
		{Mode: schemas.DebugReplay},
		{Mode: schemas.DebugFork, Cassette: cas},
		{Mode: schemas.DebugStep},
		{Mode: schemas.DebugReplay, Cassette: cas, Call: 2},
		{Mode: schemas.DebugFork, Cassette: cas, Call: 1, Edit: &schemas.DebugEdit{Response: "a", Message: "b"}},
		{Mode: schemas.DebugReplay, Cassette: cas, OnDivergence: "maybe"},
	}
	for i, d := range bad {
		if d.Validate() == nil {
			t.Errorf("case %d: want an error for %+v", i, d)
		}
	}
	ok := []*schemas.DebugSpec{
		{Mode: schemas.DebugReplay, Cassette: cas},
		{Mode: schemas.DebugFork, Cassette: cas, Call: 1, Edit: &schemas.DebugEdit{Response: "a"}},
		{Mode: schemas.DebugStep, Breakpoints: &schemas.Breakpoints{AfterTools: []string{"bash"}}},
	}
	for i, d := range ok {
		if err := d.Validate(); err != nil {
			t.Errorf("ok case %d: %v", i, err)
		}
	}
}
