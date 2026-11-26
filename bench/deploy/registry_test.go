package deploy

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// registryStub is a docker stub for the registry scripts: the server image
// knows `registry htpasswd`, and the sync tool answers each push with a
// digest reference.
func registryStub(t *testing.T) (bin, logPath string) {
	t.Helper()
	bin = t.TempDir()
	logPath = filepath.Join(bin, "docker.log")
	script := `#!/usr/bin/env bash
printf '%s\n' "$*" >>` + logPath + `
case " $* " in
*" help "*) printf '  registry   credentials\n' ;;
*" registry htpasswd -user "*) read -r pw; a="$*"; u=${a##*-user }; echo "${u%% *}:\$2a\$10\$hashof${#pw}" ;;
*" registry-sync "*) cat >/dev/null; echo "registry-writer:5000/bench/coding-core-base@sha256:` + strings.Repeat("c", 64) + `" ;;
*" save "*) echo tar ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "openssl"), []byte("#!/bin/sh\necho 0123456789abcdef0123456789abcdef0123456789abcdef\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, logPath
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// G3 B2: the registry credentials are generated once, outside the
// checkout, one identity per side, readable by owner and group only.
func TestEnsureRegistryAuth(t *testing.T) {
	bin, logPath := registryStub(t)
	auth := filepath.Join(t.TempDir(), "registry-auth")
	env := backupEnv(t, "HEIMDALL_REGISTRY_AUTH_DIR="+auth+"\n")
	run := func() string {
		out, err := runScript(t, bin, []string{"HEIMDALL_ENV_FILE=" + env, "HEIMDALL_PROJECT=y02-reg"}, "-c",
			`. hosted/lib.sh; ensure_registry_auth; echo "changed=$registry_auth_changed"`)
		if err != nil {
			t.Fatalf("ensure_registry_auth: %v\n%s", err, out)
		}
		return out
	}
	if out := run(); !strings.Contains(out, "changed=1") {
		t.Fatalf("first run wrote nothing:\n%s", out)
	}
	want := map[string]string{
		"registry/htpasswd": "heimdall-pull:$2a$10$hashof48",
		"runner/pull.auth":  "heimdall-pull:0123456789abcdef0123456789abcdef0123456789abcdef",
		"writer/htpasswd":   "heimdall-sync:$2a$10$hashof48",
		"sync/push.auth":    "heimdall-sync:0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	for f, line := range want {
		raw, err := os.ReadFile(filepath.Join(auth, f))
		if err != nil || strings.TrimSpace(string(raw)) != line {
			t.Errorf("%s = %q %v, want %q", f, raw, err, line)
		}
		if m := mode(t, filepath.Join(auth, f)); m != 0o640 {
			t.Errorf("%s mode %o", f, m)
		}
	}
	for _, d := range []string{"", "registry", "writer", "sync"} {
		if m := mode(t, filepath.Join(auth, d)); m != 0o750 {
			t.Errorf("%s/ mode %o", d, m)
		}
	}
	// Wave 4: the image publisher (group of the Docker socket) writes the
	// manifest and its lock in runner/; the shared worker mounts it read-only.
	if m := mode(t, filepath.Join(auth, "runner")); m != 0o770 {
		t.Errorf("runner/ mode %o", m)
	}
	tok, err := os.ReadFile(filepath.Join(auth, "runner", "publish.token"))
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{64}\n$`).Match(tok) {
		t.Errorf("publish.token = %q %v, want 64 hex characters", tok, err)
	}
	if m := mode(t, filepath.Join(auth, "runner", "publish.token")); m != 0o640 {
		t.Errorf("publish.token mode %o", m)
	}
	if _, err := os.Stat(filepath.Join(auth, "runner", "manifest")); err != nil {
		t.Errorf("no manifest: %v", err)
	}
	log, _ := os.ReadFile(logPath)
	if strings.Contains(string(log), "0123456789abcdef") {
		t.Fatal("a registry password reached a docker command line")
	}
	// A second run keeps what exists.
	if out := run(); strings.Contains(out, "changed=1") {
		t.Fatalf("second run regenerated credentials:\n%s", out)
	}
	if again, _ := os.ReadFile(filepath.Join(auth, "runner", "publish.token")); string(again) != string(tok) {
		t.Error("second run replaced publish.token")
	}
	// Never inside the checkout.
	inside := backupEnv(t, "HEIMDALL_REGISTRY_AUTH_DIR="+filepath.Join(mustAbs(t, "."), "hosted", "auth")+"\n")
	out, err := runScript(t, bin, []string{"HEIMDALL_ENV_FILE=" + inside}, "-c", `. hosted/lib.sh; ensure_registry_auth`)
	if err == nil || !strings.Contains(out, "inside the checkout") {
		t.Fatalf("credentials inside the checkout accepted: %v\n%s", err, out)
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// registry-sync.sh pushes through the write side only, records each pushed
// digest in the manifest and removes the write side afterwards.
func TestRegistrySyncPinsDigests(t *testing.T) {
	bin, logPath := registryStub(t)
	auth := filepath.Join(t.TempDir(), "registry-auth")
	env := backupEnv(t, "HEIMDALL_REGISTRY_AUTH_DIR="+auth+"\n")
	manifest := filepath.Join(auth, "runner", "manifest")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o750); err != nil {
		t.Fatal(err)
	}
	old := "bench/coding-core-base:1 sha256:" + strings.Repeat("a", 64) + "\npython:3.12 sha256:" + strings.Repeat("b", 64) + "\n"
	if err := os.WriteFile(manifest, []byte(old), 0o640); err != nil {
		t.Fatal(err)
	}
	out, err := runScript(t, bin, []string{"HEIMDALL_ENV_FILE=" + env, "HEIMDALL_PROJECT=y02-sync"}, "hosted/registry-sync.sh", "bench/coding-core-base:1")
	if err != nil {
		t.Fatalf("registry-sync: %v\n%s", err, out)
	}
	raw, _ := os.ReadFile(manifest)
	got := string(raw)
	if !strings.Contains(got, "bench/coding-core-base:1 sha256:"+strings.Repeat("c", 64)+"\n") || strings.Contains(got, strings.Repeat("a", 64)) ||
		!strings.Contains(got, "python:3.12 sha256:"+strings.Repeat("b", 64)) {
		t.Fatalf("manifest after sync:\n%s", got)
	}
	log, _ := os.ReadFile(logPath)
	l := string(log)
	for _, want := range []string{"up -d --no-deps registry-writer", "-e SYNC_REF=bench/coding-core-base:1 registry-sync"} {
		if !strings.Contains(l, want) {
			t.Errorf("docker log lacks %q:\n%s", want, l)
		}
	}
	// Wave 4: the writer stays up for the image publisher.
	if strings.Contains(l, "rm -sf registry-writer") {
		t.Errorf("registry-sync removed the writer the image publisher needs:\n%s", l)
	}
	if strings.Contains(l, "registry:5000/bench") {
		t.Error("pushed to the read-only registry")
	}
}
