package work

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/validate"
)

func TestEnvImageTag(t *testing.T) {
	req := issuebench.WorkRequest{Repo: "Acme/My.Repo_X", Version: "2025-03", JobID: "J_AbC123"}
	if got := EnvImageTag(req); got != "heimdall-issuebench/acme-my-repo-x:2025-03-j_abc123" {
		t.Fatalf("tag %q", got)
	}
	req.Repo = strings.Repeat("a", 90) + "/" + strings.Repeat("b", 90)
	got := EnvImageTag(req)
	if len(got) > 128 || !regexp.MustCompile(`^[a-z0-9/_.:-]+$`).MatchString(got) {
		t.Fatalf("long tag %q (%d)", got, len(got))
	}
}

func TestScriptStates(t *testing.T) {
	c := candidate()
	base := Script(&c, validate.Run{State: validate.StateBase, Command: "sh runtests.sh x"})
	if strings.Contains(base, "patch") || base != "sh runtests.sh x\n" {
		t.Fatalf("base script %q", base)
	}
	gold := Script(&c, validate.Run{State: validate.StateGold, Command: "sh runtests.sh x"})
	for _, w := range []string{"/tmp/tests.patch", "/tmp/fix.patch", "git apply --whitespace=nowarn", "HEIMDALL_PATCH_FAILED fix; exit 97", "sh runtests.sh x\n"} {
		if !strings.Contains(gold, w) {
			t.Fatalf("gold script lacks %q:\n%s", w, gold)
		}
	}
	if strings.Index(gold, "tests.patch") > strings.Index(gold, "fix.patch") {
		t.Fatal("tests patch must apply before the fix")
	}
}

func TestRunnerResults(t *testing.T) {
	c := candidate()
	run := validate.Run{State: validate.StateTests, Command: "sh runtests.sh t"}
	var stdin string
	r := TestRunner{Image: "img", JobID: "j1", Docker: func(_ context.Context, in []byte, args ...string) ([]byte, error) {
		stdin = string(in)
		return []byte("FAIL t::x\n"), &issuebench.ExitError{Code: 1}
	}}
	out, err := r.Run(context.Background(), nil, &c, run)
	if err != nil || string(out) != "FAIL t::x\n" || !strings.Contains(stdin, "base64 -d") {
		t.Fatalf("a non-zero exit is a result: %q %v", out, err)
	}

	r.Docker = func(context.Context, []byte, ...string) ([]byte, error) {
		return []byte("error: patch failed\nHEIMDALL_PATCH_FAILED tests\n"), &issuebench.ExitError{Code: 97}
	}
	if _, err := r.Run(context.Background(), nil, &c, run); !errors.Is(err, validate.ErrPatchApply) {
		t.Fatalf("patch: %v", err)
	}

	r.Docker = func(ctx context.Context, _ []byte, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	r.Timeout = 20 * time.Millisecond
	if _, err := r.Run(context.Background(), nil, &c, run); !errors.Is(err, validate.ErrTimeout) {
		t.Fatalf("timeout: %v", err)
	}

	r.Timeout = 0
	r.Docker = func(context.Context, []byte, ...string) ([]byte, error) { return nil, errors.New("no docker") }
	if _, err := r.Run(context.Background(), nil, &c, run); err == nil || !strings.Contains(err.Error(), "docker run: no docker") {
		t.Fatalf("broken docker: %v", err)
	}
}
