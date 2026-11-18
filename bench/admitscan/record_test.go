package admitscan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func sampleRecord() *schemas.AdmitScanRecord {
	code := 0
	rec := &schemas.AdmitScanRecord{
		Kind: schemas.AdmitScanKindSkill, Subject: "demo", Source: "/skills/demo", ContentHash: "sha256:abc",
		Files: []schemas.AdmitScanFile{{Path: "SKILL.md", SHA256: "aa", Size: 10, Scanned: true}, {Path: "x", Symlink: "/etc/passwd"}},
		Findings: []schemas.AdmitScanFinding{
			{Rule: RuleTagChars, Severity: schemas.AdmitScanSeverityBlock, File: "SKILL.md", Line: 3, Column: 4, Excerpt: "a<U+E0041>", Decoded: "run | this", Detail: "tag chars"},
			{Rule: RuleUnlistedHost, Severity: schemas.AdmitScanSeverityBlock, File: "a|b.md", Line: 1, Column: 1, Detail: "<img> host"},
		},
		Dynamic: &schemas.AdmitScanDynamic{Ran: true, Image: "img", HelperImage: "helper", ExitCode: &code, Observed: true, Commands: []string{"install.sh: bash x"},
			Canaries: []schemas.AdmitScanCanary{{ID: "aws", Path: "/home/agent/.aws/credentials", SHA256: "0123456789abcdef"}}},
		MCP: &schemas.AdmitScanMCP{ServerName: "s", Endpoint: "http://127.0.0.1:1/mcp", Tools: []schemas.AdmitScanMCPTool{{Name: "t", SHA256: "ffff"}}, Listings: 4},
	}
	finish(rec, Options{Now: fixedNow, Override: "reviewed by hand", By: "raj"})
	return rec
}

func TestRenderMarkdown(t *testing.T) {
	md := string(RenderMarkdown(sampleRecord()))
	tests := []struct {
		name, want string
	}{
		{"decision", "**Decision: admitted_with_override**"},
		{"override", "| Override | reviewed by hand (by raj at 2026-11-18 06:00:00Z) |"},
		{"finding with decoded payload", "decoded: `run \\| this`"},
		{"pipe in file escaped", "a\\|b.md:1:1"},
		{"html escaped", "&lt;img> host"},
		{"sandbox", "Canary aws at `/home/agent/.aws/credentials`"},
		{"mcp", "tools/list compared 4 times"},
		{"link", "link to /etc/passwd"},
		{"limits", "## What this scan does not cover"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(md, tc.want) {
				t.Fatalf("report lacks %q:\n%s", tc.want, md)
			}
		})
	}
}

func TestStoreRecord(t *testing.T) {
	root := t.TempDir()
	rec := sampleRecord()
	dir, err := StoreRecord(root, rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{schemas.AdmitScanRecordFile, schemas.AdmitScanReportFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	back, err := ReadRecord(filepath.Join(dir, schemas.AdmitScanRecordFile))
	if err != nil {
		t.Fatal(err)
	}
	if back.ID != rec.ID || back.Decision != rec.Decision || len(back.Findings) != 2 || back.Findings[0].Decoded != "run | this" {
		t.Fatalf("round trip lost data: %+v", back)
	}
	if _, err := StoreRecord(root, rec); err == nil {
		t.Fatal("an existing record was overwritten")
	}
	bad := *rec
	bad.ID = "../x"
	if _, err := StoreRecord(root, &bad); err == nil {
		t.Fatal("a bad id was stored")
	}
}

func TestAdmissionsRoot(t *testing.T) {
	tests := []struct {
		name, env, bench, want string
	}{
		{"env wins", "/data/adm", "/bench", "/data/adm"},
		{"bench root", "", "/bench", "/bench/admissions"},
		{"unknown", "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(AdmissionsRootEnv, tc.env)
			if got := AdmissionsRoot(tc.bench); got != tc.want {
				t.Fatalf("AdmissionsRoot = %q, want %q", got, tc.want)
			}
		})
	}
}
