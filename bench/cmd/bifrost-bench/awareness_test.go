package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
)

func TestPrintAwarenessRecorded(t *testing.T) {
	src := recordedSource(t)
	ctx := context.Background()
	tests := []struct {
		name string
		exp  string
		json bool
		code int
		want []string
	}{
		{name: "json", exp: "e_pilot_mixed_nc93", json: true, code: exitOK, want: []string{`"exp_id": "e_pilot_mixed_nc93"`, `"realism": "standard"`, `"heuristic": true`, `"per_arm"`, `"not_scanned"`}},
		{name: "text", exp: "e_smoke_sdoj", code: exitOK, want: []string{"HEURISTIC:", "ARM", "No signal is not evidence of no awareness"}},
		{name: "unknown experiment", exp: "e_missing", code: exitInfra},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if code := printAwareness(ctx, &out, src, tt.exp, tt.json); code != tt.code {
				t.Fatalf("exit %d, want %d; output:\n%s", code, tt.code, out.String())
			}
			for _, w := range tt.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output has no %q:\n%s", w, out.String())
				}
			}
			if !tt.json || tt.code != exitOK {
				return
			}
			var rep handlers.AwarenessReport
			if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
				t.Fatal(err)
			}
			total := 0
			for _, a := range rep.PerArm {
				if a.RunsScanned > a.Runs || a.RunsWithSignals > a.RunsScanned {
					t.Errorf("inconsistent arm %+v", a)
				}
				total += a.Runs
			}
			if total == 0 || total-len(rep.NotScanned) < 0 {
				t.Errorf("runs %d, not scanned %d", total, len(rep.NotScanned))
			}
		})
	}
}
