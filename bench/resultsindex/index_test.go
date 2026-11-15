package resultsindex

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSnapshotRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), DirName, FileName)
	one := 1.0
	at := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	s := &Snapshot{
		BuiltAt: at, UpdatedAt: at.Add(time.Hour),
		Experiments:  map[string]string{"e_b": "fp2", "e_a": "fp1"},
		RunRows:      []RunRow{{Experiment: "e_b", RunID: "r_2", Success: &one, Metrics: map[string]float64{"tokens": 10}}, {Experiment: "e_a", RunID: "r_1", Metrics: map[string]float64{}}},
		AnalysisRows: []AnalysisRow{{Experiment: "e_a", AnalysisID: "a_1", Decision: "improved", CreatedAt: at}},
		Rejects:      []Rejected{{Experiment: "e_b", RunID: "r_3", Reason: "seal does not verify: x"}},
	}
	s.sortRows()
	if err := writeSnapshot(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := readSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.exists || got.RunRows[0].RunID != "r_1" {
		t.Fatalf("rows not sorted or file missing: %+v", got)
	}
	got.exists, got.path = false, ""
	s.path = ""
	if !reflect.DeepEqual(got, s) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got, s)
	}
	st := got.Status()
	if st.Runs != 2 || st.Analyses != 1 || st.Rejected != 1 || st.Experiments != 2 || st.Format != FormatName {
		t.Fatalf("status = %+v", st)
	}
}

func TestReadSnapshotTolerance(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		content string
		write   bool
		exists  bool
		wantErr bool
	}{
		{"missing file is empty", "", false, false, false},
		{"other format version is empty", `{"kind":"meta","version":99}` + "\n" + `{"kind":"run","run":{"run_id":"r"}}` + "\n", true, false, false},
		{"broken line is an error", `{"kind":"meta","version":1}` + "\n{not json\n", true, false, true},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(dir, string(rune('a'+i))+".jsonl")
			if tt.write {
				if err := os.WriteFile(p, []byte(tt.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			s, err := readSnapshot(p)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v", err, tt.wantErr)
			}
			if err == nil && (s.exists != tt.exists || len(s.RunRows) != 0) {
				t.Fatalf("snapshot = %+v", s)
			}
		})
	}
}
