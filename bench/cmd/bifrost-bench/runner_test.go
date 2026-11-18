package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/valyala/fasthttp"
)

func TestRunnerFlags(t *testing.T) {
	args := []string{"-server", "https://h.example.com", "-token-file", "/tmp/t", "-pool", "gpu", "-slots", "4", "-name", "box-01",
		"-labels", "gpu, region=eu,", "-spend-cap-usd", "2.5", "-config", "deploy/bench.yaml"}
	for _, cmd := range []string{"runner start", "worker"} {
		f, err := parseRunnerFlags(cmd, args)
		if err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		if f.server != "https://h.example.com" || f.pool != "gpu" || f.slots != 4 || f.name != "box-01" || f.spendCap != 2.5 ||
			strings.Join(f.labelList(), "|") != "gpu|region=eu" {
			t.Fatalf("%s: %+v", cmd, f)
		}
	}
	// -id is the former name of -name.
	if f, err := parseRunnerFlags("worker", []string{"-server", "http://127.0.0.1:8090", "-token-file", "t", "-id", "w7"}); err != nil || f.name != "w7" || f.slots != 1 {
		t.Fatalf("-id: %+v %v", f, err)
	}
	for _, bad := range [][]string{
		{"-token-file", "t"},
		{"-server", "s"},
		{"-server", "s", "-token-file", "t", "-slots", "0"},
		{"-server", "s", "-token-file", "t", "-slots", "65"},
		{"-server", "s", "-token-file", "t", "-pool", "has space"},
		{"-server", "s", "-token-file", "t", "-spend-cap-usd", "-1"},
		{"-server", "s", "-token-file", "t", "extra"},
		{"-server", "s", "-token-file", "t", "-nope"},
	} {
		if _, err := parseRunnerFlags("runner start", bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	if code := runRunner(nil); code != exitUsage {
		t.Fatalf("runner without a subcommand: %d", code)
	}
	if code := runRunner([]string{"stop"}); code != exitUsage {
		t.Fatalf("unknown subcommand: %d", code)
	}
	if code := runRunnerStart("runner start", []string{"-server", "s"}); code != exitUsage {
		t.Fatalf("missing token file: %d", code)
	}
}

func TestRunnerStatus(t *testing.T) {
	st, err := store.NewFSStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := engine.NewCoordinator(&schemas.Registry{Store: st}, engine.RemoteOptions{Token: "worker-secret-0123456789abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	handlers.NewWorkersHandler(schemas.ModeLive, c).RegisterRoutes(r)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	url := "http://" + ln.Addr().String()
	dir := t.TempDir()
	good, bad := filepath.Join(dir, "good"), filepath.Join(dir, "bad")
	if err := os.WriteFile(good, []byte("worker-secret-0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("wrong"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := runRunnerStatus([]string{"-server", url, "-token-file", good, "-name", "box-01"}, &out); code != exitOK {
		t.Fatalf("status: %d", code)
	}
	for _, want := range []string{"token accepted", "mode        local", "box-01: not registered"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("status output lacks %q:\n%s", want, out.String())
		}
	}
	if code := runRunnerStatus([]string{"-server", url, "-token-file", bad}, &out); code != exitInfra {
		t.Fatalf("status with a wrong token: %d", code)
	}
	if code := runRunnerStatus([]string{"-server", url}, &out); code != exitUsage {
		t.Fatalf("status without a token file: %d", code)
	}
}
