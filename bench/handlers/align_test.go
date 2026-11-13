package handlers

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// evs builds events from "type:target" specs with ids prefix:i.
func evs(prefix string, specs ...string) []schemas.Event {
	out := make([]schemas.Event, len(specs))
	for i, s := range specs {
		typ, target := s, ""
		for j := 0; j < len(s); j++ {
			if s[j] == ':' {
				typ, target = s[:j], s[j+1:]
				break
			}
		}
		out[i] = schemas.Event{EventID: prefix + ":" + string(rune('0'+i)), Type: schemas.EventType(typ), Target: target}
	}
	return out
}

func TestAlignPair(t *testing.T) {
	tests := []struct {
		name      string
		a, b      []schemas.Event
		rows      int
		matched   int
		first     int
		positions [][2]int // expected (A, B) per row; nil skips the check
	}{
		{
			name: "identical", a: evs("a", "tool_call:ls", "tool_call:pytest", "claim"),
			b: evs("b", "tool_call:ls", "tool_call:pytest", "claim"), rows: 3, matched: 3, first: -1,
			positions: [][2]int{{0, 0}, {1, 1}, {2, 2}},
		},
		{
			name: "insertion in B", a: evs("a", "tool_call:ls", "tool_call:pytest", "claim"),
			b: evs("b", "tool_call:ls", "tool_call:cat CLAUDE.md", "tool_call:pytest", "claim"), rows: 4, matched: 3, first: 1,
			positions: [][2]int{{0, 0}, {-1, 1}, {1, 2}, {2, 3}},
		},
		{
			name: "first divergence on a substitution", a: evs("a", "model_request", "tool_call:edit a.py", "tool_call:pytest"),
			b: evs("b", "model_request", "tool_call:edit b.py", "tool_call:pytest"), rows: 4, matched: 2, first: 1,
			positions: [][2]int{{0, 0}, {1, -1}, {-1, 1}, {2, 2}},
		},
		{
			name: "same target different type", a: evs("a", "tool_call:x"), b: evs("b", "tool_result:x"),
			rows: 2, matched: 0, first: 0,
		},
		{name: "both empty", rows: 0, matched: 0, first: -1},
		{name: "one empty", a: evs("a", "claim", "grade"), rows: 2, matched: 0, first: 0, positions: [][2]int{{0, -1}, {1, -1}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			al := AlignPair(tt.a, tt.b)
			if len(al.Positions) != tt.rows || al.Matched != tt.matched || al.FirstDivergence != tt.first {
				t.Fatalf("rows %d matched %d first %d, want %d %d %d: %+v",
					len(al.Positions), al.Matched, al.FirstDivergence, tt.rows, tt.matched, tt.first, al.Positions)
			}
			for i, want := range tt.positions {
				if got := [2]int{al.Positions[i].A, al.Positions[i].B}; got != want {
					t.Errorf("row %d = %v, want %v", i, got, want)
				}
			}
			steps, first := al.Steps("A", "B", tt.a, tt.b)
			if len(steps) != tt.rows {
				t.Fatalf("steps = %d, want %d", len(steps), tt.rows)
			}
			if (first == nil) != (tt.first < 0) || (first != nil && (first.Index != tt.first || first.Note != DivergenceNote)) {
				t.Errorf("first divergence = %+v, want index %d", first, tt.first)
			}
			for i, s := range steps {
				p := al.Positions[i]
				if (s.Events["A"] == nil) != (p.A < 0) || (s.Events["B"] == nil) != (p.B < 0) || s.Diverged == p.Matched {
					t.Errorf("step %d = %+v does not match position %+v", i, s, p)
				}
			}
		})
	}
}

func TestAlignPairDoesNotMutateInput(t *testing.T) {
	a := evs("a", "tool_call:x", "claim")
	b := evs("b", "claim")
	before := a[0]
	AlignPair(a, b)
	if a[0] != before {
		t.Fatal("AlignPair modified its input")
	}
}
