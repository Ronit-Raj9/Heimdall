package admitscan

import (
	"testing"
)

func TestScanPackageJSON(t *testing.T) {
	tests := []struct {
		name      string
		json      string
		wantRules map[string]string // rule -> severity
	}{
		{"no hooks", `{"name":"x","scripts":{"test":"jest"}}`, map[string]string{}},
		{"pinned hook", `{"scripts":{"postinstall":"npx esbuild@0.21.5 src/index.ts"}}`, map[string]string{RuleInstallHook: "warn"}},
		{"unpinned npx", `{"scripts":{"postinstall":"npx some-helper --setup"}}`, map[string]string{RuleInstallHook: "warn", RuleInstallHookFetch: "block"}},
		{"latest npm", `{"scripts":{"preinstall":"npm install helper@latest"}}`, map[string]string{RuleInstallHook: "warn", RuleInstallHookFetch: "block"}},
		{"curl in hook", `{"scripts":{"install":"curl -s https://h.io/x -o x"}}`, map[string]string{RuleInstallHook: "warn", RuleInstallHookFetch: "block"}},
		{"node -e", `{"scripts":{"prepare":"node -e \"require('child_process')\""}}`, map[string]string{RuleInstallHook: "warn", RuleInstallHookFetch: "block"}},
		{"not json", `scripts: postinstall`, map[string]string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := map[string]string{}
			for _, f := range ScanPackageJSON("package.json", []byte(tc.json)) {
				got[f.Rule] = f.Severity
			}
			if len(got) != len(tc.wantRules) {
				t.Fatalf("got %v, want %v", got, tc.wantRules)
			}
			for r, sev := range tc.wantRules {
				if got[r] != sev {
					t.Fatalf("rule %s: got %q, want %q (all %v)", r, got[r], sev, got)
				}
			}
		})
	}
}

func TestPipFromURL(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"pip install requests==2.32.3", false},
		{"pip install git+https://github.com/x/y", true},
		{"pip3 install https://h.io/pkg.whl", true},
		{"pip install foo --index-url https://pypi.example.org/simple", false},
		{"pip install -i https://pypi.example.org/simple foo", false},
		{"pip install pkg @ https://h.io/pkg.tgz", true},
		{"uv pip install 'git+https://h.io/x'", true},
		{"pip install -r requirements.txt && echo https://x", false},
		{"echo hello", false},
	}
	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			if got := pipFromURL(tc.line); got != tc.want {
				t.Fatalf("pipFromURL(%q) = %v", tc.line, got)
			}
		})
	}
}

func TestUnpinnedHook(t *testing.T) {
	tests := []struct {
		cmd     string
		flagged bool
	}{
		{"npx tsc", true},
		{"npx typescript@5.6.3 tsc", false},
		{"npm i left-pad", true},
		{"npm install --no-save left-pad@1.3.0", false},
		{"node scripts/build.js", false},
	}
	for _, tc := range tests {
		t.Run(tc.cmd, func(t *testing.T) {
			if got := unpinnedHook(tc.cmd) != ""; got != tc.flagged {
				t.Fatalf("unpinnedHook(%q) flagged = %v", tc.cmd, got)
			}
		})
	}
}
