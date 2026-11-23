package sandbox

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/utils"
)

// fakePusher stands in for the docker CLI of a shared runner: it records
// every call and answers inspect, tag and push like docker does.
type fakePusher struct {
	calls   [][]string
	configs []string // config.json content seen at push time
	pushErr string
}

func (f *fakePusher) MustRunEnv(_ context.Context, env []string, args ...string) (*utils.DockerResult, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	switch args[0] {
	case "image":
		if args[1] == "inspect" {
			return &utils.DockerResult{Stdout: []byte("sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n")}, nil
		}
		return &utils.DockerResult{}, nil
	case "tag":
		return &utils.DockerResult{}, nil
	case "push":
		for _, e := range env {
			if dir, ok := strings.CutPrefix(e, "DOCKER_CONFIG="); ok {
				b, _ := os.ReadFile(filepath.Join(dir, "config.json"))
				f.configs = append(f.configs, string(b))
			}
		}
		if f.pushErr != "" {
			return &utils.DockerResult{ExitCode: 1}, errors.New(f.pushErr)
		}
		out := "The push refers to repository [" + args[1] + "]\n" +
			args[1][strings.LastIndex(args[1], ":")+1:] + ": digest: sha256:feedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedface size: 1234\n"
		return &utils.DockerResult{Stdout: []byte(out)}, nil
	}
	return &utils.DockerResult{}, nil
}

func (f *fakePusher) count(sub string) int {
	n := 0
	for _, c := range f.calls {
		if c[0] == sub {
			n++
		}
	}
	return n
}

// Fix wave 3 (N216 fix 2): an image Modal pulls is pushed to the org's own
// registry with the org's login and pulled by digest from there.
func TestResolveImagePushesToOrgRegistry(t *testing.T) {
	fp := &fakePusher{}
	reg := &RegistryImages{Login: ImageRegistry{Registry: "ghcr.io/acme/heimdall", Username: "acme-bot", Token: "ghp_SECRET_TOKEN_1234"}, Docker: fp, HostCheck: publicForTest}
	d := testDriver(t, newFakePlatform())
	d.cfg.RemoteImage = reg.Remote
	got, err := d.resolveImage(context.Background(), testRun(t), "bench/agent-codex-task:1-coding-core", &record{})
	if err != nil {
		t.Fatal(err)
	}
	want := "ghcr.io/acme/heimdall/bench-agent-codex-task@sha256:feedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedface"
	if got != want {
		t.Fatalf("remote image = %q, want %q", got, want)
	}
	if fp.count("push") != 1 || len(fp.configs) != 1 {
		t.Fatalf("pushes = %d configs = %d (%v)", fp.count("push"), len(fp.configs), fp.calls)
	}
	auth := base64.StdEncoding.EncodeToString([]byte("acme-bot:ghp_SECRET_TOKEN_1234"))
	if !strings.Contains(fp.configs[0], `"ghcr.io"`) || !strings.Contains(fp.configs[0], auth) {
		t.Fatalf("push config = %s", fp.configs[0])
	}
	for _, c := range fp.calls {
		if strings.Contains(strings.Join(c, " "), "SECRET") {
			t.Fatalf("the token is on a docker command line: %v", c)
		}
		if c[0] == "tag" && c[2] != "ghcr.io/acme/heimdall/bench-agent-codex-task:0123456789ab" {
			t.Fatalf("tag = %v", c)
		}
	}
	// The same local image is pushed once.
	if again, err := reg.Remote(context.Background(), "bench/agent-codex-task:1-coding-core"); err != nil || again != want || fp.count("push") != 1 {
		t.Fatalf("second resolve = %q %v, pushes %d", again, err, fp.count("push"))
	}
	// A failed push names the registry in plain words and never the token.
	bad := &RegistryImages{Login: reg.Login, HostCheck: publicForTest, Docker: &fakePusher{pushErr: "docker push: exit 1: denied: ghp_SECRET_TOKEN_1234 has no write access"}}
	_, err = bad.Remote(context.Background(), "bench/coding-core-base:1")
	if err == nil || !strings.HasPrefix(err.Error(), "Heimdall could not push to ghcr.io/acme/heimdall: ") || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("failed push = %v", err)
	}
}

// The registry login reaches Modal only for an image of that registry.
func TestModalCreatePassesRegistryAuth(t *testing.T) {
	m, dir := fakeModal(t, "ak-id1234:as-secret5678")
	t.Setenv("TMPDIR", dir)
	m.extraEnv = []string{"LOG_DIR_FAKE=" + dir}
	m.UseRegistry(ImageRegistry{Registry: "ghcr.io/acme/heimdall", Username: "acme-bot", Token: "ghp_SECRET_TOKEN_1234"})
	ctx := context.Background()
	if _, err := m.Create(ctx, Spec{Name: "a", Image: "ghcr.io/acme/heimdall/bench-x@sha256:feed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(ctx, Spec{Name: "b", Image: "docker.io/library/alpine@sha256:feed"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "requests"))
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("requests = %q", raw)
	}
	if !strings.Contains(lines[0], `"registry_auth":{"password":"ghp_SECRET_TOKEN_1234","username":"acme-bot"}`) {
		t.Fatalf("create of an org registry image = %s", lines[0])
	}
	if strings.Contains(lines[1], "registry_auth") {
		t.Fatalf("the login went to another registry's image: %s", lines[1])
	}
}

// fakeV2 is a registry v2 API: basic auth on /v2/ for user:good, or the
// bearer token flow when bearer is set.
func fakeV2(t *testing.T, bearer bool) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		good := ok && u == "acme-bot" && p == "good-token"
		switch {
		case r.URL.Path == "/token" && bearer:
			if !good {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.URL.Query().Get("service") != "fake" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"token":"tok-1"}`))
		case r.URL.Path == "/v2/" && bearer:
			if r.Header.Get("Authorization") == "Bearer tok-1" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="fake"`)
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/v2/":
			if !good {
				w.Header().Set("WWW-Authenticate", `Basic realm="fake"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRegistryLoginSaveSignsIn(t *testing.T) {
	for _, bearer := range []bool{false, true} {
		srv := fakeV2(t, bearer)
		host := strings.TrimPrefix(srv.URL, "https://")
		client := srv.Client()
		client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test server
		keys := keystore.NewMemory()
		c := &Connections{Keys: keys, RegistryClient: client, Build: func(string, Credentials) (Platform, error) { return &checkPlatform{}, nil }}
		ctx := context.Background()
		if _, err := c.SaveRegistry(ctx, "org1", PlatformModal, host+"/team/heimdall", "acme-bot", "good-token", "u1"); !errors.Is(err, ErrNotConnected) {
			t.Fatalf("registry of an unconnected platform = %v", err)
		}
		if _, err := c.Connect(ctx, "org1", PlatformModal, "ak-1234567:as-12345678", "", "u1"); err != nil {
			t.Fatal(err)
		}
		if _, err := c.SaveRegistry(ctx, "org1", PlatformModal, "heimdall", "acme-bot", "good-token", "u1"); !errors.Is(err, ErrRegistryPath) || err.Error() != "Use a registry path like ghcr.io/team/heimdall." {
			t.Fatalf("bad path = %v", err)
		}
		_, err := c.SaveRegistry(ctx, "org1", PlatformModal, host+"/team/heimdall", "acme-bot", "wrong-token", "u1")
		var se *RegistrySignInError
		if !errors.As(err, &se) || err.Error() != "Heimdall could not sign in to "+host+"/team/heimdall: the user name or token was refused." {
			t.Fatalf("refused sign-in (bearer %v) = %v", bearer, err)
		}
		if r, _ := c.RegistryOf(ctx, "org1", PlatformModal); r != nil {
			t.Fatal("a refused login was saved")
		}
		v, err := c.SaveRegistry(ctx, "org1", PlatformModal, host+"/team/heimdall/", "acme-bot", "good-token", "u1")
		if err != nil {
			t.Fatalf("sign-in (bearer %v) = %v", bearer, err)
		}
		if v.Registry != host+"/team/heimdall" || v.Username != "acme-bot" || v.TokenLast4 != "oken" || v.TestedAt == nil {
			t.Fatalf("view = %+v", v)
		}
		r, err := c.RegistryOf(ctx, "org1", PlatformModal)
		if err != nil || r == nil || r.Token != "good-token" {
			t.Fatalf("registry of = %+v %v", r, err)
		}
		l, _ := c.List(ctx, "org1")
		for _, p := range l.Platforms {
			if p.Name == PlatformModal && (p.Registry == nil || p.Registry.Registry != host+"/team/heimdall") {
				t.Fatalf("list view = %+v", p)
			}
		}
		stored, _ := keys.List(ctx, "org1", keystore.KindSecret)
		for _, k := range stored {
			if !IsPlatformSecret(k.Provider) || strings.Contains(k.Label, "good-token") || !strings.Contains(k.Label, " ") {
				t.Fatalf("registry secret %+v could be read as an org secret", k)
			}
		}
		if res, _ := c.Resolve(ctx, "org1"); res != nil {
			t.Fatal("resolved without a default")
		}
		if _, err := c.SetDefault(ctx, "org1", PlatformModal, "u1"); err != nil {
			t.Fatal(err)
		}
		if res, _ := c.Resolve(ctx, "org1"); res == nil || res.Registry == nil || res.Registry.Token != "good-token" {
			t.Fatalf("resolve = %+v", res)
		}
		if err := c.RemoveRegistry(ctx, "org1", PlatformModal, "u1"); err != nil {
			t.Fatal(err)
		}
		if r, _ := c.RegistryOf(ctx, "org1", PlatformModal); r != nil {
			t.Fatal("registry kept after remove")
		}
	}
}
