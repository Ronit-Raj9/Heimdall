package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A built-in monitor starts no container, so its test runs in the server
// process; a command monitor still goes to a worker (wave 4, p13-3).
func TestMonitorCertifyBuiltinRunsInProcess(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "monitors")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"kw.yaml":  "name: kw\nkind: builtin\nbuiltin: keyword\nmode: transcript\npatterns:\n  - curl\nblock_at: 1\n",
		"cmd.yaml": "name: cmd\nkind: command\nimage: python:3.12-slim\nargv: [python3, -c, 'print(1)']\n",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := jobEnv{ProjectRoot: root, Hosted: true}
	if !monitorCertifyInProcess(env, []byte(`{"monitor":"monitors/kw.yaml","exp_ids":["e1"]}`)) {
		t.Fatal("a built-in monitor must run in the server process")
	}
	if monitorCertifyInProcess(env, []byte(`{"monitor":"monitors/cmd.yaml","exp_ids":["e1"]}`)) {
		t.Fatal("a command monitor must run on a worker")
	}
	if monitorCertifyInProcess(env, []byte(`{"monitor":"../outside.yaml","exp_ids":["e1"]}`)) {
		t.Fatal("a path outside the project must not run in the server process")
	}
	if monitorCertifyInProcess(env, []byte(`{"monitor":"monitors/missing.yaml"}`)) {
		t.Fatal("a missing monitor file must not run in the server process")
	}
}
