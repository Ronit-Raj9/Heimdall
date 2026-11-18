//go:build k8s

package k8s

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

// Real-cluster checks of the protocol pieces (docs/kubernetes.md). Run with
//
//	BENCH_K8S_KUBECONFIG=<file> BENCH_K8S_IMAGE=bench/coding-core-base:1 \
//	  GOWORK=off go test -tags k8s ./k8s/ -run TestCluster -v
//
// The image must be on the nodes (kind load docker-image). Each test makes
// its own namespace and deletes it.

func clusterClient(t *testing.T) (*Client, string) {
	t.Helper()
	kc, img := os.Getenv("BENCH_K8S_KUBECONFIG"), os.Getenv("BENCH_K8S_IMAGE")
	if kc == "" || img == "" {
		t.Skip("BENCH_K8S_KUBECONFIG and BENCH_K8S_IMAGE are not set")
	}
	c, err := Connect(kc, "")
	if err != nil {
		t.Fatal(err)
	}
	return c, img
}

func TestClusterExecAndAttach(t *testing.T) {
	c, img := clusterClient(t)
	ctx := t.Context()
	ns := NamespaceFor("hd-x3-it", strings.ToLower(time.Now().UTC().Format("150405.000")))
	if err := c.Create(ctx, "/api/v1/namespaces", Namespace{APIVersion: "v1", Kind: "Namespace", Metadata: ObjectMeta{Name: ns, Labels: map[string]string{LabelBench: "1", LabelRole: RoleCheck}}}, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Delete(t.Context(), nsPath(ns)) })
	uid := int64(1000)
	pod := Pod{APIVersion: "v1", Kind: "Pod", Metadata: ObjectMeta{Name: "p"}, Spec: PodSpec{RestartPolicy: "Never", AutomountServiceAccountToken: boolPtr(false),
		Containers: []Container{
			{Name: "gated", Image: img, ImagePullPolicy: "IfNotPresent", Stdin: true, StdinOnce: true,
				Command:         []string{"/bin/sh", "-c", gateScript, "sh", "/bin/sh", "-c", "echo out-line; echo err-line >&2; exit 3"},
				SecurityContext: &SecurityContext{RunAsUser: &uid, AllowPrivilegeEscalation: boolPtr(false), Capabilities: &Capabilities{Drop: []string{"ALL"}}}},
			{Name: "helper", Image: img, ImagePullPolicy: "IfNotPresent", Command: []string{"sleep", "600"},
				SecurityContext: &SecurityContext{RunAsUser: &uid, AllowPrivilegeEscalation: boolPtr(false), Capabilities: &Capabilities{Drop: []string{"ALL"}}}},
		}}}
	if err := c.Create(ctx, corePath(ns, "pods"), pod, nil); err != nil {
		t.Fatal(err)
	}
	podPath := corePath(ns, "pods") + "/p"
	waitRunning := func(name string) {
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			var p Pod
			if err := c.Get(ctx, podPath, &p); err == nil {
				if cs := containerStatus(&p, name, false); cs != nil && cs.State.Running != nil {
					return
				}
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Fatalf("%s not running", name)
	}
	waitRunning("helper")
	// Exec with stdin closed by the v5 close frame, stdout and the exit code.
	var out bytes.Buffer
	code, err := c.Exec(ctx, ns, "p", "helper", []string{"sh", "-c", "tr a-z A-Z; exit 5"}, StreamOptions{Stdin: strings.NewReader("hello"), Stdout: &out})
	if err != nil || code != 5 || out.String() != "HELLO" {
		t.Fatalf("exec: code %d err %v out %q", code, err, out.String())
	}
	big := bytes.Repeat([]byte("x"), 3<<20)
	got, err := c.ExecOutput(ctx, ns, "p", "helper", []string{"wc", "-c"}, bytes.NewReader(big), 1024)
	if err != nil || strings.TrimSpace(string(got)) != "3145728" {
		t.Fatalf("3 MiB stdin: %q %v", got, err)
	}
	// Attach releases the gate; the streams stay apart; the exit code is
	// the container's.
	waitRunning("gated")
	var so, se bytes.Buffer
	if err := c.Attach(ctx, ns, "p", "gated", StreamOptions{Stdin: strings.NewReader("\n"), Stdout: &so, Stderr: &se}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if so.String() != "out-line\n" || se.String() != "err-line\n" {
		t.Fatalf("attach streams: %q %q", so.String(), se.String())
	}
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		var p Pod
		if err := c.Get(ctx, podPath, &p); err == nil {
			if cs := containerStatus(&p, "gated", false); cs != nil && cs.State.Terminated != nil {
				if cs.State.Terminated.ExitCode != 3 {
					t.Fatalf("exit %d", cs.State.Terminated.ExitCode)
				}
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("gated container did not terminate")
}
