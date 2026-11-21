//go:build integration

package sandbox

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Real-platform tests: go test -tags integration ./sandbox/ -run TestReal.
// Each needs the platform's own token in the environment and is skipped
// without it. Each creates one tiny sandbox (alpine, no network, 5 minute
// hard limit), runs echo, round-trips a file and always deletes it.
//
//	Modal:   MODAL_TOKEN_ID, MODAL_TOKEN_SECRET (and HEIMDALL_MODAL_PYTHON)
//	Daytona: DAYTONA_API_KEY
//	E2B:     E2B_API_KEY

func realRoundTrip(t *testing.T, p Platform) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := p.Check(ctx); err != nil {
		t.Fatalf("check: %v", err)
	}
	box, err := p.Create(ctx, Spec{Name: "heimdall-it-" + time.Now().UTC().Format("150405"), Image: "alpine:3.20",
		Egress: Egress{BlockAll: true}, Lifetime: 5 * time.Minute, Labels: map[string]string{"heimdall.role": "integration-test"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Logf("sandbox %s created", box.ID)
	defer func() {
		if err := p.Destroy(context.WithoutCancel(ctx), box); err != nil {
			t.Errorf("destroy: %v", err)
		} else {
			t.Logf("sandbox %s deleted", box.ID)
		}
		if err := p.Destroy(context.WithoutCancel(ctx), box); err != nil {
			t.Errorf("second destroy must be a no-op: %v", err)
		}
	}()
	res, err := p.Exec(ctx, box, Command{Argv: []string{"/bin/sh", "-c", "echo hello-$((40+2)); exit 3"}, Timeout: time.Minute})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.ExitCode != 3 || strings.TrimSpace(string(res.Output)) != "hello-42" {
		t.Fatalf("exec = %d %q, want 3 hello-42", res.ExitCode, res.Output)
	}
	payload := []byte("round trip \x00\x01\xff bytes\n")
	if err := p.Upload(ctx, box, "/tmp/heimdall-it/in.bin", payload); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := p.Exec(ctx, box, Command{Argv: []string{"cp", "/tmp/heimdall-it/in.bin", "/tmp/heimdall-it/out.bin"}}); err != nil {
		t.Fatalf("copy: %v", err)
	}
	got, err := p.Download(ctx, box, "/tmp/heimdall-it/out.bin", 1<<10)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("download = %q, want %q", got, payload)
	}
	if _, err := p.Download(ctx, box, "/tmp/heimdall-it/out.bin", 4); err == nil {
		t.Fatal("download over max must fail")
	}
}

func TestRealModal(t *testing.T) {
	id, secret := os.Getenv("MODAL_TOKEN_ID"), os.Getenv("MODAL_TOKEN_SECRET")
	if id == "" || secret == "" {
		t.Skip("MODAL_TOKEN_ID and MODAL_TOKEN_SECRET not set")
	}
	p, err := New(PlatformModal, Credentials{Token: id + ":" + secret})
	if err != nil {
		t.Fatal(err)
	}
	realRoundTrip(t, p)
}

func TestRealDaytona(t *testing.T) {
	tok := os.Getenv("DAYTONA_API_KEY")
	if tok == "" {
		t.Skip("DAYTONA_API_KEY not set")
	}
	p, err := New(PlatformDaytona, Credentials{Token: tok, Endpoint: os.Getenv("DAYTONA_API_URL")})
	if err != nil {
		t.Fatal(err)
	}
	realRoundTrip(t, p)
}

func TestRealE2B(t *testing.T) {
	tok := os.Getenv("E2B_API_KEY")
	if tok == "" {
		t.Skip("E2B_API_KEY not set")
	}
	p, err := New(PlatformE2B, Credentials{Token: tok, Endpoint: os.Getenv("E2B_DOMAIN")})
	if err != nil {
		t.Fatal(err)
	}
	realRoundTrip(t, p)
}
