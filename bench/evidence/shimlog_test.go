package evidence

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const shimLogFixture = `{"tool":"pytest","argv":"-q","call":1,"action":"crash","nonce":"nonce_secret","at":"2026-11-14T10:00:01Z"}
{"tool":"pytest","argv":"-q tests/test_money.py","call":2,"action":"empty","at":"2026-11-14T10:00:05Z"}

{"tool":"pytest","argv":"-q","call":3,"action":"pass","at":"2026-11-14T10:00:09Z"}
{"tool":"pytest","argv":"-q","call":4,"act
`

func TestParseShimLog(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		calls int
		bad   int
	}{
		{name: "torn last line skipped", raw: shimLogFixture, calls: 3, bad: 1},
		{name: "empty", raw: "", calls: 0, bad: 0},
		{name: "line without tool", raw: `{"call":1,"action":"crash"}` + "\n", calls: 0, bad: 1},
		{name: "line without action", raw: `{"tool":"pytest","call":1}` + "\n", calls: 0, bad: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls, bad := ParseShimLog([]byte(tt.raw))
			if len(calls) != tt.calls || bad != tt.bad {
				t.Errorf("got %d calls, %d bad; want %d, %d", len(calls), bad, tt.calls, tt.bad)
			}
			if calls == nil {
				t.Errorf("calls is nil; an observed empty log must be non-nil")
			}
		})
	}
}

// fakeDocker writes a stand-in docker binary that records its argv and
// behaves according to FAKE_DOCKER_MODE. No real docker is involved.
func fakeDocker(t *testing.T, mode, stdout string) (utils.Docker, string) {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	outFile := filepath.Join(dir, "out")
	if err := os.WriteFile(outFile, []byte(stdout), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
printf '%s\n' "$@" > "$FAKE_DOCKER_ARGS"
case "$FAKE_DOCKER_MODE" in
  missing) echo "head: /opt/bench/log/shim.jsonl: No such file or directory" >&2; exit 1 ;;
  broken) echo "docker: volume not found" >&2; exit 125 ;;
esac
cat "$FAKE_DOCKER_OUT"
`
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_DOCKER_ARGS", argsFile)
	t.Setenv("FAKE_DOCKER_OUT", outFile)
	t.Setenv("FAKE_DOCKER_MODE", mode)
	return utils.Docker{Binary: bin}, argsFile
}

func shimRC(t *testing.T) *schemas.RunContext {
	rc := testRC(t)
	rc.Probe = &schemas.ProbeSpec{ID: "test-crash", Layer: schemas.LayerToolShim}
	rc.Handle = &schemas.EnvironmentHandle{AuxVolume: "bench-aux-r_1"}
	return rc
}

func TestShimLogCollect(t *testing.T) {
	t.Run("reads the aux volume and emits fault events", func(t *testing.T) {
		docker, argsFile := fakeDocker(t, "", shimLogFixture)
		ev := &schemas.Evidence{}
		src := newShimLogSource(Deps{Docker: docker, HelperImage: "busybox:1.36"})
		if err := src.Collect(context.Background(), shimRC(t), ev); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Join(strings.Fields(string(b)), " ")
		for _, want := range []string{
			"run --rm --network none",
			"--label bifrost-bench=1",
			"--label bifrost-bench.run=r_1",
			"-v bench-aux-r_1:/opt/bench:ro busybox:1.36 head -c 8388608 /opt/bench/log/shim.jsonl",
		} {
			if !strings.Contains(args, want) {
				t.Errorf("docker args %q missing %q", args, want)
			}
		}
		if len(ev.ShimLog) != 3 {
			t.Fatalf("shim log has %d calls, want 3", len(ev.ShimLog))
		}
		var kinds []string
		for _, e := range ev.Events {
			kinds = append(kinds, string(e.Type)+":"+e.Summary)
			if strings.Contains(e.Summary, "nonce_secret") {
				t.Errorf("event leaks the nonce: %q", e.Summary)
			}
		}
		want := []string{
			"fault:shim crash on pytest call 1",
			"fault:shim empty on pytest call 2",
			"error:shim log: 1 malformed lines skipped",
		}
		if strings.Join(kinds, "|") != strings.Join(want, "|") {
			t.Errorf("events %v, want %v", kinds, want)
		}
		if e := ev.Events[0]; !e.IsError || e.Evidence != schemas.EvidenceBoundary || e.Target != "pytest -q" || e.EventID != "r_1:shim_log:1" || e.TS.IsZero() {
			t.Errorf("crash event = %+v", e)
		}
		if ev.Events[1].IsError {
			t.Errorf("empty-run event flagged as error")
		}
	})
	t.Run("missing log is an empty observation", func(t *testing.T) {
		docker, _ := fakeDocker(t, "missing", "")
		ev := &schemas.Evidence{}
		if err := newShimLogSource(Deps{Docker: docker, HelperImage: "busybox"}).Collect(context.Background(), shimRC(t), ev); err != nil {
			t.Fatal(err)
		}
		if ev.ShimLog == nil || len(ev.ShimLog) != 0 || len(ev.Events) != 0 {
			t.Errorf("shim log = %#v events = %+v", ev.ShimLog, ev.Events)
		}
	})
	t.Run("not a shim probe", func(t *testing.T) {
		docker, argsFile := fakeDocker(t, "", shimLogFixture)
		for _, probe := range []*schemas.ProbeSpec{nil, {ID: "model-outage", Layer: schemas.LayerGateway}} {
			rc := shimRC(t)
			rc.Probe = probe
			ev := &schemas.Evidence{}
			if err := newShimLogSource(Deps{Docker: docker, HelperImage: "busybox"}).Collect(context.Background(), rc, ev); err != nil {
				t.Fatal(err)
			}
			if ev.ShimLog != nil {
				t.Errorf("probe %v: shim log read", probe)
			}
		}
		if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
			t.Errorf("docker was called")
		}
	})
	t.Run("errors", func(t *testing.T) {
		docker, _ := fakeDocker(t, "broken", "")
		noAux := shimRC(t)
		noAux.Handle = nil
		cases := []struct {
			name string
			deps Deps
			rc   *schemas.RunContext
			want string
		}{
			{"helper fails", Deps{Docker: docker, HelperImage: "busybox"}, shimRC(t), "volume not found"},
			{"no aux volume", Deps{Docker: docker, HelperImage: "busybox"}, noAux, "no aux volume"},
			{"no helper image", Deps{Docker: docker}, shimRC(t), "no helper image"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				err := newShimLogSource(c.deps).Collect(context.Background(), c.rc, &schemas.Evidence{})
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Errorf("err = %v, want %q", err, c.want)
				}
			})
		}
	})
}
