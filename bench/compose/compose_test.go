package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// writeTask lays files (relative path -> content) into a fresh task folder.
func writeTask(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const stackCompose = `
services:
  app:
    build: ./app
    working_dir: /app
    environment:
      DATABASE_URL: postgres://app:app@db:5432/app
      CACHE_URL: redis://cache:6379/0
    depends_on:
      db: {condition: service_healthy}
      cache: {condition: service_started}
      seed: {condition: service_completed_successfully}
    x-heimdall: {workspace: true}
  db:
    image: postgres:18
    environment:
      - POSTGRES_PASSWORD=${DB_PASSWORD:-app}
      - POSTGRES_USER=app
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U app"]
      interval: 2s
      timeout: 3s
      retries: 20
    volumes:
      - pgdata:/var/lib/postgresql
      - ./initdb:/docker-entrypoint-initdb.d:ro
    ports: ["5432:5432"]
  cache:
    image: redis:7-alpine
    command: redis-server --save "" --appendonly no
    expose: ["6379"]
    mem_limit: 256m
  seed:
    image: postgres:18
    entrypoint: ["psql"]
    command: ["-h", "db", "-U", "app", "-f", "/seed/seed.sql"]
    depends_on:
      db: {condition: service_healthy}
    volumes:
      - ./seed:/seed:ro
volumes:
  pgdata: {}
`

func stackFiles() map[string]string {
	return map[string]string{
		"compose.yaml":           stackCompose,
		"app/Dockerfile":         "FROM python:3.12-slim\nWORKDIR /app\nCOPY . .\n",
		"app/server.py":          "print('hi')\n",
		"initdb/01-schema.sql":   "create table t (id int);\n",
		"initdb/sub/02-more.sql": "select 1;\n",
		"seed/seed.sql":          "insert into t values (1);\n",
	}
}

func TestExpandStack(t *testing.T) {
	dir := writeTask(t, stackFiles())
	res, err := Expand(dir, "compose.yaml", nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Refused(); len(r) > 0 {
		t.Fatalf("refused: %v", r)
	}
	if res.Workspace != "app" {
		t.Fatalf("workspace %q", res.Workspace)
	}
	env := res.Environment
	want := &schemas.TaskEnvironment{Dockerfile: "app/Dockerfile", Context: "app", WorkspacePath: "/app",
		Env: map[string]string{"DATABASE_URL": "postgres://app:app@db:5432/app", "CACHE_URL": "redis://cache:6379/0"}}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("environment\n got %+v\nwant %+v", env, want)
	}
	var ids []string
	for _, s := range res.Services {
		ids = append(ids, s.ID)
	}
	// db before seed (seed waits for it); cache keeps its file place.
	if got := strings.Join(ids, ","); got != "db,cache,seed" {
		t.Fatalf("order %s", got)
	}
	db, cache, seed := res.Services[0], res.Services[1], res.Services[2]
	if db.Ready == nil || !reflect.DeepEqual(db.Ready.Argv, []string{"/bin/sh", "-c", "pg_isready -U app"}) || db.Ready.TimeoutS != 101 {
		t.Fatalf("db ready %+v", db.Ready)
	}
	if db.Env["POSTGRES_PASSWORD"] != "app" || db.Port != 5432 {
		t.Fatalf("db env %v port %d", db.Env, db.Port)
	}
	if !reflect.DeepEqual(db.Compose.Volumes, []schemas.ComposeVolume{{Name: "pgdata", Target: "/var/lib/postgresql"}}) {
		t.Fatalf("db volumes %+v", db.Compose.Volumes)
	}
	wantFiles := map[string]string{"/docker-entrypoint-initdb.d/01-schema.sql": "initdb/01-schema.sql", "/docker-entrypoint-initdb.d/sub/02-more.sql": "initdb/sub/02-more.sql"}
	if !reflect.DeepEqual(db.Files, wantFiles) {
		t.Fatalf("db files %v", db.Files)
	}
	if !reflect.DeepEqual(cache.Argv, []string{"redis-server", "--save", "", "--appendonly", "no"}) || cache.Port != 6379 || cache.Compose.MemoryBytes != 256<<20 {
		t.Fatalf("cache %+v %+v", cache, cache.Compose)
	}
	if !seed.Compose.RunOnce || seed.Ready != nil || !seed.Compose.EntrypointSet || seed.Compose.Entrypoint[0] != "psql" {
		t.Fatalf("seed %+v %+v", seed, seed.Compose)
	}
	if !hasFinding(res.Findings, LevelChanged, "services.db.ports[0]", "host port 5432 is not published") {
		t.Fatalf("published port not reported: %v", res.Findings)
	}
}

func hasFinding(fs []Finding, level, where, msg string) bool {
	for _, f := range fs {
		if f.Level == level && f.Where == where && strings.Contains(f.Message, msg) {
			return true
		}
	}
	return false
}

// TestRefusals is the refusal table: each compose file holds one thing the
// bench cannot run honestly and must refuse at that place with that reason.
func TestRefusals(t *testing.T) {
	base := "services:\n  app:\n    image: python:3.12-slim\n    working_dir: /app\n    x-heimdall: {workspace: true}\n"
	cases := []struct {
		name, extra, where, msg string
	}{
		{"privileged", "  db:\n    image: postgres:18\n    privileged: true\n", "services.db.privileged", "privileged mode"},
		{"cap_add", "  db:\n    image: postgres:18\n    cap_add: [NET_ADMIN]\n", "services.db.cap_add", "capabilities"},
		{"devices", "  db:\n    image: postgres:18\n    devices: [/dev/fuse]\n", "services.db.devices", "host devices"},
		{"host network", "  db:\n    image: postgres:18\n    network_mode: host\n", "services.db.network_mode", "not allowed"},
		{"pid host", "  db:\n    image: postgres:18\n    pid: host\n", "services.db.pid", "process namespace"},
		{"ipc host", "  db:\n    image: postgres:18\n    ipc: host\n", "services.db.ipc", "IPC"},
		{"bind outside", "  db:\n    image: postgres:18\n    volumes: [\"/etc:/host-etc:ro\"]\n", "services.db.volumes[0]", "outside the task folder"},
		{"bind escapes", "  db:\n    image: postgres:18\n    volumes: [\"../../x:/x\"]\n", "services.db.volumes[0]", "leaves the task folder"},
		{"docker socket", "  db:\n    image: postgres:18\n    volumes: [\"/var/run/docker.sock:/var/run/docker.sock\"]\n", "services.db.volumes[0]", "docker socket"},
		{"docker socket long", "  db:\n    image: postgres:18\n    volumes:\n      - {type: volume, source: x, target: /var/run/docker.sock}\n", "services.db.volumes[0]", "docker socket"},
		{"extends", "  db:\n    extends: {service: base}\n    image: postgres:18\n", "services.db.extends", "extends"},
		{"unknown key", "  db:\n    image: postgres:18\n    flavour: vanilla\n", "services.db.flavour", "not a supported service key"},
		{"host variable", "  db:\n    image: postgres:18\n    environment:\n      PASSWORD: ${DB_PASSWORD}\n", "services.db.environment.PASSWORD", "DB_PASSWORD is not set"},
		{"host env passthrough", "  db:\n    image: postgres:18\n    environment: [API_KEY]\n", "services.db.environment[0]", "host environment"},
		{"security_opt", "  db:\n    image: postgres:18\n    security_opt: [seccomp:unconfined]\n", "services.db.security_opt", "seccomp"},
		{"external volume", "  db:\n    image: postgres:18\nvolumes:\n  data: {external: true}\n", "volumes.data.external", "external volume"},
		{"undeclared volume", "  db:\n    image: postgres:18\n    volumes: [\"data:/data\"]\n", "services.db.volumes[0]", "not declared"},
		{"depends on unknown", "  db:\n    image: postgres:18\n    depends_on: [nope]\n", "services.db.depends_on[0]", "does not declare"},
		{"healthy without check", "  db:\n    image: postgres:18\n  web:\n    image: nginx\n    depends_on: {db: {condition: service_healthy}}\n", "services.web.depends_on[0]", "no healthcheck"},
		{"cycle", "  a:\n    image: x\n    depends_on: [b]\n  b:\n    image: x\n    depends_on: [a]\n", "services.a.depends_on", "dependency cycle"},
		{"depends on workspace", "  db:\n    image: postgres:18\n    depends_on: [app]\n", "services.db.depends_on[0]", "workspace service"},
		{"build args", "  db:\n    build: {context: ., args: {A: b}}\n", "services.db.build.args", "build arguments"},
		{"remote context", "  db:\n    build: https://github.com/x/y.git\n", "services.db.build.context", "remote build context"},
		{"secrets top", "  db:\n    image: x\nsecrets:\n  s: {file: ./s}\n", "secrets", "not supported"},
		{"replicas", "  db:\n    image: x\n    deploy: {replicas: 3}\n", "services.db.deploy.replicas", "replica"},
		{"reserved name", "  toolproxy:\n    image: x\n", "services.toolproxy", "reserved"},
		{"bad name", "  Bad!Name:\n    image: x\n", "services.Bad!Name", "host name"},
		{"no image", "  db:\n    environment: {A: b}\n", "services.db", "needs an image or a build"},
		{"udp port", "  db:\n    image: x\n    expose: [\"53/udp\"]\n", "services.db.expose[0]", "only tcp"},
		{"empty command", "  db:\n    image: x\n    command: []\n", "services.db.command", "empty command"},
		{"empty command string", "  db:\n    image: x\n    command: \"\"\n", "services.db.command", "empty command"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"compose.yaml": base + c.extra, "Dockerfile": "FROM x\n"}
			dir := writeTask(t, files)
			res, err := Expand(dir, "compose.yaml", nil, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if !hasFinding(res.Refused(), LevelRefused, c.where, c.msg) {
				t.Fatalf("want refusal at %s containing %q; got %v", c.where, c.msg, res.Findings)
			}
		})
	}
}

func TestWorkspaceMountsRefused(t *testing.T) {
	dir := writeTask(t, map[string]string{
		"compose.yaml": "services:\n  app:\n    image: x\n    working_dir: /app\n    volumes: [\"data:/data\", \"./src:/app\", \"./conf:/etc/conf:ro\"]\n    x-heimdall: {workspace: true}\nvolumes: {data: {}}\n",
		"src/a.py":     "x\n",
		"conf/c":       "x\n",
	})
	res, err := Expand(dir, "compose.yaml", nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(res.Findings, LevelRefused, "services.app.volumes[0]", "mounts only the bench's workspace") ||
		!hasFinding(res.Findings, LevelChanged, "services.app.volumes[1]", "seeded from /app") ||
		!hasFinding(res.Findings, LevelRefused, "services.app.volumes[2]", "mounts only the bench's workspace") {
		t.Fatalf("findings %v", res.Findings)
	}
}

func TestNoWorkspace(t *testing.T) {
	dir := writeTask(t, map[string]string{"compose.yaml": "services:\n  db:\n    image: postgres:18\n"})
	res, err := Expand(dir, "compose.yaml", nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(res.Refused(), LevelRefused, "services", "no workspace service") {
		t.Fatalf("findings %v", res.Findings)
	}
	// With task.yaml's own image, every service runs beside it.
	res, err = Expand(dir, "compose.yaml", &schemas.TaskEnvironment{Image: "python:3.12-slim", WorkspacePath: "/app"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Refused()) != 0 || len(res.Services) != 1 || res.Environment.Image != "python:3.12-slim" {
		t.Fatalf("res %+v", res)
	}
}

func TestProfilesAndConventions(t *testing.T) {
	dir := writeTask(t, map[string]string{
		"compose.yaml": "services:\n  main:\n    image: x\n    working_dir: /w\n  debug:\n    image: y\n    profiles: [debug]\n  client:\n    image: z\n",
	})
	res, err := Expand(dir, "compose.yaml", nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Workspace != "main" || len(res.Services) != 1 || res.Services[0].ID != "client" {
		t.Fatalf("res %+v", res)
	}
	if !hasFinding(res.Findings, LevelIgnored, "services.debug.profiles", "not active") || !hasFinding(res.Findings, LevelChanged, "services.main", "Harbor's convention") {
		t.Fatalf("findings %v", res.Findings)
	}
}

func TestDockerfileWorkdirAndRoot(t *testing.T) {
	dir := writeTask(t, map[string]string{
		"compose.yaml": "services:\n  w:\n    build: .\n    user: root\n    x-heimdall: {workspace: true}\nx-heimdall: {build_network: true}\n",
		"Dockerfile":   "FROM a AS b\nWORKDIR /nope\nFROM c\nWORKDIR /srv\nWORKDIR app\n",
	})
	res, err := Expand(dir, "compose.yaml", nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	e := res.Environment
	if len(res.Refused()) != 0 || e.WorkspacePath != "/srv/app" || e.User != "root" || !e.BuildNetwork || e.Dockerfile != "Dockerfile" || e.Context != "." {
		t.Fatalf("env %+v findings %v", e, res.Findings)
	}
}

// TestHarborConventions maps the shape of SkillsBench's fix-visual-stability
// compose file: Harbor's variables, its log mounts and the agent harness's
// Vertex settings are recognised instead of read from the host.
func TestHarborConventions(t *testing.T) {
	compose := `services:
  main:
    build:
      context: .
      dockerfile: Dockerfile
    image: ${MAIN_IMAGE_NAME:-skillsbench-visual-stability}
    command: ["sh", "-c", "sleep infinity"]
    environment:
      - TEST_DIR=${TEST_DIR:-/tests}
      - GOOGLE_APPLICATION_CREDENTIALS=/root/.config/gcloud/application_default_credentials.json
      - CLAUDE_CODE_USE_VERTEX=${CLAUDE_CODE_USE_VERTEX}
    volumes:
      - ${HOST_VERIFIER_LOGS_PATH:-./logs}:${ENV_VERIFIER_LOGS_PATH:-/logs}
      - ${HOME}/.config/gcloud:/root/.config/gcloud:ro
    depends_on:
      - api
    networks:
      - task-net
    deploy:
      resources:
        limits:
          cpus: ${CPUS:-2}
          memory: ${MEMORY:-4096M}
  api:
    build:
      context: .
      dockerfile: Dockerfile.api
    expose:
      - "4000"
    networks:
      - task-net
networks:
  task-net:
    driver: bridge
`
	dir := writeTask(t, map[string]string{
		"environment/docker-compose.yaml": compose,
		"environment/Dockerfile":          "FROM x\nWORKDIR /app\n",
		"environment/Dockerfile.api":      "FROM y\n",
	})
	env := &schemas.TaskEnvironment{Dockerfile: "environment/Dockerfile", Context: "environment", WorkspacePath: "/app", User: "root", BuildNetwork: true}
	res, err := Expand(dir, "environment/docker-compose.yaml", env, Options{Conventions: ConventionsHarbor})
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Refused(); len(r) > 0 {
		t.Fatalf("refused: %v", r)
	}
	if res.Environment.Env["TEST_DIR"] != "/tests" || res.Environment.Env["CLAUDE_CODE_USE_VERTEX"] != "" || len(res.Environment.Env) != 1 {
		t.Fatalf("env %v", res.Environment.Env)
	}
	if len(res.Services) != 1 || res.Services[0].ID != "api" || res.Services[0].Port != 4000 || res.Services[0].Build.Dockerfile != "environment/Dockerfile.api" {
		t.Fatalf("services %+v", res.Services)
	}
	// The same file without Harbor's conventions reads host variables.
	res, err = Expand(dir, "environment/docker-compose.yaml", env, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(res.Refused(), LevelRefused, "services.main.environment[2]", "CLAUDE_CODE_USE_VERTEX is not set") ||
		!hasFinding(res.Refused(), LevelRefused, "services.main.volumes[1]", "HOME is not set") {
		t.Fatalf("plain conventions: %v", res.Findings)
	}
}

func TestDotenvAndEnvFile(t *testing.T) {
	dir := writeTask(t, map[string]string{
		"compose.yaml": "services:\n  app:\n    image: x\n    working_dir: /a\n    x-heimdall: {workspace: true}\n  db:\n    image: ${IMG}\n    env_file: [db.env, {path: missing.env, required: false}]\n    environment: {B: from-compose}\n",
		".env":         "IMG=postgres:18\n",
		"db.env":       "# c\nA=1\nB=from-file\nexport C='q z'\n",
	})
	res, err := Expand(dir, "compose.yaml", nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	db := res.Services[0]
	if len(res.Refused()) != 0 || db.Image != "postgres:18" || !reflect.DeepEqual(db.Env, map[string]string{"A": "1", "B": "from-compose", "C": "q z"}) {
		t.Fatalf("db %+v findings %v", db, res.Findings)
	}
}

// TestEnvFileOrderAndRaw pins compose semantics: later env files override
// earlier ones, the environment key beats every file whichever comes first in
// the service, and format: raw keeps a value literally.
func TestEnvFileOrderAndRaw(t *testing.T) {
	dir := writeTask(t, map[string]string{
		"compose.yaml": "services:\n  app:\n    image: x\n    working_dir: /a\n    x-heimdall: {workspace: true}\n  db:\n    image: x\n    environment: {E: explicit}\n    env_file: [a.env, b.env, {path: r.env, format: raw}]\n",
		"a.env":        "A=first\nE=from-a\nK=keep\n",
		"b.env":        "A=second\n",
		"r.env":        "R=\"q\\n\" # x\nT=word  \t\n",
	})
	res, err := Expand(dir, "compose.yaml", nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "second", "E": "explicit", "K": "keep", "R": `"q\n" # x`, "T": "word  \t"} // QC2-10-06: trailing blanks kept
	if db := res.Services[0]; len(res.Refused()) != 0 || !reflect.DeepEqual(db.Env, want) {
		t.Fatalf("db env %q findings %v", db.Env, res.Findings)
	}
}

// QC2-10-04: command: null and entrypoint: null keep the image's defaults,
// like omitted keys; an explicit empty list is still an override.
func TestNullCommandKeepsImageDefaults(t *testing.T) {
	dir := writeTask(t, map[string]string{
		"compose.yaml": "services:\n  app:\n    image: x\n    working_dir: /a\n    x-heimdall: {workspace: true}\n  db:\n    image: x\n    command: null\n    entrypoint: ~\n  e:\n    image: x\n    entrypoint: []\n",
	})
	p, err := Parse(dir, "compose.yaml", Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range p.Services {
		switch s.Name {
		case "db":
			if s.HasCommand || s.HasEntrypoint {
				t.Fatalf("null set an override: %+v", s)
			}
		case "e":
			if !s.HasEntrypoint {
				t.Fatalf("explicit empty entrypoint lost: %+v", s)
			}
		}
	}
}

func TestInterpolate(t *testing.T) {
	vars := map[string]string{"A": "a", "E": ""}
	lookup := func(k string) (string, bool) { v, ok := vars[k]; return v, ok }
	cases := []struct {
		in, out string
		unset   []string
		err     bool
	}{
		{"$A-${A}", "a-a", nil, false},
		{"$$A", "$A", nil, false},
		{"${B:-x}", "x", nil, false},
		{"${E:-x}", "x", nil, false},
		{"${E-x}", "", nil, false},
		{"${B-${A}/y}", "a/y", nil, false},
		{"${A:+set}", "set", nil, false},
		{"${B:+set}", "", nil, false},
		{"${B}", "", []string{"B"}, false},
		{"${B:?need B}", "", nil, true},
		{"${A", "${A", nil, true},
	}
	for _, c := range cases {
		r := interpolate(c.in, lookup)
		if r.value != c.out || !reflect.DeepEqual(r.unset, c.unset) || (r.err != nil) != c.err {
			t.Errorf("%q: got %q unset %v err %v", c.in, r.value, r.unset, r.err)
		}
	}
}

func TestValues(t *testing.T) {
	words, err := splitShell(`redis-server --save "" --name 'a b' x\ y`)
	if err != nil || !reflect.DeepEqual(words, []string{"redis-server", "--save", "", "--name", "a b", "x y"}) {
		t.Fatalf("split %q %v", words, err)
	}
	if _, err := splitShell(`"open`); err == nil {
		t.Fatal("unterminated quote accepted")
	}
	for in, want := range map[string]int64{"512m": 512 << 20, "1g": 1 << 30, "1.5G": 3 << 29, "1024": 1024, "4096M": 4096 << 20} {
		if got, err := parseBytes(in); err != nil || got != want {
			t.Errorf("bytes %s: %d %v", in, got, err)
		}
	}
	m, err := parseShortPort("127.0.0.1:8080:80/tcp")
	if err != nil || m.published != "127.0.0.1:8080" || !reflect.DeepEqual(m.container, []int{80}) {
		t.Fatalf("port %+v %v", m, err)
	}
	if h := (&Health{Interval: 30, Timeout: 30, Retries: 3}); readyTimeout(h) != 181 {
		t.Fatalf("default ready timeout %d", readyTimeout(h))
	}
}

func TestParseErrors(t *testing.T) {
	dir := writeTask(t, map[string]string{"a.yaml": "- not a mapping\n", "b.yaml": "services: {app: {image: x, x-heimdall: {workspace: true}, working_dir: /a}}\nnope: 1\n"})
	if _, err := Parse(dir, "a.yaml", Options{}); err == nil {
		t.Fatal("a list parsed as a compose file")
	}
	if _, err := Parse(dir, "../x.yaml", Options{}); err == nil {
		t.Fatal("a file outside the task folder was read")
	}
	p, err := Parse(dir, "b.yaml", Options{})
	if err != nil || !hasFinding(p.Refused(), LevelRefused, "nope", "top-level") {
		t.Fatalf("%v %v", p, err)
	}
}

func TestOneShotAndWorkspaceRules(t *testing.T) {
	dir := writeTask(t, map[string]string{
		"compose.yaml": `services:
  app:
    image: x
    working_dir: /a
    environment: {BENCH_KEY: x, PORT: "8080"}
    depends_on: {init: {condition: service_completed_successfully}}
    x-heimdall: {workspace: true}
  init:
    image: y
    expose: ["80"]
    x-heimdall:
      export: [{id: dump, argv: [cat, /x]}]
  side:
    image: z
    x-heimdall: {run_once: true, verifier: true}
`,
	})
	res, err := Expand(dir, "compose.yaml", nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct{ where, msg string }{
		{"services.app.environment.BENCH_KEY", "the bench's own variables"},
		{"services.init.x-heimdall.export[0]", "only path exports work"},
		{"services.init", "cannot also serve a port"},
	} {
		if !hasFinding(res.Refused(), LevelRefused, want.where, want.msg) {
			t.Errorf("want refusal at %s (%s): %v", want.where, want.msg, res.Findings)
		}
	}
	if res.Environment.Env["PORT"] != "8080" || res.Environment.Env["BENCH_KEY"] != "" {
		t.Fatalf("workspace env %v", res.Environment.Env)
	}
	var side *schemas.ServiceSpec
	for i := range res.Services {
		if res.Services[i].ID == "side" {
			side = &res.Services[i]
		}
	}
	if side == nil || !side.Compose.RunOnce || !side.Compose.Verifier || side.Ready != nil {
		t.Fatalf("side %+v", side)
	}
	if got := VerifierServices(&schemas.Task{Services: res.Services}); len(got) != 1 || got[0] != "side" {
		t.Fatalf("verifier services %v", got)
	}
}

func TestHarborServicesReachableByVerifier(t *testing.T) {
	dir := writeTask(t, map[string]string{
		"environment/docker-compose.yaml": "services:\n  main:\n    build:\n      context: ${CONTEXT_DIR}\n  api:\n    image: nginx:alpine\n",
		"environment/Dockerfile":          "FROM x\nWORKDIR /app\n",
	})
	env := &schemas.TaskEnvironment{Dockerfile: "environment/Dockerfile", Context: "environment", WorkspacePath: "/app"}
	res, err := Expand(dir, "environment/docker-compose.yaml", env, Options{Conventions: ConventionsHarbor})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Refused()) > 0 || len(res.Services) != 1 || !res.Services[0].Compose.Verifier {
		t.Fatalf("services %+v", res.Services)
	}
}

func TestAnchorsAndMergeKeys(t *testing.T) {
	dir := writeTask(t, map[string]string{
		"compose.yaml": `x-common: &common
  image: postgres:18
  environment: &env
    POSTGRES_PASSWORD: pw
  restart: always
services:
  app:
    image: x
    working_dir: /a
    x-heimdall: {workspace: true}
  db:
    <<: *common
    environment:
      <<: *env
      POSTGRES_DB: shop
  replica:
    <<: [*common]
    image: postgres:17
`,
	})
	res, err := Expand(dir, "compose.yaml", nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Refused()) > 0 || len(res.Services) != 2 {
		t.Fatalf("findings %v", res.Findings)
	}
	db, replica := res.Services[0], res.Services[1]
	if db.Image != "postgres:18" || db.Env["POSTGRES_PASSWORD"] != "pw" || db.Env["POSTGRES_DB"] != "shop" || replica.Image != "postgres:17" {
		t.Fatalf("db %+v replica %+v", db, replica)
	}
	// A billion-laughs file stops at the node budget.
	lol := "a: &a [x, x, x, x, x, x, x, x, x, x]\n"
	prev := "a"
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("l%d", i)
		lol += fmt.Sprintf("%s: &%s [*%s, *%s, *%s, *%s, *%s, *%s, *%s, *%s, *%s, *%s]\n", name, name, prev, prev, prev, prev, prev, prev, prev, prev, prev, prev)
		prev = name
	}
	dir = writeTask(t, map[string]string{"compose.yaml": "x-lol:\n  " + strings.ReplaceAll(strings.TrimSpace(lol), "\n", "\n  ") + "\nservices: {}\n"})
	if _, err := Parse(dir, "compose.yaml", Options{}); err == nil || !strings.Contains(err.Error(), "too many values") {
		t.Fatalf("billion laughs: %v", err)
	}
}
