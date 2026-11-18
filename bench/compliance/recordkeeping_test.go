package compliance

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestRecordKeeping(t *testing.T) {
	rk := recordKeeping()
	if rk.ExternalAnchor {
		t.Error("the bench has no external anchor")
	}
	if !strings.Contains(rk.Retention, "six months") || !strings.Contains(rk.Retention, "never expires") {
		t.Errorf("retention must state the six-month duty and that nothing expires: %q", rk.Retention)
	}
	want := map[string]bool{"Art. 12(1)": false, "Art. 26(6)": false, "Art. 73": false}
	for _, a := range rk.Articles {
		if _, ok := want[a.ID]; ok {
			want[a.ID] = true
		}
		if a.Note == "" || a.Source == "" {
			t.Errorf("article %s needs a note and a source", a.ID)
		}
		if a.ID == "Art. 73" && a.Verified {
			t.Error("Art. 73 was not re-read from its source and must stay unverified")
		}
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("article %s missing", id)
		}
	}
	sealed := 0
	for _, f := range rk.Retained {
		if f.Sealed {
			sealed++
		}
	}
	if sealed == 0 {
		t.Error("retained files must name what the seal covers")
	}
}

func TestNotCertifiedNamesMode(t *testing.T) {
	cases := []struct {
		mode schemas.Mode
		want bool
	}{
		{schemas.ModeLive, false},
		{schemas.ModeRecorded, true},
		{schemas.ModeSynthetic, true},
	}
	for _, c := range cases {
		t.Run(string(c.mode), func(t *testing.T) {
			p := &schemas.CompliancePack{Experiments: []schemas.ComplianceExperiment{{ExpID: "e_x", Mode: c.mode}}}
			got := strings.Contains(strings.Join(notCertified(p), "\n"), "e_x ran in")
			if got != c.want {
				t.Fatalf("mode %s: mention %v, want %v", c.mode, got, c.want)
			}
		})
	}
}

func TestNISTFunctions(t *testing.T) {
	p := &schemas.CompliancePack{Tests: make([]schemas.ComplianceTest, 4), Unmapped: []string{"gate:x"}}
	fs := nistFunctions(p)
	if len(fs) == 0 {
		t.Fatal("no functions")
	}
	for _, f := range fs {
		if f.Name == "" || f.Evidence == "" || f.Gap == "" || !f.Verified {
			t.Errorf("function %+v incomplete", f)
		}
		if f.ID == "MEASURE 1.1" && !strings.Contains(f.Evidence, "3 tests mapped; 1 unmapped") {
			t.Errorf("MEASURE 1.1 evidence %q", f.Evidence)
		}
	}
}
