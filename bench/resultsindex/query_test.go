package resultsindex

import (
	"testing"
	"time"
)

func TestParseFilterAndMatch(t *testing.T) {
	at := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	live := RunRow{Experiment: "e_1", RunID: "r_1", Suite: "s@1", Task: "t1", Arm: "A", Agent: "codex", Model: "m1",
		Condition: "clean", Outcome: "pass", ConfigHash: "sha256:a", Started: at}
	synth := live
	synth.RunID, synth.Synthetic = "r_2", true
	tests := []struct {
		name    string
		terms   []string
		want    []string
		wantErr bool
	}{
		{"no terms match all", nil, []string{"r_1", "r_2"}, false},
		{"exact field", []string{"agent=codex", "condition=clean"}, []string{"r_1", "r_2"}, false},
		{"mismatch", []string{"model=m2"}, nil, false},
		{"synthetic exclude", []string{"synthetic=exclude"}, []string{"r_1"}, false},
		{"synthetic only", []string{"synthetic=only"}, []string{"r_2"}, false},
		{"since date", []string{"since=2026-11-04"}, nil, false},
		{"until rfc3339", []string{"until=2026-11-03T12:00:01Z"}, []string{"r_1", "r_2"}, false},
		{"run id", []string{"run_id=r_2"}, []string{"r_2"}, false},
		{"unknown key", []string{"color=red"}, nil, true},
		{"not key value", []string{"codex"}, nil, true},
		{"bad synthetic", []string{"synthetic=maybe"}, nil, true},
		{"bad time", []string{"since=yesterday"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := ParseFilter(tt.terms)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			var got []string
			s := &Snapshot{RunRows: []RunRow{live, synth}}
			for _, r := range s.Runs(f) {
				got = append(got, r.RunID)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
	t.Run("limit", func(t *testing.T) {
		s := &Snapshot{RunRows: []RunRow{live, synth}}
		if got := s.Runs(Filter{Limit: 1}); len(got) != 1 {
			t.Fatalf("limit 1 returned %d rows", len(got))
		}
	})
}
