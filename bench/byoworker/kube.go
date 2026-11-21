package byoworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/k8s"
)

// Your own Kubernetes cluster. The org gives the server a credential scoped
// to one namespace (an uploaded kubeconfig, or a service account token with
// the API server and its CA) and the server keeps one runner Deployment in
// that namespace. The runner pod runs as its own service account and runs
// each evaluation's runs with the existing Kubernetes environment driver
// (package k8s: one pod per run in a heimdall-run-* namespace).
//
// KubeManifest is the RBAC the org applies first: a namespaced Role for the
// credential the server holds (the runner's Deployment and token Secret,
// nothing else), the driver ClusterRole for the runner's own account, and a
// ValidatingAdmissionPolicy that lets that account change heimdall-run-*
// namespaces only.

// Kubernetes settings.
const (
	KubeNamespace = "namespace" // required: where the runner Deployment lives
	KubeContext   = "context"   // kubeconfig only: the context to use, default the current one
	KubeImage     = "image"     // runner image; default the server's runner image
	KubeConfig    = "config"    // path of the bench.yaml inside the image, default deploy/bench.yaml
	KubeCPU       = "cpu"       // runner pod CPU request, default 1
	KubeMemory    = "memory"    // runner pod memory request, default 2Gi
)

// Kubernetes names the driver and the manifest use.
const (
	KubeConnectAccount = "heimdall-connect" // the credential the server holds
	KubeRunnerAccount  = "heimdall-runner"  // the runner pod's own account
	KubeRunPrefix      = "heimdall-run-"    // the run namespaces of the k8s driver (DefaultNamespacePrefix + "-")
	kubeTokenPath      = "/var/run/heimdall/token"
	kubeManagedBy      = "heimdall"
)

// KubeToken is the service account form of the credential (stored as JSON
// in the keystore); a kubeconfig is stored as the YAML the org uploaded.
type KubeToken struct {
	Server string `json:"server"`
	CA     string `json:"certificate_authority,omitempty"` // PEM
	Token  string `json:"token"`
}

// Kubernetes is the driver for an org's own cluster.
type Kubernetes struct {
	Now func() time.Time
	// AllowPrivate lets tests reach an API server on a loopback address.
	AllowPrivate bool
}

// Platform implements Driver.
func (Kubernetes) Platform() string { return PlatformKubernetes }

// Validate implements Driver: the settings, and a credential the client
// builder accepts (inline certificates only, https, no exec plugin).
func (k Kubernetes) Validate(in map[string]string, secret string) (map[string]string, error) {
	s, err := settingsOnly(in, KubeNamespace, KubeContext, KubeImage, KubeConfig, KubeCPU, KubeMemory)
	if err != nil {
		return nil, err
	}
	if !validDNSLabel(s[KubeNamespace]) {
		return nil, fmt.Errorf("%w: namespace must be a Kubernetes namespace name (lowercase letters, digits, '-')", ErrInvalid)
	}
	if strings.HasPrefix(s[KubeNamespace], KubeRunPrefix) {
		return nil, fmt.Errorf("%w: namespace %s is a run namespace name; pick another one, such as heimdall", ErrInvalid, s[KubeNamespace])
	}
	if img := s[KubeImage]; img != "" && !validImage(img) {
		return nil, fmt.Errorf("%w: image must be an image reference", ErrInvalid)
	}
	if c := s[KubeConfig]; c != "" && (strings.Contains(c, "..") || !validPath(c)) {
		return nil, fmt.Errorf("%w: config must be a path inside the image", ErrInvalid)
	}
	if !validQuantity(s[KubeCPU]) || !validQuantity(s[KubeMemory]) {
		return nil, fmt.Errorf("%w: cpu and memory must be Kubernetes quantities such as 2 or 4Gi", ErrInvalid)
	}
	if len(secret) == 0 || len(secret) > 8<<10 {
		return nil, fmt.Errorf("%w: upload a kubeconfig or paste a service account token (at most 8 KB)", ErrInvalid)
	}
	if _, err := k.client(Target{Settings: s, Secret: secret}); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return s, nil
}

// client builds the API client of a target and refuses an API server on a
// loopback, private or link-local address: the hosted server must not be
// pointed at its own network.
func (k Kubernetes) client(t Target) (*k8s.Client, error) {
	var c *k8s.Client
	var err error
	raw := strings.TrimSpace(t.Secret)
	if strings.HasPrefix(raw, "{") {
		var tok KubeToken
		if err := json.Unmarshal([]byte(raw), &tok); err != nil {
			return nil, fmt.Errorf("service account credential: %v", err)
		}
		c, err = k8s.FromToken(strings.TrimRight(tok.Server, "/"), []byte(tok.CA), tok.Token, t.Settings[KubeNamespace])
	} else {
		c, err = k8s.FromKubeconfigData([]byte(raw), t.Settings[KubeContext])
	}
	if err != nil {
		return nil, err
	}
	if !k.AllowPrivate {
		if err := publicHost(c.Server); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (k Kubernetes) now() time.Time {
	if k.Now != nil {
		return k.Now()
	}
	return time.Now()
}

// kubeNeeds are the verbs the server's credential needs in the namespace.
var kubeNeeds = []struct{ verb, group, resource string }{
	{"create", "apps", "deployments"}, {"get", "apps", "deployments"}, {"delete", "apps", "deployments"},
	{"create", "", "secrets"}, {"delete", "", "secrets"},
	{"list", "", "pods"},
}

// Check implements Driver: the API server answers, and the credential may do
// every verb the driver uses in the namespace.
func (k Kubernetes) Check(ctx context.Context, t Target) Health {
	h := Health{Checked: k.now().UTC()}
	ns := t.Settings[KubeNamespace]
	c, err := k.client(t)
	if err != nil {
		h.Detail = "The credential cannot be used: " + err.Error()
		return h
	}
	v, err := c.ServerVersion(ctx)
	if err != nil {
		h.Detail = "The API server could not be reached: " + err.Error()
		return h
	}
	var denied []string
	for _, n := range kubeNeeds {
		ok, _, err := c.CanI(ctx, n.verb, n.group, n.resource, "", ns)
		if err != nil {
			h.Detail = "The API server did not answer the permission check: " + err.Error()
			return h
		}
		if !ok {
			denied = append(denied, n.verb+" "+n.resource)
		}
	}
	h.Capacity = fmt.Sprintf("namespace %s on %s (Kubernetes %s)", ns, c.Server, v.GitVersion)
	if len(denied) > 0 {
		h.Detail = "The credential may not " + strings.Join(denied, ", ") + " in namespace " + ns + ". Apply the manifest shown when you connected the cluster."
		return h
	}
	h.OK, h.Detail = true, "Kubernetes "+v.GitVersion+" accepted the credential for namespace "+ns+"."
	return h
}

func kubeSecretName(name string) string { return name + "-token" }

func kubeDeployPath(ns string) string {
	return "/apis/apps/v1/namespaces/" + url.PathEscape(ns) + "/deployments"
}

func kubeSecretPath(ns string) string {
	return "/api/v1/namespaces/" + url.PathEscape(ns) + "/secrets"
}

// kubeObjectName turns a runner name into a Kubernetes object name.
func kubeObjectName(name string) string {
	n := strings.ToLower(name)
	n = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, n)
	n = strings.Trim(n, "-")
	if len(n) > 50 {
		n = strings.TrimRight(n[:50], "-")
	}
	if n == "" {
		n = "heimdall-worker"
	}
	return n
}

// KubeObjects are the Secret and the Deployment Start creates; exported for
// tests and for the docs page.
func KubeObjects(t Target, b Boot) (secret, deploy map[string]any) {
	ns := t.Settings[KubeNamespace]
	name := kubeObjectName(b.Name)
	labels := map[string]string{"app.kubernetes.io/name": "heimdall-runner", "app.kubernetes.io/managed-by": kubeManagedBy, "heimdall.worker": name}
	secret = map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata":   map[string]any{"name": kubeSecretName(name), "namespace": ns, "labels": labels},
		"type":       "Opaque",
		"stringData": map[string]string{"token": b.RunnerToken},
	}
	slots := b.Slots
	if slots <= 0 {
		slots = 1
	}
	image := t.Settings[KubeImage]
	if image == "" {
		image = b.Image
	}
	args := []string{"runner", "start", "-server", strings.TrimRight(b.ServerURL, "/"), "-token-file", kubeTokenPath,
		"-pool", b.Pool, "-name", b.Name, "-slots", strconv.Itoa(slots),
		"-labels", strings.Join(append([]string{PlatformLabel(PlatformKubernetes)}, b.Labels...), ",")}
	if c := t.Settings[KubeConfig]; c != "" {
		args = append(args, "-config", c)
	}
	cpu, mem := t.Settings[KubeCPU], t.Settings[KubeMemory]
	if cpu == "" {
		cpu = "1"
	}
	if mem == "" {
		mem = "2Gi"
	}
	deploy = map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": name, "namespace": ns, "labels": labels},
		"spec": map[string]any{
			"replicas": 1,
			"strategy": map[string]any{"type": "Recreate"}, // one runner of this name at a time
			"selector": map[string]any{"matchLabels": map[string]string{"heimdall.worker": name}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec": map[string]any{
					"serviceAccountName": KubeRunnerAccount,
					"securityContext":    map[string]any{"runAsNonRoot": true, "seccompProfile": map[string]string{"type": "RuntimeDefault"}},
					"containers": []any{map[string]any{
						"name": "runner", "image": image,
						"command": []string{"bifrost-bench"},
						"args":    args,
						"env": []any{
							map[string]any{"name": "HEIMDALL_WORKER_PLATFORM", "value": PlatformKubernetes},
							map[string]any{"name": "POD_NAMESPACE", "valueFrom": map[string]any{"fieldRef": map[string]string{"fieldPath": "metadata.namespace"}}},
						},
						"resources": map[string]any{"requests": map[string]string{"cpu": cpu, "memory": mem}},
						"securityContext": map[string]any{
							"allowPrivilegeEscalation": false,
							"capabilities":             map[string]any{"drop": []string{"ALL"}},
						},
						"volumeMounts": []any{map[string]any{"name": "token", "mountPath": "/var/run/heimdall", "readOnly": true}},
					}},
					"volumes": []any{map[string]any{"name": "token", "secret": map[string]any{
						"secretName": kubeSecretName(name), "defaultMode": 0o400,
						"items": []any{map[string]string{"key": "token", "path": "token"}},
					}}},
				},
			},
		},
	}
	return secret, deploy
}

// Start implements Driver: the token Secret, then the Deployment. The
// instance id is the Deployment's name.
func (k Kubernetes) Start(ctx context.Context, t Target, b Boot) (Instance, error) {
	if err := checkBoot(b); err != nil {
		return Instance{}, err
	}
	if t.Settings[KubeImage] == "" && b.Image == "" {
		return Instance{}, fmt.Errorf("%w: set the runner image (an image with bifrost-bench and the project, see docs/kubernetes.md)", ErrInvalid)
	}
	c, err := k.client(t)
	if err != nil {
		return Instance{}, err
	}
	ns := t.Settings[KubeNamespace]
	sec, dep := KubeObjects(t, b)
	name := kubeObjectName(b.Name)
	if err := c.Apply(ctx, kubeSecretPath(ns), kubeSecretName(name), sec); err != nil {
		return Instance{}, fmt.Errorf("create the runner token Secret in %s: %w", ns, err)
	}
	if err := c.Apply(ctx, kubeDeployPath(ns), name, dep); err != nil {
		return Instance{}, fmt.Errorf("create the runner Deployment in %s: %w", ns, err)
	}
	return Instance{ID: name, State: InstanceStarting, Detail: "Deployment " + ns + "/" + name + " created"}, nil
}

// Status implements Driver: the Deployment's ready replicas.
func (k Kubernetes) Status(ctx context.Context, t Target, id string) (Instance, error) {
	c, err := k.client(t)
	if err != nil {
		return Instance{}, err
	}
	var d struct {
		Status struct {
			ReadyReplicas int `json:"readyReplicas"`
			Conditions    []struct {
				Type, Status, Reason, Message string
			} `json:"conditions"`
		} `json:"status"`
	}
	ns := t.Settings[KubeNamespace]
	if err := c.Get(ctx, kubeDeployPath(ns)+"/"+url.PathEscape(id), &d); err != nil {
		if k8s.IsNotFound(err) {
			return Instance{ID: id, State: InstanceStopped, Detail: "the Deployment no longer exists"}, nil
		}
		return Instance{}, fmt.Errorf("read the runner Deployment: %w", err)
	}
	if d.Status.ReadyReplicas > 0 {
		return Instance{ID: id, State: InstanceRunning, Detail: "runner pod ready"}, nil
	}
	for _, cond := range d.Status.Conditions {
		if cond.Type == "Progressing" && cond.Status == "False" {
			return Instance{ID: id, State: InstanceFailed, Detail: strings.TrimSpace(cond.Reason + ": " + cond.Message)}, nil
		}
		if cond.Type == "ReplicaFailure" && cond.Status == "True" {
			return Instance{ID: id, State: InstanceFailed, Detail: strings.TrimSpace(cond.Reason + ": " + cond.Message)}, nil
		}
	}
	return Instance{ID: id, State: InstanceStarting, Detail: "runner pod not ready yet"}, nil
}

// Stop implements Driver: deletes the Deployment and its token Secret.
func (k Kubernetes) Stop(ctx context.Context, t Target, id string) error {
	c, err := k.client(t)
	if err != nil {
		return err
	}
	ns := t.Settings[KubeNamespace]
	if err := c.Delete(ctx, kubeDeployPath(ns)+"/"+url.PathEscape(id)); err != nil {
		return fmt.Errorf("delete the runner Deployment: %w", err)
	}
	if err := c.Delete(ctx, kubeSecretPath(ns)+"/"+url.PathEscape(kubeSecretName(id))); err != nil {
		return fmt.Errorf("delete the runner token Secret: %w", err)
	}
	return nil
}

// KubeManifest is the RBAC an org applies before connecting a cluster
// (kubectl apply -f), for namespace ns.
func KubeManifest(ns string) string {
	r := strings.NewReplacer("{{NS}}", ns)
	return r.Replace(kubeManifestTemplate)
}

const kubeManifestTemplate = `# Heimdall worker in namespace {{NS}}: kubectl apply -f this file.
#
# 1. heimdall-connect is what you give Heimdall (its token, below). It may
#    manage the runner's Deployment and token Secret in {{NS}}, nothing else.
# 2. heimdall-runner is the runner pod's own account. It runs each run as one
#    pod in its own heimdall-run-* namespace (the Kubernetes driver), and the
#    admission policy at the end refuses any change it asks for outside the
#    heimdall-run-* namespaces. Reads are not covered by admission: the
#    ClusterRole still lets it read pods, secrets and config maps it lists,
#    so use a cluster (or a vcluster) that holds nothing else sensitive.
apiVersion: v1
kind: Namespace
metadata:
  name: {{NS}}
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: heimdall-connect
  namespace: {{NS}}
---
apiVersion: v1
kind: Secret
metadata:
  name: heimdall-connect-token
  namespace: {{NS}}
  annotations:
    kubernetes.io/service-account.name: heimdall-connect
type: kubernetes.io/service-account-token
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: heimdall-connect
  namespace: {{NS}}
rules:
  - apiGroups: [apps]
    resources: [deployments]
    verbs: [create, get, list, patch, delete]
  - apiGroups: [""]
    resources: [secrets]
    verbs: [create, get, patch, delete]
  - apiGroups: [""]
    resources: [pods]
    verbs: [get, list]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: heimdall-connect
  namespace: {{NS}}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: heimdall-connect
subjects:
  - kind: ServiceAccount
    name: heimdall-connect
    namespace: {{NS}}
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: heimdall-runner
  namespace: {{NS}}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: heimdall-runner-{{NS}}
rules:
  - apiGroups: [""]
    resources: [namespaces]
    verbs: [create, get, list, delete]
  - apiGroups: [""]
    resources: [pods, secrets, configmaps, serviceaccounts]
    verbs: [create, get, list, delete, patch]
  - apiGroups: [""]
    resources: [pods/exec, pods/attach]
    verbs: [create, get]
  - apiGroups: [""]
    resources: [pods/log, events]
    verbs: [get, list]
  - apiGroups: [""]
    resources: [services, nodes]
    verbs: [get, list]
  - apiGroups: [networking.k8s.io]
    resources: [networkpolicies]
    verbs: [create, get, list, delete]
  - apiGroups: [authorization.k8s.io]
    resources: [selfsubjectaccessreviews]
    verbs: [create]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: heimdall-runner-{{NS}}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: heimdall-runner-{{NS}}
subjects:
  - kind: ServiceAccount
    name: heimdall-runner
    namespace: {{NS}}
---
# Kubernetes 1.30 or newer.
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: heimdall-runner-{{NS}}
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
      - apiGroups: ["*"]
        apiVersions: ["*"]
        operations: [CREATE, UPDATE, DELETE, CONNECT]
        resources: ["*", "*/*"]
  matchConditions:
    - name: runner-only
      expression: "request.userInfo.username == 'system:serviceaccount:{{NS}}:heimdall-runner'"
  validations:
    - expression: "request.namespace.startsWith('heimdall-run-') || (request.resource.resource == 'namespaces' && request.name.startsWith('heimdall-run-')) || request.resource.resource == 'selfsubjectaccessreviews'"
      message: "the Heimdall runner may only change heimdall-run-* namespaces"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: heimdall-runner-{{NS}}
spec:
  policyName: heimdall-runner-{{NS}}
  validationActions: [Deny]
---
# Then give Heimdall the token:
#   kubectl -n {{NS}} get secret heimdall-connect-token -o jsonpath='{.data.token}' | base64 -d
# with the API server address and its CA:
#   kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.server}'
#   kubectl -n {{NS}} get secret heimdall-connect-token -o jsonpath='{.data.ca\.crt}' | base64 -d
`

// publicHost refuses an API server whose host is a loopback, private,
// link-local or unspecified address, or localhost.
func publicHost(server string) error {
	u, err := url.Parse(server)
	if err != nil {
		return fmt.Errorf("server %q is not a URL", server)
	}
	h := u.Hostname()
	if strings.EqualFold(h, "localhost") || strings.HasSuffix(strings.ToLower(h), ".localhost") {
		return errors.New("the API server must be reachable on a public address, not localhost")
	}
	if ip := net.ParseIP(h); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()) {
		return fmt.Errorf("the API server address %s is not public; Heimdall reaches your cluster over the internet", h)
	}
	return nil
}

func validDNSLabel(s string) bool {
	if s == "" || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func validPath(s string) bool {
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c == '/') {
			return false
		}
	}
	return s != ""
}

// validQuantity accepts "" or a simple Kubernetes quantity (2, 500m, 4Gi).
func validQuantity(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > 16 {
		return false
	}
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	if i == 0 {
		return false
	}
	switch s[i:] {
	case "", "m", "k", "M", "G", "T", "Ki", "Mi", "Gi", "Ti":
		return true
	}
	return false
}
