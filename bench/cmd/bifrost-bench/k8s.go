package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/k8s"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

// X3: the Kubernetes driver's wiring and the `k8s` command
// (docs/kubernetes.md).

func init() {
	register("k8s", "kubernetes driver: check the cluster, run the in-cluster gateway, load images, remove leftovers", runK8s)
}

// useKubernetes swaps the registry's environment for the Kubernetes driver
// and wraps the gateway so its preflight probes egress from the cluster,
// when bench.yaml sets environment.driver kubernetes. The Docker driver
// stays inside it for image metadata.
func (a *app) useKubernetes(dockerCfg environments.DockerWorkspaceConfig) error {
	if a.cfg.Environment.DriverName() != environments.DriverKubernetes {
		return nil
	}
	d, err := newKubernetesDriver(a.cfg, dockerCfg, a)
	if err != nil {
		return err
	}
	a.reg.Environments = []schemas.Environment{d}
	if a.reg.Gateway != nil {
		a.reg.Gateway = k8s.WrapGateway(a.reg.Gateway, d)
	}
	return nil
}

func newKubernetesDriver(cfg *lib.Config, dockerCfg environments.DockerWorkspaceConfig, a *app) (*k8s.Driver, error) {
	kc := cfg.Kubernetes
	client, err := k8s.Connect(cfg.Path(kc.Kubeconfig), kc.Context)
	if err != nil {
		return nil, fmt.Errorf("kubernetes driver: %w", err)
	}
	tols := make([]k8s.Toleration, 0, len(kc.Tolerations))
	for _, t := range kc.Tolerations {
		tols = append(tols, k8s.Toleration{Key: t.Key, Operator: t.Operator, Value: t.Value, Effect: t.Effect, TolerationSeconds: t.TolerationSeconds})
	}
	d, err := k8s.New(k8s.Config{
		Client: client, NamespacePrefix: kc.NamespacePrefix, GatewayURL: kc.GatewayURL, AgentGatewayURL: cfg.Gateway.AgentURL,
		EvalNetwork: cfg.Networks.Eval, HelperImage: cfg.Images.Helper, CollectorImage: toolProxyImage(cfg), ImagePullPolicy: kc.ImagePullPolicy,
		ServiceAccount: kc.ServiceAccount, NodeSelector: kc.NodeSelector, Tolerations: tols,
		ProcessWatch: dockerCfg.ProcessWatch, WorkspaceWatch: dockerCfg.WorkspaceWatch, WatchIntervalMS: dockerCfg.WatchIntervalMS,
		EventsCollector: dockerCfg.EventsCollector, Local: environments.NewDockerDriver(dockerCfg), Docker: a.docker, Logger: a.logger,
		Install: k8s.InstallID(cfg.Root), RuntimeClass: kc.RuntimeClass, Namespaces: kc.Namespaces,
	})
	if err != nil {
		return nil, fmt.Errorf("kubernetes driver: %w", err)
	}
	return d, nil
}

// kubernetesDriver returns the app's Kubernetes driver, building one from
// the config's kubernetes section when environment.driver is docker, so
// `k8s check` works before the switch.
func (a *app) kubernetesDriver() (*k8s.Driver, error) {
	for _, e := range a.reg.Environments {
		if d, ok := e.(*k8s.Driver); ok {
			return d, nil
		}
	}
	return newKubernetesDriver(a.cfg, environments.DockerWorkspaceConfig{EvalNetwork: a.cfg.Networks.Eval, HelperImage: a.cfg.Images.Helper,
		Logger: a.logger, Docker: a.docker, ToolProxyImage: a.cfg.Images.ToolProxy, ProcessWatch: a.cfg.Observers.ProcessOn(),
		WorkspaceWatch: a.cfg.Observers.WorkspaceOn(), WatchIntervalMS: a.cfg.Observers.IntervalMS, EventsCollector: a.cfg.Events.CollectorOn()}, a)
}

const k8sUsage = `usage: bifrost-bench k8s <check|gateway up|gateway down|images load|cleanup> [flags]`

func runK8s(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, k8sUsage)
		return exitUsage
	}
	switch args[0] {
	case "check":
		return runK8sCheck(args[1:])
	case "gateway":
		if len(args) < 2 || (args[1] != "up" && args[1] != "down") {
			fmt.Fprintln(os.Stderr, k8sUsage)
			return exitUsage
		}
		return runK8sGateway(args[1], args[2:])
	case "images":
		if len(args) < 2 || args[1] != "load" {
			fmt.Fprintln(os.Stderr, k8sUsage)
			return exitUsage
		}
		return runK8sImages(args[2:])
	case "cleanup":
		return runK8sCleanup(args[1:])
	}
	fmt.Fprintln(os.Stderr, k8sUsage)
	return exitUsage
}

func k8sApp(ctx context.Context, config string) (*app, *k8s.Driver, error) {
	a, err := newApp(ctx, config, appOptions{})
	if err != nil {
		return nil, nil, err
	}
	d, err := a.kubernetesDriver()
	if err != nil {
		return nil, nil, err
	}
	return a, d, nil
}

func runK8sCheck(args []string) int {
	fs := flag.NewFlagSet("k8s check", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	var images csvFlag
	fs.Var(&images, "image", "an image runs will use (repeatable); the helper image is always checked")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	ctx := context.Background()
	_, d, err := k8sApp(ctx, *config)
	if err != nil {
		return fail("%v", err)
	}
	ok := true
	for _, c := range d.Check(ctx, images) {
		fmt.Printf("%-5s %-22s %s\n", strings.ToUpper(string(c.Status)), c.ID, c.Detail)
		if c.Status == schemas.DoctorFail {
			ok = false
		}
	}
	if !ok {
		return 1
	}
	return exitOK
}

func runK8sGateway(verb string, args []string) int {
	fs := flag.NewFlagSet("k8s gateway "+verb, flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	gwNode := fs.Int("node-port-gateway", 0, "publish Bifrost on this NodePort (a kind cluster maps it to the host; deploy/k8s/kind.yaml uses 30080)")
	ctlNode := fs.Int("node-port-control", 0, "publish the fault proxy control API on this NodePort (deploy/k8s/kind.yaml uses 30091)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	ctx := context.Background()
	a, _, err := k8sApp(ctx, *config)
	if err != nil {
		return fail("%v", err)
	}
	kc := a.cfg.Kubernetes
	ns, err := k8s.GatewayNamespace(kc.GatewayURL)
	if err != nil {
		return fail("%v", err)
	}
	client, err := k8s.Connect(a.cfg.Path(kc.Kubeconfig), kc.Context)
	if err != nil {
		return fail("%v", err)
	}
	if verb == "down" {
		if err := k8s.GatewayDown(ctx, client, ns); err != nil {
			return fail("gateway down: %v", err)
		}
		fmt.Printf("removed namespace %s\n", ns)
		return exitOK
	}
	image := toolProxyImage(a.cfg)
	err = k8s.GatewayUp(ctx, client, k8s.GatewayOptions{Namespace: ns, BenchRoot: a.cfg.Root, FaultProxyImage: image,
		SetupToken: os.Getenv(a.cfg.Gateway.AdminTokenEnv), ControlToken: os.Getenv("BENCH_FAULTPROXY_TOKEN"),
		BifrostNodePort: *gwNode, ControlNodePort: *ctlNode, PullPolicy: kc.ImagePullPolicy, KeySlots: a.cfg.Gateway.KeySlots})
	if err != nil {
		return fail("gateway up: %v", err)
	}
	fmt.Printf("gateway ready in namespace %s: Bifrost at %s, fault proxy at faultproxy.%s.svc:9100 (no provider keys: scripted model only)\n", ns, kc.GatewayURL, ns)
	if *gwNode > 0 || *ctlNode > 0 {
		fmt.Printf("node ports: Bifrost %d, fault proxy control %d (point gateway.url and faultproxy.control_url at the host ports they map to)\n", *gwNode, *ctlNode)
	}
	return exitOK
}

// runK8sImages loads local images into a kind cluster (kind load
// docker-image). An image whose reference carries a registry digest is
// loaded by its tag; for any other cluster, push the images to a registry
// the nodes can pull from and reference them by digest.
func runK8sImages(args []string) int {
	fs := flag.NewFlagSet("k8s images load", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	cluster := fs.String("cluster", "", "kind cluster name (default kubernetes.kind_cluster)")
	gateway := fs.Bool("gateway", false, "also load the gateway images (the pinned Bifrost image and images.toolproxy)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	ctx := context.Background()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	name := *cluster
	if name == "" {
		name = a.cfg.Kubernetes.KindCluster
	}
	if name == "" {
		return fail("no kind cluster: pass -cluster or set kubernetes.kind_cluster; for other clusters push the images to a registry")
	}
	want := map[string]bool{a.cfg.Images.Helper: true, toolProxyImage(a.cfg): true} // the collector sidecar
	for _, i := range fs.Args() {
		want[i] = true
	}
	if *gateway {
		img, err := k8s.PinnedBifrostImage(a.cfg.Root)
		if err != nil {
			return fail("%v", err)
		}
		want[img] = true
		want[toolProxyImage(a.cfg)] = true
	}
	var refs []string
	for i := range want {
		if i == "" {
			continue
		}
		ref, _, _ := strings.Cut(i, "@")
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	for _, ref := range refs {
		fmt.Printf("loading %s into kind cluster %s\n", ref, name)
		cmd := exec.CommandContext(ctx, "kind", "load", "docker-image", "--name", name, ref)
		cmd.Stdout, cmd.Stderr = io.Discard, os.Stderr
		if err := cmd.Run(); err != nil {
			return fail("kind load docker-image %s: %v", ref, err)
		}
	}
	return exitOK
}

// runK8sCleanup lists, and with -delete removes, every namespace the driver
// created (label heimdall.dev/bench) except the gateway's: leftovers of
// runs the bench lost track of.
func runK8sCleanup(args []string) int {
	fs := flag.NewFlagSet("k8s cleanup", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	del := fs.Bool("delete", false, "delete the stale ones (default: list only)")
	grace := fs.Duration("grace", engine.DefaultSweepGrace, "a namespace with no lease information is stale only when older than this")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	ctx := context.Background()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	client, err := k8s.Connect(a.cfg.Path(a.cfg.Kubernetes.Kubeconfig), a.cfg.Kubernetes.Context)
	if err != nil {
		return fail("%v", err)
	}
	if len(a.cfg.Kubernetes.Namespaces) > 0 {
		return k8sCleanupPool(ctx, a, client, *del, *grace)
	}
	var list struct {
		Items []k8s.Namespace `json:"items"`
	}
	q := url.Values{"labelSelector": {k8s.LabelBench + "=1," + k8s.LabelRole + "!=" + k8s.RoleGW}}
	if err := client.Do(ctx, "GET", "/api/v1/namespaces", q, nil, &list); err != nil {
		return fail("list namespaces: %v", err)
	}
	// 2-D-14: only this install's namespaces, and only runs that are not live.
	lv, err := engine.ScanRunLiveness([]string{a.store.Root(), filepath.Join(a.cfg.Root, engine.ChecksRoot)}, engine.DefaultStaleAfter, time.Now())
	if err != nil {
		return fail("read run leases (nothing deleted): %v", err)
	}
	prefix := a.cfg.Kubernetes.NamespacePrefix
	if prefix == "" {
		prefix = k8s.DefaultNamespacePrefix
	}
	verdicts := k8s.ClassifyCleanup(list.Items, k8s.CleanupInput{Prefix: prefix, Install: k8s.InstallID(a.cfg.Root), Live: lv.Live, Dead: lv.Dead, Grace: *grace, Now: time.Now()})
	removable := 0
	for _, v := range verdicts {
		n := v.Namespace
		action := "keep"
		if v.Remove {
			action = "stale"
			removable++
		}
		fmt.Printf("%s run=%s role=%s phase=%s %s (%s)\n", n.Metadata.Name, n.Metadata.Labels[k8s.LabelRun], n.Metadata.Labels[k8s.LabelRole], n.Status.Phase, action, v.Reason)
		if *del && v.Remove {
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := client.Delete(cctx, "/api/v1/namespaces/"+n.Metadata.Name)
			cancel()
			if err != nil {
				return fail("delete %s: %v", n.Metadata.Name, err)
			}
		}
	}
	fmt.Printf("%d namespaces of prefix %s, %d stale\n", len(verdicts), prefix, removable)
	return exitOK
}

// k8sCleanupPool is `k8s cleanup` in namespace pool mode: it lists, and
// with -delete releases, this install's stale anchors in the pool
// namespaces (with the run objects they left). It never deletes a
// namespace.
func k8sCleanupPool(ctx context.Context, a *app, client *k8s.Client, del bool, grace time.Duration) int {
	var anchors []k8s.PoolAnchor
	for _, ns := range a.cfg.Kubernetes.Namespaces {
		var cm k8s.ConfigMap
		err := client.Get(ctx, "/api/v1/namespaces/"+url.PathEscape(ns)+"/configmaps/run", &cm)
		if k8s.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fail("read the anchor in %s (nothing deleted): %v", ns, err)
		}
		anchors = append(anchors, k8s.PoolAnchor{Namespace: ns, Anchor: cm})
	}
	lv, err := engine.ScanRunLiveness([]string{a.store.Root(), filepath.Join(a.cfg.Root, engine.ChecksRoot)}, engine.DefaultStaleAfter, time.Now())
	if err != nil {
		return fail("read run leases (nothing deleted): %v", err)
	}
	verdicts := k8s.ClassifyPoolAnchors(anchors, k8s.CleanupInput{Install: k8s.InstallID(a.cfg.Root), Live: lv.Live, Dead: lv.Dead, Grace: grace, Now: time.Now()})
	removable := 0
	for _, v := range verdicts {
		action := "keep"
		if v.Remove {
			action = "stale"
			removable++
		}
		l := v.Anchor.Metadata.Labels
		fmt.Printf("%s anchor run=%s role=%s %s (%s)\n", v.Namespace, l[k8s.LabelRun], l[k8s.LabelRole], action, v.Reason)
		if del && v.Remove {
			cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			err := k8s.ReleaseStaleAnchor(cctx, client, v.Namespace, v.Anchor)
			cancel()
			if err != nil {
				return fail("release %s: %v", v.Namespace, err)
			}
		}
	}
	fmt.Printf("%d anchors of this install in %d pool namespaces, %d stale\n", len(verdicts), len(a.cfg.Kubernetes.Namespaces), removable)
	return exitOK
}

// toolProxyImage is images.toolproxy, or the default bench image: it runs
// the fault proxy in the cluster and the agent event collector in run pods.
func toolProxyImage(cfg *lib.Config) string {
	if cfg.Images.ToolProxy != "" {
		return cfg.Images.ToolProxy
	}
	return environments.DefaultToolProxyImage
}
