package utils

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestStartExternalEgress(t *testing.T) {
	ctx := context.Background()
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_1", ExpID: "e_1"}, Dir: t.TempDir()}
	t.Run("no targets starts nothing", func(t *testing.T) {
		d, log := fakeDocker(t, "")
		e, err := StartExternalEgress(ctx, d, rc, "helper:1", "bench-xnet-r_1", nil)
		if e != nil || err != nil || calls(t, log) != nil {
			t.Fatalf("started something: %v %v", e, err)
		}
	})
	t.Run("one forwarder per api", func(t *testing.T) {
		d, log := fakeDocker(t, "")
		targets := map[string]string{"weather": "api.weather.test:443", "fx": "fx.test:80"}
		e, err := StartExternalEgress(ctx, d, rc, "helper:1", "bench-xnet-r_1", targets)
		if err != nil {
			t.Fatal(err)
		}
		got := calls(t, log)
		if len(got) != 8 {
			t.Fatalf("docker calls:\n%s", strings.Join(got, "\n"))
		}
		// Sorted by id: fx first.
		if !strings.HasSuffix(got[0], "bench-eg-r_1-fx") || !strings.Contains(got[1], "BENCH_EGRESS_TARGET=fx.test:80") ||
			!strings.Contains(got[1], "--cap-drop ALL") || !strings.Contains(got[1], "--read-only") || !strings.Contains(got[1], "helper:1 -I -c") ||
			got[2] != "network connect bench-xnet-r_1 bench-eg-r_1-fx" || got[3] != "start bench-eg-r_1-fx" ||
			!strings.Contains(got[5], "BENCH_EGRESS_TARGET=api.weather.test:443") {
			t.Fatalf("docker calls:\n%s", strings.Join(got, "\n"))
		}
		var rec ExternalEgressRecord
		b, err := os.ReadFile(filepath.Join(rc.Dir, ExternalFolder, ExternalEgressFile))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &rec); err != nil || rec.Allowed["weather"] != "api.weather.test:443" || rec.Joined != "bench-xnet-r_1" || len(rec.Forwarders) != 2 {
			t.Fatalf("record %s: %v", b, err)
		}
		e.Stop(ctx)
		got = calls(t, log)
		if got[len(got)-1] != "network rm bench-eg-r_1-weather" {
			t.Fatalf("stop calls:\n%s", strings.Join(got, "\n"))
		}
	})
	t.Run("failure removes what started", func(t *testing.T) {
		d, log := fakeDocker(t, "network connect")
		_, err := StartExternalEgress(ctx, d, rc, "helper:1", "bench-xnet-r_1", map[string]string{"fx": "fx.test:80"})
		if err == nil {
			t.Fatal("no error")
		}
		got := calls(t, log)
		if got[len(got)-2] != "rm -f bench-eg-r_1-fx" || got[len(got)-1] != "network rm bench-eg-r_1-fx" {
			t.Fatalf("calls:\n%s", strings.Join(got, "\n"))
		}
	})
	t.Run("bad input refused", func(t *testing.T) {
		d, _ := fakeDocker(t, "")
		for name, f := range map[string]func() error{
			"network": func() error {
				_, err := StartExternalEgress(ctx, d, rc, "helper:1", "bridge", map[string]string{"fx": "fx.test:80"})
				return err
			},
			"target": func() error {
				_, err := StartExternalEgress(ctx, d, rc, "helper:1", "x", map[string]string{"fx": "fx.test"})
				return err
			},
			"image": func() error {
				_, err := StartExternalEgress(ctx, d, rc, "", "x", map[string]string{"fx": "fx.test:80"})
				return err
			},
		} {
			if f() == nil {
				t.Fatalf("%s accepted", name)
			}
		}
	})
	if ExternalEgressAddr("r_1", "fx") != "bench-eg-r_1-fx:9300" {
		t.Fatal(ExternalEgressAddr("r_1", "fx"))
	}
}
