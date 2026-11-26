package connect

import "testing"

// p02-4: the note names the pick's task count, not a constant 5.
func TestFirstLookNoteUsesPickedTasks(t *testing.T) {
	p, err := PickFirstLook([]BenchmarkInfo{{ID: "small", Compatible: true, Tasks: 2}}, true)
	if err != nil {
		t.Fatal(err)
	}
	want := "First look: 2 tasks. Too small for a verdict; run the full benchmark for one."
	if p.Tasks != 2 || p.Note != want {
		t.Fatalf("pick tasks %d note %q, want 2 and %q", p.Tasks, p.Note, want)
	}
}
