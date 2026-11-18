package lib

import (
	"os"
	"path/filepath"
	"testing"
)

// Database area (P1): the database section defaults and overrides.
func TestDatabaseConfig(t *testing.T) {
	cfg, err := LoadConfig("../deploy/bench.yaml")
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.DatabaseConfig()
	want := filepath.Join(cfg.Root, "runs", ".heimdall", "heimdall.db")
	if d.Driver != "sqlite" || d.Path != want || d.DSNEnv != "HEIMDALL_DATABASE_URL" {
		t.Fatalf("defaults: %+v, want path %s", d, want)
	}

	t.Setenv("BENCH_DATABASE_DRIVER", "postgres")
	t.Setenv("BENCH_DATABASE_DSN_ENV", "MY_PG")
	cfg, err = LoadConfig("../deploy/bench.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if d := cfg.DatabaseConfig(); d.Driver != "postgres" || d.DSNEnv != "MY_PG" {
		t.Fatalf("env overrides: %+v", d)
	}
	t.Setenv("MY_PG", "postgres://x")
	if dsn, err := cfg.DatabaseConfig().PostgresDSN(); err != nil || dsn != "postgres://x" {
		t.Fatalf("dsn %q %v", dsn, err)
	}

	dir := t.TempDir()
	src, err := os.ReadFile("../deploy/bench.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "deploy"), 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "deploy", "bench.yaml")
	if err := os.WriteFile(p, append(src, []byte("\ndatabase:\n  driver: mysql\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCH_DATABASE_DRIVER", "")
	os.Unsetenv("BENCH_DATABASE_DRIVER")
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("driver mysql accepted")
	}
}
