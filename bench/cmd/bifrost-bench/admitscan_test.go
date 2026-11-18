package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/admitscan"
	"github.com/maximhq/bifrost/bench/schemas"
)

const skillFixtures = "../../admitscan/testdata/skills"

func TestParseInterleaved(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		pos   []string
		allow string
		dyn   bool
		err   bool
	}{
		{"flags first", []string{"-dynamic", "dir"}, []string{"dir"}, "", true, false},
		{"flags after", []string{"dir", "-allow-host", "a.io", "-allow-host", "b.io,c.io", "-dynamic"}, []string{"dir"}, "a.io,b.io,c.io", true, false},
		{"two positionals", []string{"a", "-dynamic", "b"}, []string{"a", "b"}, "", true, false},
		{"double dash", []string{"--", "-odd-dir"}, []string{"-odd-dir"}, "", false, false},
		{"unknown flag", []string{"dir", "-nope"}, nil, "", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			fs.SetOutput(new(strings.Builder))
			var allow csvFlag
			fs.Var(&allow, "allow-host", "")
			dyn := fs.Bool("dynamic", false, "")
			pos, err := parseInterleaved(fs, tc.args)
			if (err != nil) != tc.err {
				t.Fatalf("err %v", err)
			}
			if tc.err {
				return
			}
			if strings.Join(pos, "|") != strings.Join(tc.pos, "|") || allow.String() != tc.allow || *dyn != tc.dyn {
				t.Fatalf("pos %v allow %q dyn %v", pos, allow.String(), *dyn)
			}
		})
	}
}

func TestRunSkillScan(t *testing.T) {
	out := t.TempDir()
	tests := []struct {
		name     string
		args     []string
		code     int
		report   string
		decision string
	}{
		{"clean admitted", []string{filepath.Join(skillFixtures, "clean"), "-o", filepath.Join(out, "clean.json")}, exitOK, "clean.json", schemas.AdmitScanAdmitted},
		{"curl blocked", []string{filepath.Join(skillFixtures, "curl-sh"), "-o", filepath.Join(out, "curl.json")}, exitBlocked, "curl.json", schemas.AdmitScanBlocked},
		{"override", []string{filepath.Join(skillFixtures, "curl-sh"), "-override", "pinned installer reviewed", "-by", "raj", "-o", filepath.Join(out, "ov.json")}, exitOK, "ov.json", schemas.AdmitScanAdmittedWithOverride},
		{"allow host", []string{filepath.Join(skillFixtures, "unknown-host"), "-allow-host", "*.evil-cdn.xyz", "-o", filepath.Join(out, "allow.json")}, exitOK, "allow.json", schemas.AdmitScanAdmitted},
		{"markdown report", []string{filepath.Join(skillFixtures, "bidi"), "-o", filepath.Join(out, "bidi.md")}, exitBlocked, "bidi.md", ""},
		{"empty override", []string{filepath.Join(skillFixtures, "curl-sh"), "-override", ""}, exitUsage, "", ""},
		{"no dir", nil, exitUsage, "", ""},
		{"missing dir", []string{filepath.Join(out, "nope"), "-o", filepath.Join(out, "x.json")}, exitInfra, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if code := runSkillScan(tc.args); code != tc.code {
				t.Fatalf("exit %d, want %d", code, tc.code)
			}
			if tc.report == "" {
				return
			}
			p := filepath.Join(out, tc.report)
			if tc.decision == "" {
				b, err := os.ReadFile(p)
				if err != nil || !strings.Contains(string(b), "**Decision: blocked**") {
					t.Fatalf("markdown report: %v %s", err, b)
				}
				return
			}
			rec, err := admitscan.ReadRecord(p)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Decision != tc.decision {
				t.Fatalf("decision %s, want %s", rec.Decision, tc.decision)
			}
		})
	}
}

func TestRunSkillScanStore(t *testing.T) {
	root := t.TempDir()
	t.Setenv(admitscan.AdmissionsRootEnv, root)
	if code := runSkillScan([]string{filepath.Join(skillFixtures, "zero-width")}); code != exitBlocked {
		t.Fatalf("exit %d", code)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("stored %v %v", entries, err)
	}
	rec, err := admitscan.ReadRecord(filepath.Join(root, entries[0].Name(), schemas.AdmitScanRecordFile))
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != entries[0].Name() || rec.Decision != schemas.AdmitScanBlocked {
		t.Fatalf("record %s %s", rec.ID, rec.Decision)
	}
	if _, err := os.Stat(filepath.Join(root, rec.ID, schemas.AdmitScanReportFile)); err != nil {
		t.Fatal(err)
	}
}

func TestRunSkillCmdAndMCPUsage(t *testing.T) {
	tests := []struct {
		name string
		run  func([]string) int
		args []string
		code int
	}{
		{"skill no sub", runSkillCmd, nil, exitUsage},
		{"skill bad sub", runSkillCmd, []string{"nope"}, exitUsage},
		{"skill rules", runSkillCmd, []string{"rules"}, exitOK},
		{"skill rules json", runSkillCmd, []string{"rules", "-json"}, exitOK},
		{"mcp no sub", runMCPCmd, nil, exitUsage},
		{"mcp no config", runMCPCmd, []string{"scan"}, exitUsage},
		{"mcp missing config", runMCPCmd, []string{"scan", "/nonexistent/svc.yaml"}, exitInfra},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if code := tc.run(tc.args); code != tc.code {
				t.Fatalf("exit %d, want %d", code, tc.code)
			}
		})
	}
}
