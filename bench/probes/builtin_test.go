package probes

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestBuiltin(t *testing.T) {
	layers := Builtin(Deps{HelperImage: "helper:1"})
	want := []schemas.ProbeLayerName{schemas.LayerFixture, schemas.LayerToolShim, schemas.LayerGateway, schemas.LayerTool, schemas.LayerService, schemas.LayerAdapter}
	if len(layers) != len(want) {
		t.Fatalf("got %d layers, want %d", len(layers), len(want))
	}
	for i, l := range layers {
		t.Run(string(want[i]), func(t *testing.T) {
			if l.GetName() != string(want[i]) {
				t.Fatalf("layer %d is %q, want %q", i, l.GetName(), want[i])
			}
			if l.GetVersion() == "" || len(l.Steps()) == 0 {
				t.Fatalf("layer %q has no version or steps", l.GetName())
			}
		})
	}
}

func TestStartReceipt(t *testing.T) {
	rc := &schemas.RunContext{Probe: &schemas.ProbeSpec{ID: "stale-notes"}}
	rec := startReceipt(rc, schemas.LayerFixture, fixtureSteps)
	if rec.ProbeID != "stale-notes" || len(rec.Steps) != len(schemas.AllReceiptSteps) {
		t.Fatalf("bad receipt %+v", rec)
	}
	cases := map[schemas.ReceiptStep]schemas.StepState{
		schemas.StepAssigned:  schemas.StepUnknown,
		schemas.StepFired:     schemas.StepNotApplicable,
		schemas.StepDelivered: schemas.StepNotApplicable,
		schemas.StepAbsorbed:  schemas.StepNotApplicable,
		schemas.StepVisible:   schemas.StepUnknown,
	}
	for step, want := range cases {
		t.Run(string(step), func(t *testing.T) {
			if got := rec.Steps[step].State; got != want {
				t.Fatalf("%s = %s, want %s", step, got, want)
			}
		})
	}
}

func TestNonceSeen(t *testing.T) {
	const nonce = "nonce_abc123"
	rows := func(complete bool, raws ...string) *schemas.Evidence {
		log := &schemas.RequestLog{Complete: complete, Reason: "timeout"}
		for i, r := range raws {
			log.Requests = append(log.Requests, schemas.GatewayRequest{ID: string(rune('a' + i)), Raw: []byte(r)})
		}
		return &schemas.Evidence{Requests: log}
	}
	cases := []struct {
		name  string
		ev    *schemas.Evidence
		nonce string
		want  schemas.StepState
	}{
		{"seen", rows(true, `{"x":1}`, `{"content":"<!-- ref: nonce_abc123 -->"}`), nonce, schemas.StepYes},
		{"seen in incomplete log", rows(false, `ref: nonce_abc123`), nonce, schemas.StepYes},
		{"absent, complete log", rows(true, `{"x":1}`), nonce, schemas.StepNo},
		{"absent, incomplete log", rows(false, `{"x":1}`), nonce, schemas.StepUnknown},
		{"no log", &schemas.Evidence{}, nonce, schemas.StepUnknown},
		{"no nonce", rows(true, `{"x":1}`), "", schemas.StepUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := nonceSeen(c.ev, c.nonce)
			if got.State != c.want {
				t.Fatalf("got %+v, want %s", got, c.want)
			}
		})
	}
}

func TestClaimMentions(t *testing.T) {
	terms := []string{"stale", "CLAUDE.md"}
	cases := []struct {
		name string
		ev   *schemas.Evidence
		want schemas.StepState
	}{
		{"mentions term", &schemas.Evidence{Claim: &schemas.Claim{Text: "The notes were STALE, I followed the code."}}, schemas.StepYes},
		{"mentions file", &schemas.Evidence{Claim: &schemas.Claim{Text: "claude.md disagreed with the tests"}}, schemas.StepYes},
		{"silent", &schemas.Evidence{Claim: &schemas.Claim{Text: "Done, all tests pass."}}, schemas.StepNo},
		{"no message", &schemas.Evidence{Claim: &schemas.Claim{Made: true}}, schemas.StepUnknown},
		{"no claim", &schemas.Evidence{}, schemas.StepUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := claimMentions(c.ev, terms); got.State != c.want {
				t.Fatalf("got %+v, want %s", got, c.want)
			}
		})
	}
}

func TestGradeEffect(t *testing.T) {
	cases := []struct {
		name string
		ev   *schemas.Evidence
		want schemas.StepState
	}{
		{"passed", &schemas.Evidence{Grade: &schemas.Grade{Passed: true}}, schemas.StepYes},
		{"failed", &schemas.Evidence{Grade: &schemas.Grade{}}, schemas.StepNo},
		{"undecided", &schemas.Evidence{Grade: &schemas.Grade{Passed: true, InvalidEvaluator: true}}, schemas.StepUnknown},
		{"no grade", &schemas.Evidence{}, schemas.StepUnknown},
		{"nil evidence", nil, schemas.StepUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := gradeEffect(c.ev); got.State != c.want {
				t.Fatalf("got %+v, want %s", got, c.want)
			}
		})
	}
}

func TestParseSHA256Sums(t *testing.T) {
	h := strings.Repeat("a", 64)
	got := parseSHA256Sums([]byte(h + "  /w/CLAUDE.md\n" + h + " */a/shim/pytest\nnoise\n"))
	if got["/w/CLAUDE.md"] != h || got["/a/shim/pytest"] != h || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestHelperRunRejectsBadVolume(t *testing.T) {
	d := Deps{HelperImage: "helper:1"}
	for _, vol := range []string{"", "/etc", "vol,target=/x", "../x"} {
		t.Run(vol, func(t *testing.T) {
			if _, err := d.helperRun(t.Context(), nil, []helperMount{{Volume: vol, Target: "/w"}}, "true"); err == nil {
				t.Fatalf("volume %q accepted", vol)
			}
		})
	}
	if _, err := (Deps{}).helperRun(t.Context(), nil, nil, "true"); err == nil {
		t.Fatal("want error without a helper image")
	}
}

// fakeDocker returns a Docker whose binary runs the helper script on the host
// under a temp root, with /w and /a mapped into it and chown a no-op, so the
// real helper scripts are exercised without Docker. It records argv.
func fakeDocker(t *testing.T) (utils.Docker, string) {
	t.Helper()
	for _, tool := range []string{"sh", "sed", "base64", "sha256sum", "head"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	for _, d := range []string{bin, filepath.Join(root, "w"), filepath.Join(root, "a")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExec(t, filepath.Join(bin, "chown"), "#!/bin/sh\nexit 0\n")
	script := `#!/bin/sh
root='` + root + `'
printf '%s\n' "$@" > "$root/argv"
while [ $# -gt 0 ] && [ "$1" != "-c" ]; do shift; done
shift
script=$(printf '%s' "$1" | sed -e "s#/w/#$root/w/#g" -e "s#/a/#$root/a/#g" -e "s#\"/w\"#\"$root/w\"#g")
shift 2
PATH="$root/bin:$PATH" sh -c "$script" sh "$@" > "$root/out"
rc=$?
sed "s#$root##g" "$root/out"
exit $rc
`
	writeExec(t, filepath.Join(bin, "docker"), script)
	return utils.Docker{Binary: filepath.Join(bin, "docker")}, root
}

func writeExec(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readArgv(t *testing.T, root string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "argv"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func TestHelperRunArgv(t *testing.T) {
	docker, root := fakeDocker(t)
	d := Deps{Docker: docker, HelperImage: "helper:1"}
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_1", ExpID: "e_1"}}
	if _, err := d.helperRun(t.Context(), rc, []helperMount{{Volume: "vol_1", Target: "/a", ReadOnly: true}}, "true"); err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(readArgv(t, root), " ")
	for _, want := range []string{
		"run --rm --network none --user 0:0 --cap-drop ALL",
		"--security-opt no-new-privileges",
		"--label bifrost-bench=1 --label bifrost-bench.exp=e_1 --label bifrost-bench.run=r_1",
		"--mount type=volume,source=vol_1,target=/a,readonly",
		"--entrypoint /bin/sh helper:1 -c true sh",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv %q lacks %q", argv, want)
		}
	}
}

// fakeDockerFailing returns a Docker whose binary always exits 1.
func fakeDockerFailing(t *testing.T) utils.Docker {
	t.Helper()
	p := filepath.Join(t.TempDir(), "docker")
	writeExec(t, p, "#!/bin/sh\necho 'Cannot connect to the Docker daemon' >&2\nexit 1\n")
	return utils.Docker{Binary: p}
}
