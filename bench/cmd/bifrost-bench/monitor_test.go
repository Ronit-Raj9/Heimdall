package main

// Eval integrity area (S2): monitor certification CLI.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/monitorcert"
	"github.com/maximhq/bifrost/bench/schemas"
)

// monitorConfig writes a bench config over the recorded bundle (read only)
// and points the certificates root at a temp folder.
func monitorConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	recorded, err := filepath.Abs(filepath.Join("..", "..", "testdata", "recorded"))
	if err != nil {
		t.Fatal(err)
	}
	benchmarks, err := filepath.Abs(filepath.Join("..", "..", "benchmarks"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "runs_root: " + recorded + "\nbenchmarks_root: " + benchmarks + "\nserver:\n  listen: 127.0.0.1:0\n  mode: recorded\n"
	p := filepath.Join(dir, "deploy", "bench.yaml")
	if err := os.WriteFile(p, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(monitorcert.RootEnv, filepath.Join(dir, "certs"))
	return p
}

func TestRunMonitor(t *testing.T) {
	cfg := monitorConfig(t)
	builtin := filepath.Join("..", "..", "examples", "monitors", "keyword", "builtin.yaml")
	code, out := captureOutput(t, func() int {
		return runMonitor([]string{"certify", "-config", cfg, "-monitor", builtin, "-exp", "e_pilot_mixed_nc93", "-exp", "e_smoke_sdoj", "-fpr", "0.05,0.01"})
	})
	if code != exitOK || !strings.Contains(out, "18 honest, 0 attack") || !strings.Contains(out, "target FPR 0.05") {
		t.Fatalf("certify exit %d:\n%s", code, out)
	}
	certs, err := monitorcert.NewCertStore(os.Getenv(monitorcert.RootEnv), "")
	if err != nil {
		t.Fatal(err)
	}
	list, err := certs.List(t.Context())
	if err != nil || len(list.Certifications) != 1 {
		t.Fatalf("list %+v err %v", list, err)
	}
	id := list.Certifications[0].CertID
	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"list", []string{"list", "-config", cfg}, exitOK, id},
		{"show", []string{"show", id, "-config", cfg}, exitOK, "# Monitor certificate " + id},
		{"show json", []string{"show", "-json", "-config", cfg, id}, exitOK, `"cert_id": "` + id + `"`},
		{"show unknown", []string{"show", "mc_nope", "-config", cfg}, exitInfra, ""},
		{"no subcommand", nil, exitUsage, ""},
		{"unknown subcommand", []string{"revoke"}, exitUsage, ""},
		{"certify without exp", []string{"certify", "-config", cfg, "-monitor", builtin}, exitUsage, ""},
		{"certify bad fpr", []string{"certify", "-config", cfg, "-monitor", builtin, "-exp", "e_smoke_sdoj", "-fpr", "2"}, exitInfra, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, out := captureOutput(t, func() int { return runMonitor(tc.args) })
			if code != tc.code || !strings.Contains(out, tc.want) {
				t.Errorf("exit %d, want %d; output lacks %q:\n%s", code, tc.code, tc.want, out)
			}
		})
	}
	c, err := certs.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if c.Mode != schemas.ModeRecorded || !slices.Contains(c.ExpIDs, "e_smoke_sdoj") || len(c.Experiments) != 2 {
		t.Errorf("certificate %+v", c)
	}
	b, err := json.Marshal(c)
	if err != nil || strings.Contains(string(b), "nonce_") {
		t.Errorf("certificate carries a nonce or does not encode: %v", err)
	}
}

// TestMonitorExamples loads every shipped example monitor config.
func TestMonitorExamples(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "monitors", "*", "*.yaml"))
	if err != nil || len(paths) < 3 {
		t.Fatalf("examples %v err %v", paths, err)
	}
	for _, p := range paths {
		t.Run(filepath.Base(filepath.Dir(p))+"/"+filepath.Base(p), func(t *testing.T) {
			r, err := monitorcert.LoadConfig(p)
			if err != nil {
				t.Fatal(err)
			}
			if r.Network() != schemas.MonitorNetworkNone {
				t.Errorf("example network %q", r.Network())
			}
		})
	}
}

func TestParseFPRs(t *testing.T) {
	tests := []struct {
		in      string
		want    []float64
		wantErr bool
	}{
		{"0.01,0.001", []float64{0.01, 0.001}, false},
		{" 0.05 ", []float64{0.05}, false},
		{"", nil, true},
		{"1", nil, true},
		{"0", nil, true},
		{"x", nil, true},
	}
	for _, tc := range tests {
		got, err := parseFPRs(tc.in)
		if (err != nil) != tc.wantErr || !slices.Equal(got, tc.want) {
			t.Errorf("parseFPRs(%q) = %v, %v", tc.in, got, err)
		}
	}
}
