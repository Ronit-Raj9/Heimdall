package verdict

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestAuditMaximTB21(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "testdata", "audit", "maxim-tb21.csv"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	rows, err := ParseAuditCSV(f)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	v, err := Audit(rows, 0.05)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	aPass, bPass := 0, 0
	for _, r := range rows {
		aPass += r.APass
		bPass += r.BPass
	}
	e := v.Effect
	tests := []struct {
		name string
		ok   bool
	}{
		{"77 tasks, A 74, B 73", len(rows) == 77 && aPass == 74 && bPass == 73},
		{"inconclusive", v.Decision == schemas.DecisionInconclusive && v.Action == schemas.ActionNoDecision},
		{"p = 1.0", e.PValue != nil && *e.PValue == 1.0},
		{"4 discordant for A, 3 for B", e.NNonzero == 7 && e.NNegative == 4 && e.NPositive == 3 && e.NTasks == 77},
		{"d_bar -1/77", near(e.DBar, -1.0/77, 1e-12)},
		{"bounds equal the point estimate", e.LowerBound.DBar == e.DBar && e.UpperBound.DBar == e.DBar},
		{"assumed SD", v.MDEAssumedSD && v.SD == defaultAssumedSD && v.MDE > 0},
		{"no invalid reasons", len(v.InvalidReasons) == 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Fatalf("verdict %+v; effect %+v", v, e)
			}
		})
	}
}

func TestAudit(t *testing.T) {
	clear := make([]AuditRow, 8)
	for i := range clear {
		clear[i] = AuditRow{Task: taskID(i), APass: 0, AN: 3, BPass: 3, BN: 3}
	}
	tests := []struct {
		name     string
		rows     []AuditRow
		alpha    float64
		decision schemas.Decision
		wantErr  string
	}{
		{"clear improvement", clear, 0.05, schemas.DecisionImproved, ""},
		{"default alpha", clear, 0, schemas.DecisionImproved, ""},
		{"unequal attempts is invalid", []AuditRow{{Task: "t1", APass: 1, AN: 2, BPass: 1, BN: 1}}, 0.05, schemas.DecisionInvalid, ""},
		{"no rows", nil, 0.05, "", "no rows"},
		{"passes above attempts", []AuditRow{{Task: "t1", APass: 2, AN: 1, BPass: 0, BN: 1}}, 0.05, "", "a_pass 2 outside 0..1"},
		{"zero attempts", []AuditRow{{Task: "t1", AN: 0, BN: 1}}, 0.05, "", "attempts must be positive"},
		{"empty task", []AuditRow{{AN: 1, BN: 1}}, 0.05, "", "empty task id"},
		{"duplicate task", []AuditRow{{Task: "t1", AN: 1, BN: 1}, {Task: "t1", AN: 1, BN: 1}}, 0.05, "", "duplicate task t1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := Audit(tt.rows, tt.alpha)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("audit: %v", err)
			}
			if v.Decision != tt.decision {
				t.Fatalf("decision %s, want %s (%v)", v.Decision, tt.decision, v.InvalidReasons)
			}
		})
	}
}

func TestParseAuditCSV(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []AuditRow
		wantErr string
	}{
		{"columns in any order with spaces", "b_n, b_pass, task, a_n, a_pass\n3, 2, t1, 3, 1\n",
			[]AuditRow{{Task: "t1", APass: 1, AN: 3, BPass: 2, BN: 3}}, ""},
		{"header only", "task,a_pass,a_n,b_pass,b_n\n", nil, ""},
		{"byte order mark", "\ufefftask,a_pass,a_n,b_pass,b_n\nt1,0,1,1,1\n", []AuditRow{{Task: "t1", AN: 1, BPass: 1, BN: 1}}, ""},
		{"empty input", "", nil, "read audit csv header"},
		{"missing column", "task,a_pass,a_n,b_pass\nt1,1,1,1\n", nil, "missing column b_n"},
		{"not a number", "task,a_pass,a_n,b_pass,b_n\nt1,one,1,1,1\n", nil, "audit csv line 2: column a_pass"},
		{"ragged row", "task,a_pass,a_n,b_pass,b_n\nt1,1,1\n", nil, "read audit csv line 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAuditCSV(strings.NewReader(tt.in))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("row %d: got %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
