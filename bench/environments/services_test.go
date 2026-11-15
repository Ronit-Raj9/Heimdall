package environments

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// fakeDocker writes a stand-in docker binary that logs each call's argv on
// one line (newlines inside an argument become spaces) and answers like a daemon with everything present and healthy:
// containers are running, images exist, execs and copies succeed. Calls
// whose argv contains a string in fail exit 1.
func fakeDocker(t *testing.T, fail ...string) (utils.Docker, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	failCases := ""
	for _, f := range fail {
		failCases += fmt.Sprintf("case \"$*\" in *%q*) echo \"failed: $*\" >&2; exit 1;; esac\n", f)
	}
	script := fmt.Sprintf(`#!/bin/sh
line=$(printf '%%s' "$*" | tr '\n' ' ')
printf '%%s\n' "$line" >> %[1]q
%[2]s
case "$1 $2" in
  "image inspect")
    case "$*" in
      *"{{.Id}}"*) echo sha256:1111111111111111111111111111111111111111111111111111111111111111 ;;
      *"{{json .}}"*) echo '{"Id":"sha256:2222","Config":{"User":"10001:10001"}}' ;;
      *Config.Env*) echo '["PATH=/usr/bin","LANG=C"]' ;;
      *) echo '[]' ;;
    esac ;;
  "cp -a") cat > /dev/null ;;
esac
case "$1" in
  inspect)
    case "$*" in
      *Running*) echo "true 0" ;;
      *Config.Env*) printf '["PATH=/opt/bench/shim:/usr/bin","SECRET=x","LANG=C"]\nsha256:img\n' ;;
      *) echo 0 ;;
    esac ;;
  start) [ "$2" = "-a" ] && echo "out $3" ;;
  commit) echo sha256:3333333333333333333333333333333333333333333333333333333333333333 ;;
  ps) [ -n "$FAKE_PS" ] && printf '%%s\n' $FAKE_PS ;;
esac
exit 0
`, logPath, failCases)
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}, logPath
}

// calls returns the logged docker calls.
func calls(t *testing.T, logPath string) []string {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// callWith returns the first call that starts with prefix and contains every
// part, or "".
func callWith(all []string, prefix string, parts ...string) string {
	for _, c := range all {
		if !strings.HasPrefix(c, prefix) {
			continue
		}
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(c, p)
		}
		if ok {
			return c
		}
	}
	return ""
}

func serviceRC(t *testing.T, services ...schemas.ServiceSpec) *schemas.RunContext {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "seed.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return &schemas.RunContext{
		Run:   &schemas.Run{RunID: "r_1", ExpID: "e_1"},
		Suite: &schemas.Suite{ID: "s", BaseImage: schemas.ImageSpec{Tag: "helper:1"}},
		Task:  &schemas.Task{ID: "t", Dir: dir, Services: services},
		Dir:   t.TempDir(),
	}
}

func TestStartServicesWithFakeDocker(t *testing.T) {
	docker, logPath := fakeDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1"})
	rc := serviceRC(t,
		schemas.ServiceSpec{ID: "kv", Role: schemas.ServiceRoleData, Image: "kv:1", Port: 8080, Env: map[string]string{"MODE": "clean", "A": "1"},
			Files: map[string]string{"/srv/seed.json": "seed.json"}, Ready: &schemas.ReadyCheck{HTTP: "/healthz"}},
		schemas.ServiceSpec{ID: "mcp1", Role: schemas.ServiceRoleMCP, Image: "mcp:1", Port: 9000, MCP: &schemas.MCPEndpoint{Path: "/rpc"}},
		schemas.ServiceSpec{ID: "audit", Image: "audit:1", Hidden: true, Ready: &schemas.ReadyCheck{Argv: []string{"true"}}},
	)
	rc.Probe = &schemas.ProbeSpec{Apply: schemas.ProbeApply{ServiceEnv: map[string]map[string]string{"kv": {"MODE": "hostile"}}}}
	handles, results, err := e.StartServices(t.Context(), rc)
	if err != nil {
		t.Fatalf("start services: %v", err)
	}
	if len(handles) != 3 || handles[0].URL != "http://kv:8080" || handles[1].URL != "http://toolproxy:9200/s/mcp1" || handles[2].URL != "" {
		t.Fatalf("handles %+v", handles)
	}
	if !results[1].Proxied || results[0].Proxied || !results[2].Ready || results[0].ImageID == "" {
		t.Fatalf("results %+v", results)
	}
	all := calls(t, logPath)
	for _, want := range []struct {
		prefix string
		parts  []string
	}{
		{"network create --internal", []string{"bifrost-bench.run=r_1", "bench-net-r_1"}},
		{"network create --internal", []string{"bench-hnet-r_1"}},
		{"create --name bench-r_1-svc-kv", []string{"--network bench-net-r_1 --network-alias kv", "-e A=1 -e MODE=hostile kv:1", "--security-opt no-new-privileges"}},
		{"network connect --alias kv bench-hnet-r_1 bench-r_1-svc-kv", nil},
		{"cp -a - bench-r_1-svc-kv:/", nil},
		{"exec bench-r_1-svcprobe", []string{"http http://kv:8080/healthz"}},
		{"exec bench-r_1-svcprobe", []string{"tcp mcp1 9000"}},
		{"create --name bench-r_1-svc-audit", []string{"--network bench-hnet-r_1"}},
		{"exec bench-r_1-svc-audit true", nil},
		{"create --name bench-tp-r_1", []string{"--network bench-net-r_1 --network-alias toolproxy", "--read-only", "-v bench-tpl-r_1:/ledger -v bench-tpc-r_1:/cfg",
			"--entrypoint bifrost-bench bench/bifrost-bench:dev toolproxy -listen :9200 -routes /cfg/routes.json -fault /cfg/fault.json -ledger /ledger/tools.jsonl"}},
		{"cp -a - bench-tp-r_1:/cfg", nil},
		{"exec bench-r_1-svcprobe", []string{"http http://toolproxy:9200/healthz"}},
	} {
		if callWith(all, want.prefix, want.parts...) == "" {
			t.Errorf("no call %q with %q in:\n%s", want.prefix, want.parts, strings.Join(all, "\n"))
		}
	}
	if c := callWith(all, "create --name bench-r_1-svc-audit"); strings.Contains(c, "bench-net-r_1") {
		t.Errorf("hidden service on the agent's network: %s", c)
	}
}

func TestStartServicesErrors(t *testing.T) {
	t.Run("no services touches nothing", func(t *testing.T) {
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: utils.Docker{Binary: filepath.Join(t.TempDir(), "no-docker")}})
		h, r, err := e.StartServices(t.Context(), serviceRC(t))
		if h != nil || r != nil || err != nil {
			t.Fatalf("got %v %v %v", h, r, err)
		}
	})
	t.Run("a service that fails to start is named", func(t *testing.T) {
		docker, _ := fakeDocker(t, "start bench-r_1-svc-db")
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker})
		_, results, err := e.StartServices(t.Context(), serviceRC(t, schemas.ServiceSpec{ID: "db", Image: "db:1"}))
		var se *ServiceError
		if !errors.As(err, &se) || se.Service != "db" || len(results) != 1 || results[0].Ready || results[0].Error == "" {
			t.Fatalf("err %v results %+v", err, results)
		}
	})
	t.Run("a missing tool proxy image is the tool proxy's error", func(t *testing.T) {
		docker, _ := fakeDocker(t, "{{json .}} bench/bifrost-bench:dev")
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker})
		_, _, err := e.StartServices(t.Context(), serviceRC(t, schemas.ServiceSpec{ID: "tool", Role: schemas.ServiceRoleTool, Image: "t:1", Port: 80}))
		var se *ServiceError
		if !errors.As(err, &se) || se.Service != ToolProxyHost || !strings.Contains(err.Error(), "images.toolproxy") {
			t.Fatalf("err %v", err)
		}
	})
}

func TestCheckService(t *testing.T) {
	no := false
	tests := []struct {
		name    string
		spec    schemas.ServiceSpec
		wantErr string
	}{
		{"plain", schemas.ServiceSpec{ID: "db", Image: "postgres:16"}, ""},
		{"build", schemas.ServiceSpec{ID: "db", Build: &schemas.ImageSpec{Dockerfile: "svc/Dockerfile"}}, ""},
		{"reserved id", schemas.ServiceSpec{ID: "toolproxy", Image: "x"}, "must match"},
		{"bad id", schemas.ServiceSpec{ID: "a b", Image: "x"}, "must match"},
		{"no image", schemas.ServiceSpec{ID: "a"}, "neither image nor build"},
		{"both ready checks", schemas.ServiceSpec{ID: "a", Image: "x", Port: 1, Ready: &schemas.ReadyCheck{Argv: []string{"true"}, HTTP: "/"}}, "both"},
		{"http without port", schemas.ServiceSpec{ID: "a", Image: "x", Ready: &schemas.ReadyCheck{HTTP: "/h"}}, "needs a port"},
		{"proxied without port", schemas.ServiceSpec{ID: "a", Image: "x", Role: schemas.ServiceRoleTool}, "no port"},
		{"proxy off without port", schemas.ServiceSpec{ID: "a", Image: "x", Role: schemas.ServiceRoleTool, Proxy: &no}, ""},
		{"relative file", schemas.ServiceSpec{ID: "a", Image: "x", Files: map[string]string{"srv/x": "x"}}, "absolute clean"},
		{"export both", schemas.ServiceSpec{ID: "a", Image: "x", Export: []schemas.StateExport{{ID: "e", Argv: []string{"cat"}, Path: "/x"}}}, "exactly one"},
		{"export repeated", schemas.ServiceSpec{ID: "a", Image: "x", Export: []schemas.StateExport{{ID: "e", Path: "/x"}, {ID: "e", Path: "/y"}}}, "repeated"},
		{"export relative path", schemas.ServiceSpec{ID: "a", Image: "x", Export: []schemas.StateExport{{ID: "e", Path: "x"}}}, "absolute"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkService(tt.spec)
			if tt.wantErr == "" && err != nil || tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestRunServicesOrderAndDuplicates(t *testing.T) {
	rc := &schemas.RunContext{
		Suite: &schemas.Suite{Root: "/suite", Services: []schemas.ServiceSpec{{ID: "a", Image: "x"}}},
		Task: &schemas.Task{Dir: "/task", Environment: &schemas.TaskEnvironment{BuildNetwork: true},
			Services: []schemas.ServiceSpec{{ID: "b", Image: "x"}, {ID: "c", Image: "x"}}},
	}
	svcs, err := runServices(rc)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range svcs {
		got = append(got, fmt.Sprintf("%s:%s:%t", s.spec.ID, s.base, s.network))
	}
	if want := []string{"a:/suite:false", "b:/task:true", "c:/task:true"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	rc.Task.Services = append(rc.Task.Services, schemas.ServiceSpec{ID: "a", Image: "y"})
	if _, err := runServices(rc); err == nil || !strings.Contains(err.Error(), "used twice") {
		t.Fatalf("duplicate id: %v", err)
	}
}

func TestProxyRoutes(t *testing.T) {
	yes := true
	svcs := []runService{
		{spec: schemas.ServiceSpec{ID: "db", Role: schemas.ServiceRoleData, Port: 5432}},
		{spec: schemas.ServiceSpec{ID: "tool", Role: schemas.ServiceRoleTool, Port: 80}},
		{spec: schemas.ServiceSpec{ID: "m", Role: schemas.ServiceRoleMCP, Port: 9000}},
		{spec: schemas.ServiceSpec{ID: "m2", Role: schemas.ServiceRoleMCP, Port: 9001, MCP: &schemas.MCPEndpoint{Path: "/rpc"}}},
		{spec: schemas.ServiceSpec{ID: "hid", Role: schemas.ServiceRoleTool, Port: 1, Hidden: true}},
		{spec: schemas.ServiceSpec{ID: "dbp", Role: schemas.ServiceRoleData, Port: 2, Proxy: &yes}},
	}
	got, err := proxyRoutes(svcs)
	if err != nil {
		t.Fatal(err)
	}
	want := []toolRoute{
		{Service: "tool", Upstream: "http://tool:80", Protocol: "http"},
		{Service: "m", Upstream: "http://m:9000", Protocol: "mcp", MCPPath: "/mcp"},
		{Service: "m2", Upstream: "http://m2:9001", Protocol: "mcp", MCPPath: "/rpc"},
		{Service: "dbp", Upstream: "http://dbp:2", Protocol: "http"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if _, err := proxyRoutes([]runService{{spec: schemas.ServiceSpec{ID: "s", Role: schemas.ServiceRoleMCP, Port: 1, MCP: &schemas.MCPEndpoint{Transport: "stdio"}}}}); err == nil {
		t.Fatal("stdio transport accepted")
	}
}

func TestServiceEnv(t *testing.T) {
	spec := schemas.ServiceSpec{ID: "kv", Env: map[string]string{"A": "1", "MODE": "clean"}}
	rc := &schemas.RunContext{}
	if got := serviceEnv(rc, spec); !reflect.DeepEqual(got, map[string]string{"A": "1", "MODE": "clean"}) {
		t.Fatalf("no probe: %v", got)
	}
	rc.Probe = &schemas.ProbeSpec{Apply: schemas.ProbeApply{ServiceEnv: map[string]map[string]string{"kv": {"MODE": "hostile", "B": "2"}, "other": {"A": "x"}}}}
	if got := serviceEnv(rc, spec); !reflect.DeepEqual(got, map[string]string{"A": "1", "B": "2", "MODE": "hostile"}) {
		t.Fatalf("probe: %v", got)
	}
	if spec.Env["MODE"] != "clean" {
		t.Fatal("spec env mutated")
	}
}

func TestNumericOwner(t *testing.T) {
	tests := map[string][3]string{"1000": {"1000", "1000", "y"}, "10001:10002": {"10001", "10002", "y"}, "1000:staff": {"1000", "1000", "y"}, "postgres": {"", "", ""}, "": {"", "", ""}}
	for user, want := range tests {
		u, g, ok := numericOwner(user)
		if u != want[0] || g != want[1] || ok != (want[2] == "y") {
			t.Errorf("%q: %q %q %v", user, u, g, ok)
		}
	}
	if uid, gid := numericUser("postgres"); uid != 0 || gid != 0 {
		t.Errorf("named user: %d %d", uid, gid)
	}
}

func TestWriteFilesTar(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "seed.json"), "{}")
	writeFile(t, filepath.Join(base, "data", "a.txt"), "a")
	writeFile(t, filepath.Join(base, "data", "sub", "b.txt"), "b")
	if err := os.Symlink("/etc/passwd", filepath.Join(base, "data", "leak")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := writeFilesTar(&buf, base, map[string]string{"/srv/seed.json": "seed.json", "/var/lib/data": "data"}, 10001, 10002)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(&buf)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Uid != 10001 || h.Gid != 10002 {
			t.Errorf("%s owned by %d:%d", h.Name, h.Uid, h.Gid)
		}
		names = append(names, h.Name)
	}
	want := []string{"srv/seed.json", "var/lib/data/", "var/lib/data/a.txt", "var/lib/data/sub/", "var/lib/data/sub/b.txt"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("entries %v", names)
	}
	if err := writeFilesTar(io.Discard, base, map[string]string{"/x": "../outside"}, 0, 0); err == nil {
		t.Fatal("file outside the base accepted")
	}
}

func TestUnpackExport(t *testing.T) {
	dir := t.TempDir()
	mk := func(entries ...[2]string) string {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, e := range entries {
			if strings.HasSuffix(e[0], "/") {
				_ = tw.WriteHeader(&tar.Header{Name: e[0], Typeflag: tar.TypeDir, Mode: 0o755})
				continue
			}
			_ = tw.WriteHeader(&tar.Header{Name: e[0], Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(e[1]))})
			_, _ = tw.Write([]byte(e[1]))
		}
		_ = tw.Close()
		p := filepath.Join(dir, fmt.Sprintf("spool-%d.tar", len(entries)))
		writeFile(t, p, buf.String())
		return p
	}
	out := filepath.Join(dir, "file")
	n, problem, err := unpackExport(mk([2]string{"state.json", `{"a":1}`}), out, 1024)
	if err != nil || problem != "" || n != 7 || string(readOut(t, out)) != `{"a":1}` {
		t.Fatalf("file: %d %q %v", n, problem, err)
	}
	folder := filepath.Join(dir, "folder")
	spool := mk([2]string{"srv/", ""}, [2]string{"srv/a", "x"})
	if n, problem, err := unpackExport(spool, folder, 1<<20); err != nil || problem != "" || n == 0 {
		t.Fatalf("folder: %d %q %v", n, problem, err)
	}
	if tr := tar.NewReader(bytes.NewReader(readOut(t, folder))); func() bool { h, err := tr.Next(); return err != nil || h.Name != "srv/" }() {
		t.Fatal("folder export is not the tar")
	}
	if _, problem, _ := unpackExport(mk([2]string{"big", strings.Repeat("x", 100)}), filepath.Join(dir, "big"), 10); !strings.Contains(problem, "over its 10 byte cap") {
		t.Fatalf("cap: %q", problem)
	}
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readOut(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestExportStateWithFakeDocker(t *testing.T) {
	docker, logPath := fakeDocker(t, "exec bench-r_1-svc-kv false")
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker})
	rc := serviceRC(t, schemas.ServiceSpec{ID: "kv", Image: "kv:1", Export: []schemas.StateExport{
		{ID: "ok", Argv: []string{"echo", "hi"}}, {ID: "bad", Argv: []string{"false"}}}})
	files, err := e.ExportState(t.Context(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Error != "" || files[0].Path != "state/kv/ok" || files[1].Error == "" {
		t.Fatalf("files %+v", files)
	}
	if _, err := os.Stat(filepath.Join(rc.Dir, "state", "kv", "bad")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed export left a file: %v", err)
	}
	if callWith(calls(t, logPath), "cp bench-tp-r_1") != "" {
		t.Fatal("ledger copied for a run without proxied services")
	}
}
