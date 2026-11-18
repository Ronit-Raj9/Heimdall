package admitscan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func fixedNow() time.Time { return time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC) }

func TestScanSkillFixtures(t *testing.T) {
	tests := []struct {
		skill    string
		decision string
		rules    []string // block rules that must be present
	}{
		{"clean", schemas.AdmitScanAdmitted, nil},
		{"zero-width", schemas.AdmitScanBlocked, []string{RuleZeroWidth}},
		{"bidi", schemas.AdmitScanBlocked, []string{RuleBidi}},
		{"tag-chars", schemas.AdmitScanBlocked, []string{RuleTagChars}},
		{"base64", schemas.AdmitScanBlocked, []string{RuleBase64}},
		{"curl-sh", schemas.AdmitScanBlocked, []string{RuleRemoteFetch}},
		{"unknown-host", schemas.AdmitScanBlocked, []string{RuleUnlistedHost}},
		{"html-comment", schemas.AdmitScanBlocked, []string{RuleHTMLComment, RuleInjection}},
		{"lure", schemas.AdmitScanBlocked, []string{RuleInstallLure, RuleDownloadExec, RuleRemoteFetch, RuleUnlistedHost}},
	}
	for _, tc := range tests {
		t.Run(tc.skill, func(t *testing.T) {
			rec, err := ScanSkill(context.Background(), filepath.Join("testdata", "skills", tc.skill), Options{Now: fixedNow})
			if err != nil {
				t.Fatal(err)
			}
			if rec.Decision != tc.decision {
				t.Fatalf("decision %s, want %s; findings %+v", rec.Decision, tc.decision, rec.Findings)
			}
			blocks := map[string]bool{}
			for _, f := range rec.Findings {
				if f.Severity == schemas.AdmitScanSeverityBlock {
					blocks[f.Rule] = true
				}
				if f.File == "" || (f.Line == 0 && strings.HasPrefix(f.Rule, "unicode")) {
					t.Fatalf("finding without location: %+v", f)
				}
			}
			for _, r := range tc.rules {
				if !blocks[r] {
					t.Fatalf("missing block rule %s; got %v", r, blocks)
				}
			}
			if rec.Counts.Block != countSeverity(rec.Findings, schemas.AdmitScanSeverityBlock) {
				t.Fatalf("counts %+v do not match findings", rec.Counts)
			}
			if !strings.HasPrefix(rec.ContentHash, "sha256:") || len(rec.Files) == 0 || rec.Files[0].SHA256 == "" {
				t.Fatalf("record lacks hashes: %+v", rec)
			}
			if rec.Kind != schemas.AdmitScanKindSkill || rec.Subject != tc.skill || len(rec.Limits) == 0 {
				t.Fatalf("bad record header: %+v", rec)
			}
		})
	}
}

func countSeverity(fs []schemas.AdmitScanFinding, sev string) int {
	n := 0
	for _, f := range fs {
		if f.Severity == sev {
			n++
		}
	}
	return n
}

func TestScanSkillOverrideAndAllow(t *testing.T) {
	tests := []struct {
		name     string
		skill    string
		opts     Options
		decision string
	}{
		{"override admits", "curl-sh", Options{Override: "vendored installer reviewed by hand", By: "raj"}, schemas.AdmitScanAdmittedWithOverride},
		{"blank override blocks", "curl-sh", Options{Override: "  "}, schemas.AdmitScanBlocked},
		{"override on clean stays admitted", "clean", Options{Override: "not needed"}, schemas.AdmitScanAdmitted},
		{"allowlisted host admits", "unknown-host", Options{AllowHosts: []string{"*.evil-cdn.xyz"}}, schemas.AdmitScanAdmitted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.Now = fixedNow
			rec, err := ScanSkill(context.Background(), filepath.Join("testdata", "skills", tc.skill), tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Decision != tc.decision {
				t.Fatalf("decision %s, want %s", rec.Decision, tc.decision)
			}
			if tc.decision == schemas.AdmitScanAdmittedWithOverride {
				if rec.Override == nil || rec.Override.Reason != tc.opts.Override || rec.Override.By != "raj" || !rec.Override.At.Equal(fixedNow()) {
					t.Fatalf("override not recorded: %+v", rec.Override)
				}
			} else if rec.Override != nil {
				t.Fatalf("override recorded without effect: %+v", rec.Override)
			}
		})
	}
}

func TestScanSkillLinksAndMissing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "passwd")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tool.bin"), append([]byte("\x7fELF\x02\x01\x01\x00"), make([]byte, 64)...), 0o755); err != nil {
		t.Fatal(err)
	}
	rec, err := ScanSkill(context.Background(), dir, Options{Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{RuleSymlink: "block", RuleSkillMDMissing: "block", RuleExecutable: "warn"}
	got := map[string]string{}
	for _, f := range rec.Findings {
		got[f.Rule] = f.Severity
	}
	for r, sev := range want {
		if got[r] != sev {
			t.Fatalf("rule %s = %q, want %q (all %v)", r, got[r], sev, got)
		}
	}
	for _, f := range rec.Files {
		if f.Path == "passwd" && (f.Symlink != "/etc/passwd" || f.Scanned || f.SHA256 != "") {
			t.Fatalf("link was followed or hashed: %+v", f)
		}
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ScanSkill(context.Background(), link, Options{}); err == nil {
		t.Fatal("a symlinked skill folder was scanned")
	}
}

func TestContentHashChanges(t *testing.T) {
	base := []schemas.AdmitScanFile{{Path: "SKILL.md", SHA256: "aa"}, {Path: "b.sh", SHA256: "bb"}}
	tests := []struct {
		name  string
		files []schemas.AdmitScanFile
		same  bool
	}{
		{"reordered", []schemas.AdmitScanFile{base[1], base[0]}, true},
		{"changed file", []schemas.AdmitScanFile{base[0], {Path: "b.sh", SHA256: "cc"}}, false},
		{"added file", append(append([]schemas.AdmitScanFile(nil), base...), schemas.AdmitScanFile{Path: "c", SHA256: "dd"}), false},
		{"renamed file", []schemas.AdmitScanFile{base[0], {Path: "c.sh", SHA256: "bb"}}, false},
		{"link target", []schemas.AdmitScanFile{base[0], {Path: "b.sh", Symlink: "/x"}}, false},
	}
	h0 := ContentHash(base)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ContentHash(tc.files) == h0; got != tc.same {
				t.Fatalf("same hash = %v, want %v", got, tc.same)
			}
		})
	}
}

func TestFindSetup(t *testing.T) {
	md := "# X\n\n## Install\n\nRun this first:\n\n```bash\n$ pip install foo\n```\n\n## Usage\n\n```bash\nx --help\n```\n"
	files := map[string][]byte{
		SkillFile:              []byte(md),
		"install.sh":           []byte("echo hi"),
		"scripts/setup.py":     []byte("print(1)"),
		"scripts/run.sh":       []byte("echo run"),
		"a/b/c/d/bootstrap.sh": []byte("deep"),
		"README.md":            []byte("x"),
	}
	steps := FindSetup(files)
	var got []string
	for _, s := range steps {
		got = append(got, s.Source)
	}
	want := []string{"install.sh", "scripts/setup.py", "SKILL.md:8"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("steps %v, want %v", got, want)
	}
	if steps[2].Script != "pip install foo" {
		t.Fatalf("snippet %q", steps[2].Script)
	}
	script, cmds := setupDriver("x", steps)
	for _, w := range []string{"bash '/home/agent/skills/x/install.sh'", "python3 '/home/agent/skills/x/scripts/setup.py'", "sh /home/agent/.admitscan/snippet-3.sh", "exit $rc"} {
		if !strings.Contains(script, w) {
			t.Fatalf("driver lacks %q:\n%s", w, script)
		}
	}
	if len(cmds) != 3 {
		t.Fatalf("commands %v", cmds)
	}
}

func TestDynamicRecord(t *testing.T) {
	canaries := []Canary{{ID: "aws", Path: SandboxHome + "/.aws/credentials", Value: "AKIACANARYVALUE123"}}
	tests := []struct {
		name  string
		out   RunOutput
		obs   Observation
		rules []string
	}{
		{"clean", RunOutput{Output: []byte("ok\n")}, Observation{Observed: true}, nil},
		{"failed setup", RunOutput{ExitCode: 2}, Observation{Observed: true}, []string{RuleSetupFailed}},
		{"timeout", RunOutput{ExitCode: 137, TimedOut: true, Duration: time.Minute}, Observation{Observed: true}, []string{RuleSetupTimeout}},
		{"not observed", RunOutput{}, Observation{Observed: false, Error: "watcher not ready"}, []string{RuleNotObserved}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dyn, fs, err := dynamicRecord(&schemas.AdmitScanDynamic{}, canaries, tc.out, tc.obs)
			if err != nil {
				t.Fatal(err)
			}
			if !dyn.Ran || dyn.ExitCode == nil || *dyn.ExitCode != tc.out.ExitCode || len(dyn.Canaries) != 1 || dyn.Canaries[0].SHA256 == "" {
				t.Fatalf("bad dynamic section %+v", dyn)
			}
			got := map[string]bool{}
			for _, f := range fs {
				got[f.Rule] = true
			}
			if len(got) != len(tc.rules) {
				t.Fatalf("rules %v, want %v", got, tc.rules)
			}
			for _, r := range tc.rules {
				if !got[r] {
					t.Fatalf("missing %s in %v", r, got)
				}
			}
		})
	}
}
