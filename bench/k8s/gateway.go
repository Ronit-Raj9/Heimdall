package k8s

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// `k8s gateway up|down` (docs/kubernetes.md, "The gateway in the cluster"):
// the same pinned Bifrost image as deploy/docker-compose.yml and the bench's
// fault proxy, in one pool namespace, so run pods reach a gateway inside
// the cluster and every model call still goes Bifrost -> fault proxy. The
// fault proxy gets no provider key: every call is answered by its scripted
// model, so a run through it costs nothing. Bifrost keeps its config and
// logs in SQLite on an emptyDir instead of the compose stack's Postgres;
// the bench reads both through Bifrost's API either way.

// Gateway object names and ports.
const (
	gwBifrost      = "bifrost"
	gwFaultProxy   = "faultproxy"
	gwSecret       = "gateway-tokens"
	gwConfig       = "bifrost-config"
	gwDatasheet    = "bifrost-datasheet"
	gwProxyConfig  = "faultproxy-config"
	bifrostPort    = 8080
	proxyPort      = 9100
	controlPort    = 9101
	appNameLabel   = "app.kubernetes.io/name"
	gwReadyTimeout = 5 * time.Minute
)

// GatewayOptions configures `k8s gateway up`.
type GatewayOptions struct {
	Namespace       string // the pool namespace, from kubernetes.gateway_url
	BenchRoot       string // where deploy/ lives
	FaultProxyImage string // the bench image (images.toolproxy)
	SetupToken      string // BIFROST_SETUP_TOKEN
	ControlToken    string // BENCH_FAULTPROXY_TOKEN
	BifrostNodePort int    // 0 keeps the Service ClusterIP
	ControlNodePort int
	PullPolicy      string
}

// GatewayNamespace is the namespace kubernetes.gateway_url names.
func GatewayNamespace(gatewayURL string) (string, error) {
	_, ns, _, err := parseServiceURL(gatewayURL)
	return ns, err
}

// PinnedBifrostImage reads the Bifrost image, digest and all, from
// deploy/docker-compose.yml, so the cluster runs the very image the Docker
// stack does.
func PinnedBifrostImage(benchRoot string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(benchRoot, "deploy", "docker-compose.yml"))
	if err != nil {
		return "", fmt.Errorf("read the compose file for the pinned Bifrost image: %w", err)
	}
	var c struct {
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return "", fmt.Errorf("parse deploy/docker-compose.yml: %w", err)
	}
	img := c.Services["bifrost"].Image
	if !strings.Contains(img, "@sha256:") {
		return "", fmt.Errorf("deploy/docker-compose.yml pins no Bifrost digest (%q)", img)
	}
	return img, nil
}

// bifrostConfig is deploy/bifrost/config.json with both stores moved to
// SQLite in the pod's data folder. It is pure apart from reading the file.
func bifrostConfig(benchRoot string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(benchRoot, "deploy", "bifrost", "config.json"))
	if err != nil {
		return nil, fmt.Errorf("read Bifrost config: %w", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse Bifrost config: %w", err)
	}
	cfg["config_store"] = map[string]any{"enabled": true, "type": "sqlite", "config": map[string]any{"path": "/app/data/config.db"}}
	logs := map[string]any{"enabled": true, "type": "sqlite", "config": map[string]any{"path": "/app/data/logs.db"}}
	if old, ok := cfg["logs_store"].(map[string]any); ok && old["writer"] != nil {
		logs["writer"] = old["writer"]
	}
	cfg["logs_store"] = logs
	return json.MarshalIndent(cfg, "", "  ")
}

// gzipFile reads and compresses a datasheet file: the model parameter
// sheet is over a config map's 1 MiB limit as plain JSON.
func gzipFile(p string) ([]byte, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GatewayUp creates or updates the pool namespace's gateway and waits until
// both deployments are ready.
func GatewayUp(ctx context.Context, c *Client, o GatewayOptions) error {
	if o.SetupToken == "" || o.ControlToken == "" {
		return errors.New("BIFROST_SETUP_TOKEN and BENCH_FAULTPROXY_TOKEN must be set: they become the in-cluster gateway's admin and control tokens")
	}
	if o.FaultProxyImage == "" {
		return errors.New("no fault proxy image (images.toolproxy)")
	}
	if o.PullPolicy == "" {
		o.PullPolicy = "IfNotPresent"
	}
	image, err := PinnedBifrostImage(o.BenchRoot)
	if err != nil {
		return err
	}
	cfgJSON, err := bifrostConfig(o.BenchRoot)
	if err != nil {
		return err
	}
	sheets := map[string][]byte{}
	for _, f := range []string{"pricing.json", "model-parameters.json"} {
		b, err := gzipFile(filepath.Join(o.BenchRoot, "deploy", "bifrost", "datasheet", f))
		if err != nil {
			return fmt.Errorf("datasheet %s: %w", f, err)
		}
		sheets[f+".gz"] = b
	}
	proxyYAML, err := os.ReadFile(filepath.Join(o.BenchRoot, "deploy", "bench.yaml"))
	if err != nil {
		return fmt.Errorf("read deploy/bench.yaml for the fault proxy: %w", err)
	}
	ns := o.Namespace
	labels := map[string]string{LabelBench: "1", LabelRole: RoleGW}
	if err := c.Apply(ctx, "/api/v1/namespaces", ns, Namespace{APIVersion: "v1", Kind: "Namespace", Metadata: ObjectMeta{Name: ns, Labels: labels}}); err != nil {
		return fmt.Errorf("namespace %s: %w", ns, err)
	}
	objs := []struct {
		coll, name string
		obj        any
	}{
		{corePath(ns, "secrets"), gwSecret, Secret{APIVersion: "v1", Kind: "Secret", Type: "Opaque", Metadata: ObjectMeta{Name: gwSecret, Labels: labels},
			Data: map[string][]byte{"BIFROST_SETUP_TOKEN": []byte(o.SetupToken), "BENCH_FAULTPROXY_TOKEN": []byte(o.ControlToken)}}},
		{corePath(ns, "configmaps"), gwConfig, ConfigMap{APIVersion: "v1", Kind: "ConfigMap", Metadata: ObjectMeta{Name: gwConfig, Labels: labels},
			Data: map[string]string{"config.json": string(cfgJSON)}}},
		{corePath(ns, "configmaps"), gwDatasheet, ConfigMap{APIVersion: "v1", Kind: "ConfigMap", Metadata: ObjectMeta{Name: gwDatasheet, Labels: labels},
			BinaryData: sheets}},
		{corePath(ns, "configmaps"), gwProxyConfig, ConfigMap{APIVersion: "v1", Kind: "ConfigMap", Metadata: ObjectMeta{Name: gwProxyConfig, Labels: labels},
			Data: map[string]string{"bench.yaml": string(proxyYAML)}}},
		{deployPath(ns), gwFaultProxy, faultProxyDeployment(o, labels)},
		{deployPath(ns), gwBifrost, bifrostDeployment(image, o.PullPolicy, labels)},
		{corePath(ns, "services"), gwFaultProxy, service(gwFaultProxy, labels, []ServicePort{
			{Name: "upstream", Port: proxyPort, TargetPort: Port(proxyPort), Protocol: "TCP"},
			{Name: "control", Port: controlPort, TargetPort: Port(controlPort), NodePort: o.ControlNodePort, Protocol: "TCP"}}, o.ControlNodePort > 0)},
		{corePath(ns, "services"), gwBifrost, service(gwBifrost, labels, []ServicePort{
			{Name: "http", Port: bifrostPort, TargetPort: Port(bifrostPort), NodePort: o.BifrostNodePort, Protocol: "TCP"}}, o.BifrostNodePort > 0)},
	}
	for _, x := range objs {
		if err := c.Apply(ctx, x.coll, x.name, x.obj); err != nil {
			return fmt.Errorf("%s %s: %w", x.coll, x.name, err)
		}
	}
	for _, name := range []string{gwFaultProxy, gwBifrost} {
		if err := waitDeployment(ctx, c, ns, name); err != nil {
			return err
		}
	}
	return nil
}

// GatewayDown removes the pool namespace and everything in it.
func GatewayDown(ctx context.Context, c *Client, ns string) error {
	if err := c.Delete(ctx, nsPath(ns)); err != nil {
		return err
	}
	return c.WaitGone(ctx, nsPath(ns), nsGoneTimeout)
}

func service(name string, labels map[string]string, ports []ServicePort, nodePort bool) Service {
	s := Service{APIVersion: "v1", Kind: "Service", Metadata: ObjectMeta{Name: name, Labels: labels},
		Spec: ServiceSpec{Type: "ClusterIP", Selector: map[string]string{appNameLabel: name}, Ports: ports}}
	if nodePort {
		s.Spec.Type = "NodePort"
	}
	return s
}

func appLabels(base map[string]string, name string) map[string]string {
	out := map[string]string{appNameLabel: name}
	for k, v := range base {
		out[k] = v
	}
	return out
}

func tokenEnv(name string) EnvVar {
	return EnvVar{Name: name, ValueFrom: &EnvVarSource{SecretKeyRef: &KeySelector{Name: gwSecret, Key: name}}}
}

// faultProxyDeployment runs `bifrost-bench proxy` with no provider key.
func faultProxyDeployment(o GatewayOptions, labels map[string]string) Deployment {
	l := appLabels(labels, gwFaultProxy)
	var dep Deployment
	dep.APIVersion, dep.Kind = "apps/v1", "Deployment"
	dep.Metadata = ObjectMeta{Name: gwFaultProxy, Labels: l}
	dep.Spec.Replicas = 1
	dep.Spec.Selector = LabelSelector{MatchLabels: map[string]string{appNameLabel: gwFaultProxy}}
	dep.Spec.Template.Metadata = ObjectMeta{Labels: l}
	dep.Spec.Template.Spec = PodSpec{
		AutomountServiceAccountToken: boolPtr(false), EnableServiceLinks: boolPtr(false),
		SecurityContext: &PodSecurityContext{RunAsNonRoot: boolPtr(true), SeccompProfile: &SeccompProfile{Type: "RuntimeDefault"}},
		Containers: []Container{{Name: gwFaultProxy, Image: o.FaultProxyImage, ImagePullPolicy: o.PullPolicy,
			Args: []string{"proxy", "--config", "/etc/bench/bench.yaml"},
			Env: []EnvVar{tokenEnv("BENCH_FAULTPROXY_TOKEN"), {Name: "BENCH_FAULTPROXY_LISTEN", Value: fmt.Sprintf(":%d", proxyPort)},
				{Name: "BENCH_FAULTPROXY_CONTROL_LISTEN", Value: fmt.Sprintf("0.0.0.0:%d", controlPort)},
				{Name: "BENCH_FAULTPROXY_LEDGER", Value: "/var/lib/bench/ledger/ledger.jsonl"}},
			Ports: []ContainerPort{{Name: "upstream", ContainerPort: proxyPort}, {Name: "control", ContainerPort: controlPort}},
			SecurityContext: &SecurityContext{AllowPrivilegeEscalation: boolPtr(false), ReadOnlyRootFilesystem: boolPtr(true),
				Capabilities: &Capabilities{Drop: []string{"ALL"}}},
			VolumeMounts: []VolumeMount{{Name: "config", MountPath: "/etc/bench", ReadOnly: true}, {Name: "ledger", MountPath: "/var/lib/bench/ledger"},
				{Name: "tmp", MountPath: "/tmp"}},
			Resources: &ResourceRequirements{Limits: map[string]string{"memory": "512Mi"}}}},
		Volumes: []Volume{{Name: "config", ConfigMap: &ConfigMapVolumeSource{Name: gwProxyConfig}}, {Name: "ledger", EmptyDir: &EmptyDirVolumeSource{}},
			{Name: "tmp", EmptyDir: &EmptyDirVolumeSource{}}},
	}
	return dep
}

// bifrostDeployment runs the pinned Bifrost image. An init container lays
// the config into the writable data folder and unpacks the datasheets.
func bifrostDeployment(image, pull string, labels map[string]string) Deployment {
	l := appLabels(labels, gwBifrost)
	var dep Deployment
	dep.APIVersion, dep.Kind = "apps/v1", "Deployment"
	dep.Metadata = ObjectMeta{Name: gwBifrost, Labels: l}
	dep.Spec.Replicas = 1
	dep.Spec.Selector = LabelSelector{MatchLabels: map[string]string{appNameLabel: gwBifrost}}
	dep.Spec.Template.Metadata = ObjectMeta{Labels: l}
	sc := &SecurityContext{AllowPrivilegeEscalation: boolPtr(false), Capabilities: &Capabilities{Drop: []string{"ALL"}}}
	mounts := []VolumeMount{{Name: "data", MountPath: "/app/data"}, {Name: "datasheet", MountPath: "/app/datasheet"}}
	dep.Spec.Template.Spec = PodSpec{
		AutomountServiceAccountToken: boolPtr(false), EnableServiceLinks: boolPtr(false),
		SecurityContext: &PodSecurityContext{RunAsNonRoot: boolPtr(true), SeccompProfile: &SeccompProfile{Type: "RuntimeDefault"}},
		InitContainers: []Container{{Name: "config", Image: image, ImagePullPolicy: pull, SecurityContext: sc,
			Command:      []string{"/bin/sh", "-c", "cp /config/config.json /app/data/config.json && gunzip -c /sheets/pricing.json.gz > /app/datasheet/pricing.json && gunzip -c /sheets/model-parameters.json.gz > /app/datasheet/model-parameters.json"},
			VolumeMounts: append([]VolumeMount{{Name: "config", MountPath: "/config", ReadOnly: true}, {Name: "sheets", MountPath: "/sheets", ReadOnly: true}}, mounts...)}},
		Containers: []Container{{Name: gwBifrost, Image: image, ImagePullPolicy: pull, SecurityContext: sc,
			Env:            []EnvVar{tokenEnv("BIFROST_SETUP_TOKEN"), {Name: "LOG_STYLE", Value: "json"}},
			Ports:          []ContainerPort{{Name: "http", ContainerPort: bifrostPort}},
			ReadinessProbe: &Probe{HTTPGet: &HTTPGetAction{Path: "/health", Port: bifrostPort}, PeriodSeconds: 2, FailureThreshold: 3},
			VolumeMounts:   mounts,
			Resources:      &ResourceRequirements{Limits: map[string]string{"memory": "1Gi"}}}},
		Volumes: []Volume{{Name: "config", ConfigMap: &ConfigMapVolumeSource{Name: gwConfig}}, {Name: "sheets", ConfigMap: &ConfigMapVolumeSource{Name: gwDatasheet}},
			{Name: "data", EmptyDir: &EmptyDirVolumeSource{}}, {Name: "datasheet", EmptyDir: &EmptyDirVolumeSource{}}},
	}
	return dep
}

// waitDeployment polls until a deployment has a ready replica.
func waitDeployment(ctx context.Context, c *Client, ns, name string) error {
	deadline := time.Now().Add(gwReadyTimeout)
	for {
		var dep Deployment
		err := c.Get(ctx, deployPath(ns)+"/"+name, &dep)
		if err == nil && dep.Status.ReadyReplicas > 0 {
			return nil
		}
		if err != nil && !isTransient(err) {
			return fmt.Errorf("deployment %s/%s: %w", ns, name, err)
		}
		if time.Now().After(deadline) {
			evs, _ := c.Events(ctx, ns)
			var warn []Event
			for _, e := range evs {
				if e.Type == "Warning" {
					warn = append(warn, e)
				}
			}
			return fmt.Errorf("deployment %s/%s not ready after %s; events: %s", ns, name, gwReadyTimeout, strings.Join(eventLines(warn), "; "))
		}
		if err := sleepCtx(ctx, time.Second); err != nil {
			return err
		}
	}
}
