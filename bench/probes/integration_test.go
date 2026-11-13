//go:build integration

package probes

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Integration tests for the helper-container writes. They need Docker and
// the images below; run with:
//
//	GOWORK=off go test -tags integration ./probes/...
//
// BENCH_HELPER_IMAGE overrides the helper (default alpine:3.20), and
// BENCH_AGENT_IMAGE the image the shim runs in (default
// bench/coding-core-base:1, which ships dash, python and pytest).

func integrationImages(t *testing.T) (utils.Docker, string, string) {
	t.Helper()
	d := utils.Docker{}
	helper := envOr("BENCH_HELPER_IMAGE", "alpine:3.20")
	agent := envOr("BENCH_AGENT_IMAGE", "bench/coding-core-base:1")
	for _, img := range []string{helper, agent} {
		if _, err := d.MustRun(context.Background(), "image", "inspect", img); err != nil {
			t.Skipf("image %s not available: %v", img, err)
		}
	}
	return d, helper, agent
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// testVolume creates a labelled volume and removes it when the test ends.
func testVolume(t *testing.T, d utils.Docker, runID string) string {
	t.Helper()
	name := utils.NewID("bench_it", 10)
	args := append([]string{"volume", "create"}, utils.LabelArgs("e_it", runID)...)
	if _, err := d.MustRun(context.Background(), append(args, name)...); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := d.MustRun(context.Background(), "volume", "rm", "-f", name); err != nil {
			t.Errorf("remove volume %s: %v", name, err)
		}
	})
	return name
}

// inVolume runs script in image with vol mounted at target and returns stdout
// and the exit code.
func inVolume(t *testing.T, d utils.Docker, image, user, vol, target, script string) (string, int) {
	t.Helper()
	args := []string{"run", "--rm", "--network", "none", "--user", user, "--read-only", "--tmpfs", "/tmp", "--tmpfs", "/fake:exec,mode=1777"}
	args = append(args, utils.LabelArgs("e_it", "r_it")...)
	args = append(args, "--mount", "type=volume,source="+vol+",target="+target, "--entrypoint", "/bin/sh", image, "-c", script)
	res, err := d.Run(context.Background(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return string(res.Stdout) + string(res.Stderr), res.ExitCode
}

func TestIntegrationFixtureWrite(t *testing.T) {
	d, helper, _ := integrationImages(t)
	vol := testVolume(t, d, "r_it_fix")
	rc := fixtureRun(t)
	rc.Handle.Volume = vol
	rc.Probe.Apply.Overlay.To = "docs/{{agent.notes.project_file}}"
	l := newFixtureLayer(Deps{Docker: d, HelperImage: helper})
	rec, err := l.Arm(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	out, code := inVolume(t, d, helper, "0:0", vol, "/w", `stat -c '%u:%g %a %n' /w/docs /w/docs/CLAUDE.md && cat /w/docs/CLAUDE.md && sha256sum /w/docs/CLAUDE.md`)
	if code != 0 {
		t.Fatalf("inspect: %s", out)
	}
	for _, want := range []string{"1000:1000 755 /w/docs\n", "1000:1000 644 /w/docs/CLAUDE.md\n", "# Notes\nRun python -m pytest ledger/\n<!-- ref: nonce_"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, strings.TrimPrefix(rec.Steps[schemas.StepArmed].Detail, "sha256 ")) {
		t.Fatalf("armed hash %q not the file's hash:\n%s", rec.Steps[schemas.StepArmed].Detail, out)
	}
}

func TestIntegrationToolShim(t *testing.T) {
	d, helper, agent := integrationImages(t)
	vol := testVolume(t, d, "r_it_shim")
	rc := &schemas.RunContext{
		Run:    &schemas.Run{RunID: "r_it_shim", ExpID: "e_it"},
		Probe:  &schemas.ProbeSpec{ID: "test-crash", Apply: schemas.ProbeApply{Shim: &schemas.ShimSpec{Tool: "pytest", Mode: "crash", Calls: 2, ExitCode: 139}}},
		Handle: &schemas.EnvironmentHandle{AuxVolume: vol},
	}
	l := newToolShimLayer(Deps{Docker: d, HelperImage: helper})
	if _, err := l.Arm(context.Background(), rc); err != nil {
		t.Fatal(err)
	}

	// The agent image (dash) as uid 1000: two crashes, then real pytest via
	// python -m, a plain python call untouched, and the shims read-only.
	script := `export PATH=/opt/bench/shim:$PATH
pytest -q; echo "exit=$?"
pytest -q; echo "exit=$?"
python -m pytest --version; echo "exit=$?"
python -c 'print(6*7)'; echo "exit=$?"
echo x > /opt/bench/shim/pytest 2>/dev/null && echo "shim writable" || echo "shim read-only"
`
	out, code := inVolume(t, d, agent, "1000:1000", vol, "/opt/bench", script)
	if code != 0 {
		t.Fatalf("agent script: %s", out)
	}
	for _, want := range []string{"Fatal Python error: Segmentation fault", "exit=139\n", "pytest 8.", "exit=0\n", "42\n", "shim read-only"} {
		if !strings.Contains(out, want) {
			t.Fatalf("agent output lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "exit=139") != 2 {
		t.Fatalf("want two crashes:\n%s", out)
	}

	// Busybox ash, as another POSIX shell, with a fake pytest.
	bb := `printf '#!/bin/sh\necho REAL pytest "$@"\n' > /fake/pytest && chmod +x /fake/pytest
export PATH=/opt/bench/shim:/fake:$PATH
pytest -k x; echo "exit=$?"
`
	out, code = inVolume(t, d, helper, "1000:1000", vol, "/opt/bench", bb)
	if code != 0 || !strings.Contains(out, "REAL pytest -k x") || !strings.Contains(out, "exit=0") {
		t.Fatalf("busybox run: %d %s", code, out)
	}

	rec, err := l.Receipts(context.Background(), rc, &schemas.Evidence{})
	if err != nil {
		t.Fatal(err)
	}
	if f := rec.Steps[schemas.StepFired]; f.State != schemas.StepYes || f.Detail != "2" {
		t.Fatalf("fired %+v", f)
	}
}
