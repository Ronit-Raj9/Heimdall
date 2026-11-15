package environments

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// externalRC is a run of a task with one external API and no services. The
// cassette file exists when cassette is true.
func externalRC(t *testing.T, mode, experiment string, cassette bool) *schemas.RunContext {
	t.Helper()
	rc := serviceRC(t)
	api := schemas.ExternalAPI{
		ID: "weather", BaseURL: "https://api.weather.test/v1/", Mode: mode, Cassette: "weather.cassette.jsonl",
		Allow:   []schemas.ExternalAllow{{Methods: []string{"GET"}, PathPrefix: "/forecast"}},
		Headers: map[string]string{"Accept": "application/json"}, AuthEnv: "HD_EXT_WEATHER_KEY", AuthScheme: "Bearer",
	}
	rc.Task.External = []schemas.ExternalAPI{api}
	if cassette {
		line := `{"method":"GET","path":"/forecast","query":"city=oslo","body_sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","status":200,"headers":{"Content-Type":"application/json"},"body":"{\"temp_c\":4}"}` + "\n"
		if err := os.WriteFile(filepath.Join(rc.Task.Dir, "weather.cassette.jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rc.Manifest = &schemas.Manifest{Experiment: schemas.Experiment{External: experiment}}
	return rc
}

func stubEnv(t *testing.T, vars map[string]string) {
	t.Helper()
	old := lookupEnv
	lookupEnv = func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	}
	t.Cleanup(func() { lookupEnv = old })
}

func TestStartServicesExternalReplay(t *testing.T) {
	stubEnv(t, nil)
	docker, logPath := fakeDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", ToolProxyImage: "tp:1"})
	rc := externalRC(t, "", "", true)
	handles, results, err := e.StartServices(t.Context(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(handles) != 1 || handles[0].Spec.Role != schemas.ServiceRoleExternal || handles[0].URL != "http://toolproxy:9200/x/weather" ||
		handles[0].Host != ToolProxyHost || handles[0].Upstream != "https://api.weather.test/v1" || handles[0].Container != "bench-tp-r_1" {
		t.Fatalf("handles %+v", handles)
	}
	if len(results) != 1 || !results[0].Ready || !results[0].Proxied || results[0].Image != "tp:1" || results[0].Role != schemas.ServiceRoleExternal {
		t.Fatalf("results %+v", results)
	}
	all := calls(t, logPath)
	if callWith(all, "network create", "--internal", "bench-net-r_1") == "" {
		t.Fatalf("no service network:\n%s", strings.Join(all, "\n"))
	}
	create := callWith(all, "create --name bench-tp-r_1")
	if create == "" || strings.Contains(create, "HD_EXT_WEATHER_KEY") {
		t.Fatalf("replay proxy got a credential or did not start: %q", create)
	}
	for _, c := range all {
		if strings.Contains(c, "bench-xnet-") || strings.Contains(c, "bench-eg-") {
			t.Fatalf("replay run opened egress: %s", c)
		}
	}
	if callWith(all, "cp -a - bench-tp-r_1:/cfg") == "" {
		t.Fatalf("cassette not copied:\n%s", strings.Join(all, "\n"))
	}
	js, err := utils.ExternalAPIsJSON(handles)
	if err != nil || js != `{"weather":{"url":"http://toolproxy:9200/x/weather"}}` {
		t.Fatalf("env json %s %v", js, err)
	}
}

func TestStartServicesExternalLive(t *testing.T) {
	stubEnv(t, map[string]string{"HD_EXT_WEATHER_KEY": "sk-test-value"})
	docker, logPath := fakeDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", ToolProxyImage: "tp:1"})
	rc := externalRC(t, "", schemas.ExternalLive, false)
	if _, _, err := e.StartServices(t.Context(), rc); err != nil {
		t.Fatal(err)
	}
	all := calls(t, logPath)
	for _, c := range all {
		if strings.Contains(c, "sk-test-value") {
			t.Fatalf("credential value on a docker command line: %s", c)
		}
	}
	checks := []struct{ prefix, part string }{
		{"network create", "bench-xnet-r_1"},
		{"create --name bench-eg-r_1-weather", "BENCH_EGRESS_TARGET=api.weather.test:443"},
		{"network connect", "bench-xnet-r_1 bench-eg-r_1-weather"},
		{"create --name bench-tp-r_1", "-e HD_EXT_WEATHER_KEY --entrypoint bifrost-bench tp:1"},
		{"network connect", "bench-xnet-r_1 bench-tp-r_1"},
	}
	for _, c := range checks {
		if callWith(all, c.prefix, c.part) == "" {
			t.Fatalf("missing %q %q in:\n%s", c.prefix, c.part, strings.Join(all, "\n"))
		}
	}
	if !strings.Contains(callWith(all, "network create", "bench-xnet-r_1"), "--internal") {
		t.Fatal("external network is not internal")
	}
	if strings.Contains(callWith(all, "network create", "bench-eg-r_1-weather"), "--internal") {
		t.Fatal("forwarder outbound network is internal")
	}
	if _, err := os.Stat(filepath.Join(rc.Dir, "external", "egress.json")); err != nil {
		t.Fatal(err)
	}
}

func TestStartServicesExternalRefused(t *testing.T) {
	cases := []struct {
		name       string
		mode, exp  string
		cassette   bool
		env        map[string]string
		service    string
		wantSubstr string
	}{
		{name: "no cassette no opt-in", wantSubstr: "has no cassette"},
		{name: "replay without cassette", mode: schemas.ExternalReplay, wantSubstr: "cassette"},
		{name: "live without opt-in", mode: schemas.ExternalLive, cassette: true, wantSubstr: "must opt in"},
		{name: "live without credential", exp: schemas.ExternalLive, wantSubstr: "auth_env HD_EXT_WEATHER_KEY is not set"},
		{name: "id clash", cassette: true, service: "weather", wantSubstr: "also a service id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stubEnv(t, c.env)
			docker, logPath := fakeDocker(t)
			e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1"})
			rc := externalRC(t, c.mode, c.exp, c.cassette)
			if c.service != "" {
				rc.Task.Services = []schemas.ServiceSpec{{ID: c.service, Image: "x:1"}}
			}
			_, _, err := e.StartServices(t.Context(), rc)
			var se *ServiceError
			if !errors.As(err, &se) || se.Service != "external" || !strings.Contains(err.Error(), c.wantSubstr) {
				t.Fatalf("err %v, want ServiceError external with %q", err, c.wantSubstr)
			}
			if got := calls(t, logPath); len(got) != 0 {
				t.Fatalf("docker called before refusing: %v", got)
			}
		})
	}
}

func TestExternalRoutes(t *testing.T) {
	ext := []externalAPI{
		{ref: utils.ExternalRef{API: schemas.ExternalAPI{ID: "fx", BaseURL: "http://fx.test:8080/", AuthEnv: "K"}}, mode: schemas.ExternalReplay},
		{ref: utils.ExternalRef{API: schemas.ExternalAPI{ID: "wx", BaseURL: "https://wx.test", AuthEnv: "K", AuthHeader: "x-api-key", MaxCalls: 5}}, mode: schemas.ExternalRecord},
	}
	raw, err := json.Marshal(externalRoutes("r_1", ext))
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"service":"fx","upstream":"http://fx.test:8080","protocol":"external","external":{"mode":"replay","cassette":"/cfg/external/fx.cassette.jsonl","max_calls":200,"max_bytes":4194304,"timeout_s":30}},` +
		`{"service":"wx","upstream":"https://wx.test","protocol":"external","external":{"mode":"record","auth_env":"K","auth_header":"X-Api-Key","record_to":"/ledger/external/wx.cassette.jsonl","connect":"bench-eg-r_1-wx:9300","max_calls":5,"max_bytes":4194304,"timeout_s":30}}]`
	if string(raw) != want {
		t.Fatalf("routes\n got %s\nwant %s", raw, want)
	}
	if got := externalProxyEnv(ext); strings.Join(got, " ") != "-e K" {
		t.Fatalf("proxy env %v", got)
	}
	// A route of a proxied service marshals as it did before external APIs.
	old, err := json.Marshal(toolRoute{Service: "kv", Upstream: "http://kv:1", Protocol: "http"})
	if err != nil || string(old) != `{"service":"kv","upstream":"http://kv:1","protocol":"http"}` {
		t.Fatalf("service route %s %v", old, err)
	}
}

func TestExternalTar(t *testing.T) {
	tr := tar.NewReader(bytes.NewReader(externalTar(map[string][]byte{"b.jsonl": []byte("2"), "a.jsonl": []byte("1")}, 7, 8)))
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Uid != 7 || h.Gid != 8 {
			t.Fatalf("owner %d:%d", h.Uid, h.Gid)
		}
		names = append(names, h.Name)
	}
	if strings.Join(names, ",") != "external/,external/a.jsonl,external/b.jsonl" {
		t.Fatal(names)
	}
}

func TestExportStateExternalRecord(t *testing.T) {
	stubEnv(t, nil)
	docker, logPath := fakeDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1"})
	rc := externalRC(t, "", schemas.ExternalRecord, false)
	if _, err := e.ExportState(t.Context(), rc); err != nil {
		t.Fatal(err)
	}
	all := calls(t, logPath)
	if callWith(all, "cp bench-tp-r_1:/ledger/tools.jsonl") == "" {
		t.Fatalf("ledger not copied for an external-only run:\n%s", strings.Join(all, "\n"))
	}
	if callWith(all, "cp bench-tp-r_1:/ledger/external/weather.cassette.jsonl") == "" {
		t.Fatalf("cassette not exported:\n%s", strings.Join(all, "\n"))
	}
	// The fake docker returns no tar: nothing is left behind as the cassette.
	if _, err := os.Stat(filepath.Join(rc.Dir, "external", "weather.cassette.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("half-written cassette left: %v", err)
	}
}
