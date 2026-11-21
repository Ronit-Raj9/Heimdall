package byoworker

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAPI records requests and answers them from a handler table.
type fakeAPI struct {
	mu    sync.Mutex
	calls []string
	auth  []string
	body  map[string]map[string]any // "METHOD path" -> decoded body
	route func(w http.ResponseWriter, r *http.Request, body map[string]any)
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	key := r.Method + " " + r.URL.RequestURI()
	f.mu.Lock()
	f.calls = append(f.calls, key)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	if f.body == nil {
		f.body = map[string]map[string]any{}
	}
	f.body[key] = body
	f.mu.Unlock()
	f.route(w, r, body)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

var testBoot = Boot{Name: "fly-1", Pool: "fly-1", Slots: 2, ServerURL: "https://heimdall.example.com/", RunnerToken: "hmr_abcdefgh_secret"}

func TestRunloopDriver(t *testing.T) {
	f := &fakeAPI{route: func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/devboxes":
			writeJSON(w, 200, map[string]any{"devboxes": []any{}, "has_more": false})
		case r.Method == "POST" && r.URL.Path == "/v1/devboxes":
			writeJSON(w, 200, map[string]any{"id": "dbx_1", "status": "provisioning"})
		case r.Method == "GET" && r.URL.Path == "/v1/devboxes/dbx_1":
			writeJSON(w, 200, map[string]any{"id": "dbx_1", "status": "failure", "failure_reason": "out_of_disk"})
		case r.Method == "POST" && r.URL.Path == "/v1/devboxes/dbx_1/shutdown":
			writeJSON(w, 200, map[string]any{"id": "dbx_1", "status": "shutdown"})
		case r.URL.Path == "/v1/devboxes/gone" || r.URL.Path == "/v1/devboxes/gone/shutdown":
			writeJSON(w, 404, map[string]any{"error": "not found"})
		default:
			writeJSON(w, 500, nil)
		}
	}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	d := Runloop{BaseURL: srv.URL}
	settings, err := d.Validate(map[string]string{"blueprint": "heimdall-runner"}, "rl_api_key_123")
	if err != nil || settings[RunloopSize] != "X_LARGE" {
		t.Fatalf("validate: %v %v", settings, err)
	}
	for _, bad := range []map[string]string{{"size": "HUGE"}, {"hours": "49"}, {"blueprint": "a b"}, {"base_url": "http://10.0.0.1"}} {
		if _, err := d.Validate(bad, "rl_api_key_123"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("validate %v: %v", bad, err)
		}
	}
	tg := Target{Settings: settings, Secret: "rl_api_key_123"}
	ctx := context.Background()
	if h := d.Check(ctx, tg); !h.OK || !strings.Contains(h.Capacity, "4 CPU, 16 GB memory") {
		t.Fatalf("check: %+v", h)
	}
	in, err := d.Start(ctx, tg, testBoot)
	if err != nil || in.ID != "dbx_1" || in.State != InstanceStarting {
		t.Fatalf("start: %+v %v", in, err)
	}
	body := f.body["POST /v1/devboxes"]
	env := body["environment_variables"].(map[string]any)
	if env[EnvRunnerToken] != testBoot.RunnerToken || env[EnvServer] != "https://heimdall.example.com" || env[EnvLabels] != "platform=runloop" || env[EnvSlots] != "2" {
		t.Fatalf("env: %v", env)
	}
	if body["blueprint_name"] != "heimdall-runner" || !strings.Contains(body["entrypoint"].(string), "--foreground") {
		t.Fatalf("body: %v", body)
	}
	if strings.Contains(body["entrypoint"].(string), testBoot.RunnerToken) {
		t.Fatal("the token is in the entrypoint line")
	}
	lp := body["launch_parameters"].(map[string]any)
	if lp["resource_size_request"] != "X_LARGE" || lp["keep_alive_time_seconds"].(float64) != 48*3600 {
		t.Fatalf("launch: %v", lp)
	}
	if st, err := d.Status(ctx, tg, "dbx_1"); err != nil || st.State != InstanceFailed || !strings.Contains(st.Detail, "out_of_disk") {
		t.Fatalf("status: %+v %v", st, err)
	}
	if st, err := d.Status(ctx, tg, "gone"); err != nil || st.State != InstanceStopped {
		t.Fatalf("gone: %+v %v", st, err)
	}
	if err := d.Stop(ctx, tg, "dbx_1"); err != nil {
		t.Fatal(err)
	}
	if err := d.Stop(ctx, tg, "gone"); err != nil {
		t.Fatalf("stop of a gone devbox: %v", err)
	}
	for _, a := range f.auth {
		if a != "Bearer rl_api_key_123" {
			t.Fatalf("auth header %q", a)
		}
	}
	if _, err := d.Start(ctx, tg, Boot{Name: "x", Pool: "x", ServerURL: "http://evil.example.com", RunnerToken: "hmr_x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("plain http server: %v", err)
	}
}

func TestRunloopRefusedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 401, map[string]string{"error": "bad key"}) }))
	defer srv.Close()
	h := Runloop{BaseURL: srv.URL}.Check(context.Background(), Target{Settings: map[string]string{}, Secret: "nope-nope"})
	if h.OK || !strings.Contains(h.Detail, "refused") {
		t.Fatalf("health %+v", h)
	}
}

func TestFlyDriver(t *testing.T) {
	f := &fakeAPI{route: func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/apps/heimdall-workers":
			writeJSON(w, 200, map[string]any{"name": "heimdall-workers", "status": "deployed"})
		case r.Method == "GET" && r.URL.Path == "/v1/apps/missing":
			writeJSON(w, 404, map[string]any{"error": "app not found"})
		case r.Method == "POST" && r.URL.Path == "/v1/apps/heimdall-workers/machines":
			writeJSON(w, 200, map[string]any{"id": "m_1", "state": "created", "region": "iad"})
		case r.Method == "GET" && r.URL.Path == "/v1/apps/heimdall-workers/machines/m_1":
			writeJSON(w, 200, map[string]any{"id": "m_1", "state": "started"})
		case r.Method == "DELETE" && r.URL.Path == "/v1/apps/heimdall-workers/machines/m_1":
			if r.URL.Query().Get("force") != "true" {
				writeJSON(w, 412, nil)
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true})
		default:
			writeJSON(w, 404, nil)
		}
	}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	d := Fly{BaseURL: srv.URL}
	tokenValue := "FlyV1 fm2_abcdefghijklmnop"
	settings, err := d.Validate(map[string]string{"app": "heimdall-workers", "region": "iad"}, tokenValue)
	if err != nil || settings[FlyCPUs] != "4" || settings[FlyMemoryMB] != "8192" || settings[FlyCPUKind] != "performance" {
		t.Fatalf("validate: %v %v", settings, err)
	}
	for _, bad := range []map[string]string{{"app": "Bad_App"}, {"app": "a", "region": "iadx"}, {"app": "a", "cpus": "64"}, {"app": "a", "cpu_kind": "gpu"}, {"app": "a", "image": "x; rm -rf /"}, {"app": "a", "token": "x"}} {
		if _, err := d.Validate(bad, tokenValue); !errors.Is(err, ErrInvalid) {
			t.Fatalf("validate %v: %v", bad, err)
		}
	}
	tg := Target{Settings: settings, Secret: tokenValue}
	ctx := context.Background()
	if h := d.Check(ctx, tg); !h.OK || h.Capacity != "Fly Machine: 4 CPU (performance), 8 GB memory" {
		t.Fatalf("check: %+v", h)
	}
	missing := Target{Settings: map[string]string{"app": "missing", "cpus": "1", "memory_mb": "1024", "cpu_kind": "shared"}, Secret: tokenValue}
	if h := d.Check(ctx, missing); h.OK || !strings.Contains(h.Detail, "fly apps create missing") {
		t.Fatalf("missing app: %+v", h)
	}
	in, err := d.Start(ctx, tg, testBoot)
	if err != nil || in.ID != "m_1" || in.State != InstanceStarting {
		t.Fatalf("start: %+v %v", in, err)
	}
	body := f.body["POST /v1/apps/heimdall-workers/machines"]
	cfg := body["config"].(map[string]any)
	if cfg["image"] != FlyDefaultImage || body["region"] != "iad" || body["name"] != "fly-1" {
		t.Fatalf("body: %v", body)
	}
	guest := cfg["guest"].(map[string]any)
	if guest["cpus"].(float64) != 4 || guest["memory_mb"].(float64) != 8192 {
		t.Fatalf("guest: %v", guest)
	}
	if env := cfg["env"].(map[string]any); env[EnvRunnerToken] != testBoot.RunnerToken || env[EnvLabels] != "platform=fly" {
		t.Fatalf("env: %v", env)
	}
	if st, err := d.Status(ctx, tg, "m_1"); err != nil || st.State != InstanceRunning {
		t.Fatalf("status: %+v %v", st, err)
	}
	if st, err := d.Status(ctx, tg, "m_404"); err != nil || st.State != InstanceStopped {
		t.Fatalf("gone: %+v %v", st, err)
	}
	if err := d.Stop(ctx, tg, "m_1"); err != nil {
		t.Fatal(err)
	}
	if f.auth[0] != "Bearer "+tokenValue {
		t.Fatalf("auth %q", f.auth[0])
	}
}

// fakeKube is a TLS API server with the endpoints the Kubernetes driver
// uses; deny lists "verb resource" access reviews it refuses.
type fakeKube struct {
	mu      sync.Mutex
	deny    map[string]bool
	objects map[string]map[string]any // path -> object
	ready   bool
	calls   []string
}

func (k *fakeKube) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.calls = append(k.calls, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer sa-token" {
		writeJSON(w, 401, map[string]any{"kind": "Status", "code": 401, "reason": "Unauthorized"})
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var obj map[string]any
	_ = json.Unmarshal(raw, &obj)
	switch {
	case r.URL.Path == "/version":
		writeJSON(w, 200, map[string]any{"major": "1", "minor": "31", "gitVersion": "v1.31.2"})
	case strings.HasSuffix(r.URL.Path, "/selfsubjectaccessreviews"):
		ra := obj["spec"].(map[string]any)["resourceAttributes"].(map[string]any)
		allowed := !k.deny[ra["verb"].(string)+" "+ra["resource"].(string)] && ra["namespace"] == "heimdall"
		writeJSON(w, 201, map[string]any{"status": map[string]any{"allowed": allowed}})
	case r.Method == "POST":
		name := obj["metadata"].(map[string]any)["name"].(string)
		k.objects[r.URL.Path+"/"+name] = obj
		writeJSON(w, 201, obj)
	case r.Method == "GET":
		o, ok := k.objects[r.URL.Path]
		if !ok {
			writeJSON(w, 404, map[string]any{"kind": "Status", "code": 404, "reason": "NotFound"})
			return
		}
		if k.ready {
			o["status"] = map[string]any{"readyReplicas": 1}
		}
		writeJSON(w, 200, o)
	case r.Method == "DELETE":
		delete(k.objects, r.URL.Path)
		writeJSON(w, 200, map[string]any{"kind": "Status"})
	default:
		writeJSON(w, 405, nil)
	}
}

func kubeTarget(t *testing.T, srv *httptest.Server, settings map[string]string) Target {
	t.Helper()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	cred, _ := json.Marshal(KubeToken{Server: srv.URL, CA: string(ca), Token: "sa-token"})
	return Target{Settings: settings, Secret: string(cred)}
}

func TestKubernetesDriver(t *testing.T) {
	fk := &fakeKube{deny: map[string]bool{}, objects: map[string]map[string]any{}}
	srv := httptest.NewTLSServer(fk)
	defer srv.Close()
	d := Kubernetes{AllowPrivate: true, Now: func() time.Time { return time.Unix(0, 0) }}
	tg := kubeTarget(t, srv, map[string]string{"namespace": "heimdall", "image": "registry.example.com/heimdall-runner:1"})
	settings, err := d.Validate(tg.Settings, tg.Secret)
	if err != nil {
		t.Fatal(err)
	}
	tg.Settings = settings
	ctx := context.Background()
	if h := d.Check(ctx, tg); !h.OK || !strings.Contains(h.Capacity, "namespace heimdall") || !strings.Contains(h.Detail, "v1.31.2") {
		t.Fatalf("check: %+v", h)
	}
	fk.deny["create deployments"] = true
	if h := d.Check(ctx, tg); h.OK || !strings.Contains(h.Detail, "create deployments") {
		t.Fatalf("denied check: %+v", h)
	}
	in, err := d.Start(ctx, tg, Boot{Name: "Kube_1", Pool: "Kube_1", Slots: 3, ServerURL: "https://heimdall.example.com", RunnerToken: "hmr_abcdefgh_secret"})
	if err != nil || in.ID != "kube-1" || in.State != InstanceStarting {
		t.Fatalf("start: %+v %v", in, err)
	}
	sec := fk.objects["/api/v1/namespaces/heimdall/secrets/kube-1-token"]
	dep := fk.objects["/apis/apps/v1/namespaces/heimdall/deployments/kube-1"]
	if sec == nil || dep == nil {
		t.Fatalf("objects: %v", fk.calls)
	}
	if sec["stringData"].(map[string]any)["token"] != "hmr_abcdefgh_secret" {
		t.Fatalf("secret: %v", sec)
	}
	b, _ := json.Marshal(dep)
	for _, want := range []string{`"serviceAccountName":"heimdall-runner"`, `"allowPrivilegeEscalation":false`, `"-token-file","/var/run/heimdall/token"`, `"-slots","3"`, `platform=kubernetes`, `registry.example.com/heimdall-runner:1`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("deployment lacks %s: %s", want, b)
		}
	}
	if strings.Contains(string(b), "hmr_abcdefgh_secret") {
		t.Fatal("the runner token is in the Deployment")
	}
	if st, err := d.Status(ctx, tg, "kube-1"); err != nil || st.State != InstanceStarting {
		t.Fatalf("status: %+v %v", st, err)
	}
	fk.ready = true
	if st, err := d.Status(ctx, tg, "kube-1"); err != nil || st.State != InstanceRunning {
		t.Fatalf("ready status: %+v %v", st, err)
	}
	if err := d.Stop(ctx, tg, "kube-1"); err != nil || len(fk.objects) != 0 {
		t.Fatalf("stop: %v %v", err, fk.objects)
	}
	if st, _ := d.Status(ctx, tg, "kube-1"); st.State != InstanceStopped {
		t.Fatalf("after stop: %+v", st)
	}
	noImage := Target{Settings: map[string]string{"namespace": "heimdall"}, Secret: tg.Secret}
	if _, err := d.Start(ctx, noImage, testBoot); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no image: %v", err)
	}
}

func TestKubernetesValidate(t *testing.T) {
	d := Kubernetes{}
	pub := `{"server":"https://k8s.example.com:6443","token":"sa-token"}`
	cases := []struct {
		settings map[string]string
		secret   string
		want     string
	}{
		{map[string]string{"namespace": "Bad NS"}, pub, "namespace"},
		{map[string]string{"namespace": "heimdall-run-x"}, pub, "run namespace"},
		{map[string]string{"namespace": "heimdall", "cpu": "lots"}, pub, "quantities"},
		{map[string]string{"namespace": "heimdall"}, `{"server":"https://10.0.0.4:6443","token":"x"}`, "not public"},
		{map[string]string{"namespace": "heimdall"}, `{"server":"https://localhost:6443","token":"x"}`, "localhost"},
		{map[string]string{"namespace": "heimdall"}, `{"server":"http://k8s.example.com","token":"x"}`, "not https"},
		{map[string]string{"namespace": "heimdall"}, kubeconfigWith("certificate-authority: /etc/kubernetes/pki/ca.crt"), "names a file"},
		{map[string]string{"namespace": "heimdall"}, kubeconfigWith("insecure-skip-tls-verify: true"), "insecure-skip-tls-verify"},
	}
	for _, c := range cases {
		_, err := d.Validate(c.settings, c.secret)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%v %q: %v (want %q)", c.settings, c.secret, err, c.want)
		}
	}
	if _, err := d.Validate(map[string]string{"namespace": "heimdall"}, kubeconfigWith("")); err != nil {
		t.Fatalf("inline kubeconfig: %v", err)
	}
	if _, err := d.Validate(map[string]string{"namespace": "heimdall"}, pub); err != nil {
		t.Fatalf("token: %v", err)
	}
}

func kubeconfigWith(clusterExtra string) string {
	extra := ""
	if clusterExtra != "" {
		extra = "\n    " + clusterExtra
	}
	return `apiVersion: v1
kind: Config
current-context: org
clusters:
- name: c
  cluster:
    server: https://k8s.example.com:6443` + extra + `
contexts:
- name: org
  context: {cluster: c, user: u, namespace: heimdall}
users:
- name: u
  user: {token: sa-token}
`
}

func TestKubeManifest(t *testing.T) {
	m := KubeManifest("acme-workers")
	for _, want := range []string{"namespace: acme-workers", "kind: ValidatingAdmissionPolicy", "system:serviceaccount:acme-workers:heimdall-runner", "name: heimdall-connect-token", "kind: Role\n"} {
		if !strings.Contains(m, want) {
			t.Fatalf("manifest lacks %q", want)
		}
	}
	if strings.Contains(m, "{{NS}}") {
		t.Fatal("unreplaced placeholder")
	}
}

func TestBootScriptReadsEnvOnly(t *testing.T) {
	s := BootScript()
	if !strings.Contains(s, `"$HEIMDALL_SERVER/api/bench/runners/install.sh"`) || !strings.Contains(s, "sh -s -- --foreground --install-docker") {
		t.Fatalf("boot script: %s", s)
	}
	env := BootEnv(Boot{Name: "n", Pool: "p", ServerURL: "https://h/", RunnerToken: "hmr_t", Labels: []string{"gpu=1"}}, PlatformFly)
	if env[EnvSlots] != "1" || env[EnvLabels] != "platform=fly,gpu=1" || env[EnvServer] != "https://h" {
		t.Fatalf("env: %v", env)
	}
}
