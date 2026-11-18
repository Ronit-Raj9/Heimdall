//go:build k8s

package k8s

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Checks against a real API server that need no kubelet: every object the
// driver and `k8s gateway up` write is accepted by the server's validation
// (native sidecars, the DNS and host alias settings, owner references,
// immutable secrets, network policies), and Destroy removes the run. Run
// with BENCH_K8S_API_KUBECONFIG pointing at any cluster's API server (a
// bare kube-apiserver with etcd is enough); pods stay Pending there.
func TestAPIServerAcceptsRunObjects(t *testing.T) {
	kc := os.Getenv("BENCH_K8S_API_KUBECONFIG")
	if kc == "" {
		t.Skip("BENCH_K8S_API_KUBECONFIG is not set")
	}
	c, err := Connect(kc, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	d, err := New(Config{Client: c, NamespacePrefix: "hd-x3-api", GatewayURL: "http://bifrost." + gwNamespace() + ".svc:8080", AgentGatewayURL: "http://bifrost:8080",
		HelperImage: "bench/coding-core-base:1", CollectorImage: "bench/bifrost-bench:dev", EventsCollector: true, ProcessWatch: true, WorkspaceWatch: true,
		Local: environments.NewDockerDriver(environments.DockerWorkspaceConfig{})})
	if err != nil {
		t.Fatal(err)
	}
	// The gateway: every object, then the Service the run resolves.
	gwNS := gwNamespace()
	t.Cleanup(func() { c.Delete(t.Context(), nsPath(gwNS)) })
	o := GatewayOptions{Namespace: gwNS, BenchRoot: "..", FaultProxyImage: "bench/bifrost-bench:dev", SetupToken: "s", ControlToken: "c"}
	done := make(chan error, 1)
	gctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { done <- GatewayUp(gctx, c, o) }()
	// No controller runs here, so the deployments never get ready: wait for
	// the objects instead, then stop waiting.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var s Service
		if c.Get(ctx, corePath(gwNS, "services")+"/bifrost", &s) == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	// A second `gateway up` patches what exists.
	labels := map[string]string{LabelBench: "1", LabelRole: RoleGW}
	if err := c.Apply(ctx, deployPath(gwNS), gwBifrost, bifrostDeployment("maximhq/bifrost:v2.5.1", "IfNotPresent", labels)); err != nil {
		t.Fatalf("patch deployment: %v", err)
	}
	if err := c.Apply(ctx, corePath(gwNS, "services"), gwBifrost, service(gwBifrost, labels, []ServicePort{{Name: "http", Port: bifrostPort, TargetPort: bifrostPort, Protocol: "TCP"}}, false)); err != nil {
		t.Fatalf("patch service: %v", err)
	}
	gw, err := d.ResolveGateway(ctx)
	if err != nil {
		t.Fatalf("resolve the gateway the API server holds: %v", err)
	}
	if gw.ip == "" || gw.targetPort != 8080 || gw.selector[appNameLabel] != "bifrost" {
		t.Fatalf("gateway: %+v", gw)
	}
	// A run: Create's objects, then Exec's, then the pod (Pending).
	taskDir := t.TempDir()
	os.MkdirAll(taskDir+"/repo", 0o755)
	os.WriteFile(taskDir+"/repo/a.py", []byte("x\n"), 0o644)
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_api" + strings.ToLower(time.Now().UTC().Format("150405")), ExpID: "e_api"}, Suite: &schemas.Suite{ID: "s"},
		Task: &schemas.Task{ID: "t", Dir: taskDir, RepoDir: "repo"}, Agent: &schemas.AgentConfig{ID: "a"}, Dir: t.TempDir()}
	h, err := d.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	r, _ := d.ref(rc)
	owner, err := d.owner(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	env := environments.AgentEventEnv(rc, r.runID, []string{"HOME=/home/agent", "PATH=/opt/bench/shim:/usr/bin:/bin", "MULTI=a\nb"}, true)
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	if err := d.createRunObjects(ctx, r, owner, m, gw); err != nil {
		t.Fatalf("run objects: %v", err)
	}
	in := testInput()
	in.RunID, in.ExpID, in.GatewayIP = r.runID, r.expID, gw.ip
	if err := c.Create(ctx, corePath(r.ns, "pods"), buildPod(in, owner), nil); err != nil {
		t.Fatalf("the API server refused the run pod: %v", err)
	}
	var p Pod
	if err := c.Get(ctx, corePath(r.ns, "pods")+"/"+podName, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Spec.InitContainers) != 4 || p.Spec.InitContainers[1].RestartPolicy != "Always" || p.Spec.DNSPolicy != "None" || len(p.Spec.HostAliases) != 2 {
		t.Fatalf("stored pod: %+v", p.Spec)
	}
	if len(p.Metadata.OwnerReferences) != 1 || p.Metadata.OwnerReferences[0].Name != anchorName {
		t.Fatalf("owner: %+v", p.Metadata.OwnerReferences)
	}
	rows := d.permissionRows(ctx)
	if rows[0].Status != schemas.DoctorPass {
		t.Fatalf("permissions: %+v", rows)
	}
	if err := d.Destroy(ctx, rc); err != nil {
		t.Fatal(err)
	}
	if err := d.Destroy(ctx, rc); err != nil {
		t.Fatalf("destroy twice: %v", err)
	}
	var ns Namespace
	if err := c.Get(ctx, nsPath(r.ns), &ns); err == nil && ns.Metadata.DeletionTimestamp == "" {
		t.Fatalf("namespace %s not being deleted", r.ns)
	}
	cancel()
	<-done
}

// apiGWNS is the test's gateway namespace, fresh per test binary: a bare
// API server has no namespace controller, so a deleted namespace stays.
var apiGWNS = "hd-x3-api-gw-" + time.Now().UTC().Format("150405")

func gwNamespace() string { return apiGWNS }
