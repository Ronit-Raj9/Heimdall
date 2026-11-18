package monitorcert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func writeConfig(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "mon.py", "print('hi')\n")
	tests := []struct {
		name    string
		body    string
		wantErr string
		check   func(t *testing.T, r *Resolved)
	}{
		{"command with script", "name: kw\nkind: command\nimage: python:3.12-slim\nargv: [python3, -c, '{{script}}']\nscript: mon.py\n", "",
			func(t *testing.T, r *Resolved) {
				c := r.Config
				if c.Mode != schemas.MonitorModeTranscript || c.Network != schemas.MonitorNetworkNone || c.TimeoutS != defaultTimeoutS ||
					c.Resources.MemoryMB != defaultMemoryMB || c.Resources.PidsLimit != defaultPidsLimit || c.Resources.CPUs != defaultCPUs {
					t.Errorf("defaults %+v", c)
				}
				if argv := r.Argv(); argv[2] != "print('hi')\n" {
					t.Errorf("argv %q", argv)
				}
				if !strings.HasPrefix(r.Hash, "sha256:") || r.Network() != "none" {
					t.Errorf("hash %q network %q", r.Hash, r.Network())
				}
			}},
		{"json is yaml", `{"name":"aa","kind":"builtin","builtin":"always_allow","mode":"action"}`, "",
			func(t *testing.T, r *Resolved) {
				if r.Config.Mode != schemas.MonitorModeAction || r.Network() != "none" {
					t.Errorf("config %+v", r.Config)
				}
			}},
		{"http operator-managed", "name: h\nkind: http\nurl: http://127.0.0.1:9/score\n", "",
			func(t *testing.T, r *Resolved) {
				if r.Network() != schemas.MonitorNetworkOperator {
					t.Errorf("network %q", r.Network())
				}
			}},
		{"keyword defaults block_at", "name: k\nkind: builtin\nbuiltin: keyword\nkeywords: [rm -rf]\n", "",
			func(t *testing.T, r *Resolved) {
				if r.Config.BlockAt != 1 {
					t.Errorf("block_at %d", r.Config.BlockAt)
				}
			}},
		{"allowlist refused", "name: kw\nkind: command\nimage: x\nargv: [x]\nnetwork: allowlist\n", "cannot enforce an allowlist", nil},
		{"http network none refused", "name: h\nkind: http\nurl: https://x.example/score\nnetwork: none\n", "operator-managed", nil},
		{"http image refused", "name: h\nkind: http\nurl: https://x.example/score\nimage: x\n", "image-backed http monitors are not supported", nil},
		{"unknown field", "name: k\nkind: builtin\nbuiltin: always_allow\nheaders: {a: b}\n", "field headers not found", nil},
		{"unknown kind", "name: k\nkind: wasm\n", "kind \"wasm\"", nil},
		{"bad mode", "name: k\nkind: builtin\nbuiltin: always_allow\nmode: batch\n", "mode \"batch\"", nil},
		{"bad name", "name: 'a b'\nkind: builtin\nbuiltin: always_allow\n", "name", nil},
		{"script without token", "name: kw\nkind: command\nimage: x\nargv: [x]\nscript: mon.py\n", "no argv element holds {{script}}", nil},
		{"token without script", "name: kw\nkind: command\nimage: x\nargv: ['{{script}}']\n", "script is not set", nil},
		{"missing script file", "name: kw\nkind: command\nimage: x\nargv: ['{{script}}']\nscript: nope.py\n", "read script", nil},
		{"keyword without terms", "name: k\nkind: builtin\nbuiltin: keyword\n", "needs keywords or patterns", nil},
		{"bad pattern", "name: k\nkind: builtin\nbuiltin: keyword\npatterns: ['(']\n", "pattern", nil},
		{"unknown builtin", "name: k\nkind: builtin\nbuiltin: llm\n", "builtin \"llm\"", nil},
		{"timeout too long", "name: k\nkind: builtin\nbuiltin: always_allow\ntimeout_s: 9999\n", "timeout_s", nil},
		{"bad url", "name: h\nkind: http\nurl: ftp://x\n", "url", nil},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := writeConfig(t, dir, "c"+string(rune('a'+i))+".yaml", tc.body)
			r, err := LoadConfig(p)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, r)
		})
	}
}

func TestConfigHash(t *testing.T) {
	base := schemas.MonitorConfig{Name: "kw", Kind: schemas.MonitorKindCommand, Image: "x", Argv: []string{"{{script}}"}, Script: "s.py"}
	a, err := Resolve(base, "print(1)")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Resolve(base, "print(1)")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Resolve(base, "print(2)")
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash != b.Hash || a.Hash == c.Hash {
		t.Errorf("hashes %s %s %s: same config must hash the same, a script change must change it", a.Hash, b.Hash, c.Hash)
	}
}
