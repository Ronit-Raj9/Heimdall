package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakePython stands in for the interpreter: it reads the request, checks
// the token arrived in the environment and answers by op.
const fakePython = `#!/bin/sh
req="$(cat)"
echo "$req" >> "$LOG_DIR_FAKE/requests"
echo "the SDK may print details here: $MODAL_TOKEN_SECRET" >&2
if [ "$MODAL_TOKEN_ID" != "ak-id1234" ] || [ "$MODAL_TOKEN_SECRET" != "as-secret5678" ]; then
	printf '{"error":"unauthorized","message":"token ak-id1234 refused"}'; exit 1
fi
if [ ! -f "$MODAL_CONFIG_PATH" ] || [ -s "$MODAL_CONFIG_PATH" ]; then
	printf '{"error":"failed","message":"config not isolated"}'; exit 1
fi
case "$req" in
*'"op":"check"'*) printf '{"ok":true}' ;;
*'"op":"create"'*) printf '{"sandbox_id":"sb-1"}' ;;
*'"op":"exec"'*) printf '{"exit_code":4,"output":"aGk="}' ;;
*'"op":"upload"'*) printf '{}' ;;
*'"op":"download"'*'"max_bytes":1,'*) printf '{"error":"too_large","message":"big"}'; exit 1 ;;
*'"op":"download"'*) printf '{"data":"aGk="}' ;;
*'"op":"terminate"'*'"sb-gone"'*) printf '{"error":"not_found","message":"gone"}'; exit 1 ;;
*'"op":"terminate"'*) printf '{}' ;;
*) printf '{"error":"failed","message":"unknown"}'; exit 1 ;;
esac
`

func fakeModal(t *testing.T, token string) (*Modal, string) {
	t.Helper()
	dir := t.TempDir()
	py := filepath.Join(dir, "python")
	if err := os.WriteFile(py, []byte(fakePython), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOG_DIR_FAKE", dir)
	m, err := NewModal(Credentials{Token: token}, py)
	if err != nil {
		t.Fatal(err)
	}
	return m, dir
}

func TestModalHelperProtocol(t *testing.T) {
	t.Setenv("MODAL_TOKEN_ID", "ak-from-laptop") // a token of this machine must never be used
	m, dir := fakeModal(t, "ak-id1234:as-secret5678")
	// the fake reads LOG_DIR_FAKE from its environment, which call() builds
	// from scratch; pass it through the allowed list for the test.
	t.Setenv("TMPDIR", dir)
	m.extraEnv = []string{"LOG_DIR_FAKE=" + dir}
	ctx := context.Background()
	if err := m.Check(ctx); err != nil {
		t.Fatal(err)
	}
	box, err := m.Create(ctx, Spec{Name: "n", Image: "alpine:3.20", Egress: Egress{AllowHosts: []string{"gw.example.com"}}, CPUs: 1, MemGB: 0.5})
	if err != nil || box.ID != "sb-1" {
		t.Fatalf("create = %+v %v", box, err)
	}
	res, err := m.Exec(ctx, box, Command{Argv: []string{"echo", "hi"}})
	if err != nil || res.ExitCode != 4 || string(res.Output) != "hi" {
		t.Fatalf("exec = %+v %v", res, err)
	}
	if err := m.Upload(ctx, box, "/tmp/a", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if b, err := m.Download(ctx, box, "/tmp/a", 10); err != nil || string(b) != "hi" {
		t.Fatalf("download = %q %v", b, err)
	}
	if _, err := m.Download(ctx, box, "/tmp/a", 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("download over max = %v", err)
	}
	if err := m.Destroy(ctx, box); err != nil {
		t.Fatal(err)
	}
	if err := m.Destroy(ctx, &Box{ID: "sb-gone"}); err != nil {
		t.Fatalf("destroy of a gone sandbox = %v", err)
	}
	reqs, _ := os.ReadFile(filepath.Join(dir, "requests"))
	if strings.Contains(string(reqs), "secret5678") {
		t.Fatal("the token went into a request body")
	}
	if !strings.Contains(string(reqs), `"allow_hosts":["gw.example.com"]`) || !strings.Contains(string(reqs), `"memory_mb":512`) {
		t.Fatalf("create request = %s", reqs)
	}
}

func TestModalBadTokenAndFormat(t *testing.T) {
	if _, err := New(PlatformModal, Credentials{Token: "no-separator"}); err == nil {
		t.Fatal("a token without id and secret must be refused")
	}
	m, dir := fakeModal(t, "ak-id1234:as-wrong0000")
	m.extraEnv = []string{"LOG_DIR_FAKE=" + dir}
	err := m.Check(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bad token = %v", err)
	}
	if strings.Contains(err.Error(), "ak-id1234") || strings.Contains(err.Error(), "as-wrong0000") {
		t.Fatalf("error leaks the token: %v", err)
	}
	m2, _ := NewModal(Credentials{Token: "ak-a:as-b"}, filepath.Join(dir, "no-such-python"))
	if err := m2.Check(context.Background()); err == nil || !strings.Contains(err.Error(), ModalPythonEnv) {
		t.Fatalf("missing interpreter = %v", err)
	}
}
