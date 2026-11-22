package main

import (
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/drain"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// TestDrainCommand: `bifrost-bench drain` turns drain mode on with the
// token file, waits while a lease is in flight, and exits 0 once none is;
// with a lease that never ends it exits 5 after the timeout, drain on.
func TestDrainCommand(t *testing.T) {
	t.Cleanup(func() { drain.Stop(); drain.SetToken("") })
	tokFile := drain.TokenPath(filepath.Join(t.TempDir(), "runs"))
	if err := drain.WriteToken(tokFile); err != nil {
		t.Fatal(err)
	}
	var inFlight atomic.Int64
	inFlight.Store(1)
	remove := drain.AddSource(func() int { return int(inFlight.Load()) })
	defer remove()
	r := router.New()
	handlers.NewDrainHandler(schemas.ModeLive, true, nil, nil).RegisterRoutes(r)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	base := "http://" + ln.Addr().String()
	args := []string{"-server", base, "-token-file", tokFile, "-every", "20ms"}

	go func() {
		time.Sleep(150 * time.Millisecond)
		inFlight.Store(0) // the last run finished
	}()
	if code := runDrain(append(args, "-timeout", "10s")); code != exitOK {
		t.Fatalf("drain exit %d, want 0", code)
	}
	if !drain.Active() {
		t.Fatal("drain mode is off after drain")
	}
	if code := runDrain(append(args, "-off")); code != exitOK || drain.Active() {
		t.Fatalf("drain -off exit %d, active %v", code, drain.Active())
	}
	inFlight.Store(3)
	if code := runDrain(append(args, "-timeout", "100ms")); code != exitDrainTimeout {
		t.Fatalf("drain with a stuck lease exit %d, want %d", code, exitDrainTimeout)
	}
	if !drain.Active() {
		t.Fatal("drain mode was turned off by a timeout")
	}
	if code := runDrain([]string{"-server", base, "-token-file", filepath.Join(t.TempDir(), "none")}); code == exitOK {
		t.Fatal("drain without a token file succeeded")
	}
}

func TestLocalServerURL(t *testing.T) {
	for in, want := range map[string]string{
		"0.0.0.0:8090":   "http://127.0.0.1:8090",
		":8090":          "http://127.0.0.1:8090",
		"[::]:8090":      "http://127.0.0.1:8090",
		"127.0.0.1:8090": "http://127.0.0.1:8090",
		"10.0.0.5:9000":  "http://10.0.0.5:9000",
	} {
		if got := localServerURL(in); got != want {
			t.Errorf("localServerURL(%q) = %q, want %q", in, got, want)
		}
	}
}
