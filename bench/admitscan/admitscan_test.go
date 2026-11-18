package admitscan

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestDecide(t *testing.T) {
	block := schemas.AdmitScanFinding{Rule: "x", Severity: schemas.AdmitScanSeverityBlock}
	warn := schemas.AdmitScanFinding{Rule: "y", Severity: schemas.AdmitScanSeverityWarn}
	tests := []struct {
		name     string
		findings []schemas.AdmitScanFinding
		override string
		want     string
	}{
		{"none", nil, "", schemas.AdmitScanAdmitted},
		{"warn only", []schemas.AdmitScanFinding{warn}, "", schemas.AdmitScanAdmitted},
		{"block", []schemas.AdmitScanFinding{warn, block}, "", schemas.AdmitScanBlocked},
		{"block with override", []schemas.AdmitScanFinding{block}, "reviewed", schemas.AdmitScanAdmittedWithOverride},
		{"block with blank override", []schemas.AdmitScanFinding{block}, "   ", schemas.AdmitScanBlocked},
		{"override without block", []schemas.AdmitScanFinding{warn}, "reviewed", schemas.AdmitScanAdmitted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(tc.findings, tc.override); got != tc.want {
				t.Fatalf("Decide = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestFinish(t *testing.T) {
	rec := &schemas.AdmitScanRecord{Findings: []schemas.AdmitScanFinding{
		{Rule: "i", Severity: schemas.AdmitScanSeverityInfo, File: "a"},
		{Rule: "b2", Severity: schemas.AdmitScanSeverityBlock, File: "b", Line: 2},
		{Rule: "w", Severity: schemas.AdmitScanSeverityWarn, File: "a"},
		{Rule: "b1", Severity: schemas.AdmitScanSeverityBlock, File: "b", Line: 1},
	}}
	finish(rec, Options{Now: fixedNow, Override: " ok ", By: "me", AllowHosts: []string{"z.io", "a.io"}})
	var order []string
	for _, f := range rec.Findings {
		order = append(order, f.Rule)
	}
	if strings.Join(order, ",") != "b1,b2,w,i" {
		t.Fatalf("order %v", order)
	}
	if rec.Counts != (schemas.AdmitScanCounts{Block: 2, Warn: 1, Info: 1}) {
		t.Fatalf("counts %+v", rec.Counts)
	}
	if rec.Decision != schemas.AdmitScanAdmittedWithOverride || rec.Override.Reason != "ok" || rec.Override.By != "me" {
		t.Fatalf("decision %s override %+v", rec.Decision, rec.Override)
	}
	if !strings.HasPrefix(rec.ID, "adm_") || rec.ScannerVersion != Version || !rec.CreatedAt.Equal(fixedNow()) {
		t.Fatalf("stamp %+v", rec)
	}
	if strings.Join(rec.AllowHosts, ",") != "a.io,z.io" || len(rec.Limits) != len(Limits) {
		t.Fatalf("hosts %v limits %d", rec.AllowHosts, len(rec.Limits))
	}
}

func TestRulesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Rules() {
		if seen[r.ID] {
			t.Fatalf("rule %s listed twice", r.ID)
		}
		seen[r.ID] = true
		switch r.Severity {
		case schemas.AdmitScanSeverityBlock, schemas.AdmitScanSeverityWarn, schemas.AdmitScanSeverityInfo:
		default:
			t.Fatalf("rule %s has severity %q", r.ID, r.Severity)
		}
		if r.What == "" {
			t.Fatalf("rule %s has no description", r.ID)
		}
	}
	for _, id := range []string{RuleZeroWidth, RuleRugPull, RuleCanaryRead, RuleRemoteFetch, RuleMCPFilesSkipped, RuleNoSetup} {
		if !seen[id] {
			t.Fatalf("rule %s missing from Rules", id)
		}
	}
}
