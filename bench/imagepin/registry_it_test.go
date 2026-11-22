package imagepin_test

// End to end on a throwaway local registry (registry:2 with token auth):
// the Heimdall token endpoint, a sync push by the sync identity, a pull by
// digest on a machine that lacks the image (prefetch, as an org with its
// API token), and the refusals: an org cannot push, and cannot pull a
// restricted suite. Needs docker and the registry:2 image; runs only with
// BENCH_REGISTRY_IT=1. It never touches any other registry.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/imagepin"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
	"golang.org/x/crypto/bcrypt"
)

func dockerOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestLocalRegistryEndToEnd(t *testing.T) {
	if os.Getenv("BENCH_REGISTRY_IT") != "1" {
		t.Skip("set BENCH_REGISTRY_IT=1 to run against a throwaway local registry:2")
	}
	ctx := context.Background()
	rnd := make([]byte, 4)
	rand.Read(rnd)
	id := hex.EncodeToString(rnd)
	dir := t.TempDir()
	os.Chmod(dir, 0o755)

	// Token endpoint on the host.
	keyDir := filepath.Join(dir, "token")
	if err := imagepin.GenerateTokenKey(keyDir, ""); err != nil {
		t.Fatal(err)
	}
	iss, err := imagepin.LoadIssuer(keyDir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	const syncPW = "sync-password-0123456789"
	hash, _ := bcrypt.GenerateFromPassword([]byte(syncPW), bcrypt.MinCost)
	policy := &imagepin.AccessPolicy{Namespace: "library", Restricted: map[string][]string{"secret": {"org-allowed"}}}
	resolve := func(_ context.Context, s string) (string, error) {
		if s == "hm_org" {
			return "org-1", nil
		}
		return "", fmt.Errorf("refused")
	}
	h := handlers.NewRegistryTokenHandler(schemas.ModeLive, iss, policy, resolve, "sync:"+string(hash), "")
	r := router.New()
	h.RegisterRoutes(r)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go fasthttp.Serve(ln, r.Handler)
	defer ln.Close()
	realm := "http://" + ln.Addr().String() + handlers.RegistryTokenPath

	// The registry, trusting the token certificate.
	cfg := strings.Join([]string{
		"version: 0.1",
		"storage: {filesystem: {rootdirectory: /var/lib/registry}, delete: {enabled: false}}",
		"auth: {token: {realm: \"" + realm + "\", service: " + imagepin.DefaultService + ", issuer: " + imagepin.DefaultIssuer + ", rootcertbundle: /auth/token.crt}}",
		"http: {addr: \":5000\"}",
	}, "\n") + "\n"
	cfgPath := filepath.Join(dir, "config.yml")
	os.WriteFile(cfgPath, []byte(cfg), 0o644)
	name := "z13-it-registry-" + id
	dockerOut(t, "run", "-d", "--name", name, "-p", "127.0.0.1::5000",
		"-v", cfgPath+":/etc/docker/registry/config.yml:ro", "-v", filepath.Join(keyDir, imagepin.TokenCertFile)+":/auth/token.crt:ro", "registry:2")
	defer exec.Command("docker", "rm", "-f", name).Run()
	port := dockerOut(t, "port", name, "5000/tcp")
	registry := "127.0.0.1:" + port[strings.LastIndex(port, ":")+1:]
	deadline := time.Now().Add(20 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", registry, time.Second)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("registry did not start")
		}
		time.Sleep(200 * time.Millisecond)
	}

	// A tiny image built here, standing in for a library image.
	bctx := filepath.Join(dir, "ctx")
	os.MkdirAll(bctx, 0o755)
	os.WriteFile(filepath.Join(bctx, "hello"), []byte("z13 "+id+"\n"), 0o644)
	os.WriteFile(filepath.Join(bctx, "Dockerfile"), []byte("FROM scratch\nCOPY hello /hello\n"), 0o644)
	tag := "z13-it/tiny-" + id + ":1"
	dockerOut(t, "build", "-q", "-t", tag, bctx)
	defer exec.Command("docker", "image", "rm", "-f", tag).Run()

	d := utils.Docker{}
	sync := imagepin.Credentials{User: "sync", Password: syncPW}
	res, err := imagepin.Sync(ctx, imagepin.SyncOptions{Registry: registry, Suite: "open", Tags: []string{tag, "z13-it/absent:1"},
		Push: sync, PlainHTTP: true, Docker: d, Log: testWriter{t}})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Error != "" || res[0].Pinned == "" || !res[1].Missing {
		t.Fatalf("sync %+v", res)
	}
	pinned := res[0].Pinned
	defer exec.Command("docker", "image", "rm", "-f", pinned).Run()
	if !strings.HasPrefix(pinned, registry+"/library/open/z13-it/tiny-"+id+"@sha256:") {
		t.Fatalf("pinned %s", pinned)
	}
	if _, same, err := d.HasPinned(ctx, tag, pinned); err != nil || !same {
		t.Fatalf("local image is not the pushed digest: %v", err)
	}

	// A fresh machine: the image is gone; prefetch pulls it by digest as an org.
	dockerOut(t, "image", "rm", tag)
	if present, _, _ := d.HasPinned(ctx, tag, pinned); present {
		t.Fatal("image still present")
	}
	orgEnv, done, err := imagepin.DockerConfig(registry, imagepin.Credentials{User: "heimdall", Password: "hm_org"})
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	pre := imagepin.Prefetch(ctx, d, orgEnv, []imagepin.Want{{Tag: tag, Pinned: pinned}}, 1, testWriter{t})
	if pre[0].State != imagepin.PrefetchPulled {
		t.Fatalf("prefetch %+v", pre[0])
	}
	t.Logf("cold pull by digest: %s", pre[0].Elapsed)
	if again := imagepin.Prefetch(ctx, d, orgEnv, []imagepin.Want{{Tag: tag, Pinned: pinned}}, 1, nil); again[0].State != imagepin.PrefetchWarm {
		t.Fatalf("second prefetch %+v", again[0])
	}

	// The layer report reads the registry with the org's token.
	rep := imagepin.Layers(ctx, &imagepin.Client{Host: registry, PlainHTTP: true, User: "heimdall", Password: "hm_org"}, []imagepin.Want{{Tag: tag, Pinned: pinned}})
	if rep.Images[0].Error != "" || len(rep.Images[0].Layers) != 1 {
		t.Fatalf("layers %+v", rep.Images[0])
	}

	// An org cannot push.
	evil := registry + "/library/open/evil-" + id + ":1"
	dockerOut(t, "tag", tag, evil)
	defer exec.Command("docker", "image", "rm", "-f", evil).Run()
	cmd := exec.Command("docker", "push", "-q", evil)
	cmd.Env = append(os.Environ(), orgEnv...)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("an org pushed: %s", out)
	}

	// A restricted suite: the sync identity pushes it, an org outside its
	// access list cannot pull it (another image, so nothing is local).
	os.WriteFile(filepath.Join(bctx, "hello"), []byte("z13 secret "+id+"\n"), 0o644)
	secret := "z13-it/secret-" + id + ":1"
	dockerOut(t, "build", "-q", "-t", secret, bctx)
	defer exec.Command("docker", "image", "rm", "-f", secret).Run()
	res, err = imagepin.Sync(ctx, imagepin.SyncOptions{Registry: registry, Suite: "secret", Tags: []string{secret}, Push: sync, PlainHTTP: true, Docker: d})
	if err != nil || res[0].Pinned == "" {
		t.Fatalf("restricted sync %+v %v", res, err)
	}
	secretPin := res[0].Pinned
	defer exec.Command("docker", "image", "rm", "-f", secretPin).Run()
	dockerOut(t, "image", "rm", secret)
	pull := imagepin.Prefetch(ctx, d, orgEnv, []imagepin.Want{{Tag: secret, Pinned: secretPin}}, 1, nil)
	if pull[0].State != imagepin.PrefetchFailed {
		t.Fatalf("an org outside the access list pulled a restricted suite: %+v", pull[0])
	}
	t.Logf("restricted pull refused: %s", pull[0].Error)
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
