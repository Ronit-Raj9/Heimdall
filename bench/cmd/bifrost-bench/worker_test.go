package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestReadTokenFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tok, err := readTokenFile(write("ok", "  secret-token\n", 0o600))
	if err != nil || tok != "secret-token" {
		t.Fatalf("token %q, %v", tok, err)
	}
	cases := []struct {
		path string
		want string
	}{
		{write("open", "secret", 0o644), "readable by others"},
		{write("empty", " \n", 0o600), "empty"},
		{filepath.Join(dir, "missing"), "token file"},
	}
	for _, tc := range cases {
		if _, err := readTokenFile(tc.path); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v, want %q", tc.path, err, tc.want)
		}
	}
}

func TestWithWorkers(t *testing.T) {
	st, err := store.NewFSStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{logger: utils.NewStdLogger(os.Stderr, false), reg: &schemas.Registry{Store: st}}
	base := newLauncher(a, nil)

	t.Setenv(workerTokenEnv, "")
	l, api := withWorkers(a, base)
	if l != base || api != nil {
		t.Fatalf("token unset: launcher %T, api %v", l, api)
	}

	t.Setenv(workerTokenEnv, "worker-secret")
	l, api = withWorkers(a, nil)
	if l != nil || api != nil {
		t.Fatalf("offline server: launcher %v, api %v", l, api)
	}
	l, api = withWorkers(a, base)
	wl, ok := l.(*workerLauncher)
	if !ok || api == nil || wl.launcher != base {
		t.Fatalf("token set: launcher %T, api %v", l, api)
	}
	if !api.CheckToken("worker-secret") || api.CheckToken("other") {
		t.Fatal("coordinator does not check the configured token")
	}
	if len(api.Workers()) != 0 {
		t.Fatal("workers before any registered")
	}
}

func TestWorkerModeCheck(t *testing.T) {
	cases := []struct {
		token    string
		loopback bool
		status   schemas.DoctorStatus
	}{
		{"", true, schemas.DoctorSkip},
		{"tok-7f3k", true, schemas.DoctorWarn},
		{strings.Repeat("x", minWorkerToken), true, schemas.DoctorPass},
		{strings.Repeat("x", minWorkerToken), false, schemas.DoctorWarn},
	}
	for _, tc := range cases {
		t.Setenv(workerTokenEnv, tc.token)
		row := workerModeCheck(tc.loopback)
		if row.Status != tc.status || (tc.token != "" && strings.Contains(row.Detail, tc.token)) {
			t.Fatalf("token %q loopback %v: %+v", tc.token, tc.loopback, row)
		}
	}
}
