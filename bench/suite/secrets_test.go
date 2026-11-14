package suite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanSecretsIn(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantKind string // "" means no finding
		secret   string // must not appear in the masked value
	}{
		{"private key", "x\n-----BEGIN RSA PRIVATE KEY-----\nabc\n", "private key", "RSA PRIVATE KEY"},
		{"openai style key", `key = "sk-proj-Ab3dEf6hIj9kLm2nOp5qRs8t"`, "sk- API key", "proj-Ab3dEf6hIj9kLm2nOp5qRs8t"},
		{"aws key id", "aws_id=AKIAIOSFODNN7EXAMPLE\n", "AWS access key id", "IOSFODNN7EXAMPLE"},
		{"github token", "ghp_0123456789abcdefghijABCDEFGHIJ012345", "GitHub token", "0123456789abcdef"},
		{"slack token", "url: xoxb-1234567890-abcdefghij", "Slack token", "1234567890"},
		{"high entropy token assignment", "export DEPLOY_TOKEN=q8Zr2LmX9vB4nT7kW1pS6dF3", "high-entropy DEPLOY_TOKEN value", "q8Zr2LmX9vB4nT7kW1pS6dF3"},
		{"low entropy key assignment", "CACHE_KEY=aaaaaaaaaaaaaaaaaaaaaaaa", "", ""},
		{"placeholder token", "BIFROST_SETUP_TOKEN=replace-with-a-random-token", "", ""},
		{"word ending in sk-", "task-runner and desk-lamp-assembly-instructions", "", ""},
		{"short value", "API_KEY=abc123", "", ""},
		{"binary file", "\x00sk-proj-Ab3dEf6hIj9kLm2nOp5qRs8t", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ScanSecretsIn("f.txt", []byte(tc.content))
			if tc.wantKind == "" {
				if len(got) != 0 {
					t.Fatalf("findings = %+v, want none", got)
				}
				return
			}
			if len(got) != 1 || got[0].Kind != tc.wantKind {
				t.Fatalf("findings = %+v, want one %q", got, tc.wantKind)
			}
			if strings.Contains(got[0].Masked, tc.secret) || strings.Contains(got[0].String(), tc.secret) {
				t.Fatalf("masked value %q shows the secret", got[0].Masked)
			}
			if got[0].Line < 1 || got[0].Path != "f.txt" {
				t.Fatalf("finding = %+v", got[0])
			}
		})
	}
}

func TestScanSecrets(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("repo/app/config.py", "import os\n\nTOKEN = 'x'\nAWS = 'AKIAIOSFODNN7EXAMPLE'\n")
	write("repo/.git/config", "AKIAIOSFODNN7EXAMPLE\n")
	write("instruction.md", "Fix it.\n")
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "repo", "link")); err != nil {
		t.Fatal(err)
	}
	got, err := ScanSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "repo/app/config.py" || got[0].Line != 4 {
		t.Fatalf("findings = %+v", got)
	}
}
