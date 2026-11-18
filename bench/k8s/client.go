// Package k8s is the Kubernetes environment driver (docs/kubernetes.md):
// each run is one pod in its own namespace, reached through the Kubernetes
// REST API over net/http. There is no client-go: the package speaks only the
// handful of endpoints it needs, exec and attach through a WebSocket of its
// own (ws.go).
package k8s

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// In-cluster service account files and variables (the standard projection).
const (
	saDir          = "/var/run/secrets/kubernetes.io/serviceaccount"
	envServiceHost = "KUBERNETES_SERVICE_HOST"
	envServicePort = "KUBERNETES_SERVICE_PORT"
	requestTimeout = 60 * time.Second
	maxBodyBytes   = 32 << 20
)

// Client talks to one Kubernetes API server as one identity.
type Client struct {
	Server    string // https://host:port
	Context   string // kubeconfig context name, or "in-cluster"
	Namespace string // the context's default namespace, "" when unset
	base      *url.URL
	hc        *http.Client // ordinary requests, HTTP/2 allowed
	ws        *http.Client // WebSocket upgrades, HTTP/1.1 only
	token     string
	tokenFile string // re-read on every request (projected tokens rotate)
}

// APIError is a non-2xx answer of the API server, with the Status it sent.
type APIError struct {
	Code    int
	Reason  string
	Message string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("kubernetes API %d %s: %s", e.Code, e.Reason, e.Message)
	}
	return fmt.Sprintf("kubernetes API %d %s", e.Code, e.Reason)
}

// IsNotFound reports whether err is a 404 from the API server.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == http.StatusNotFound
}

// IsConflict reports whether err is a 409 (already exists, or a conflict).
func IsConflict(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == http.StatusConflict
}

// kubeconfig is the subset of the kubeconfig file format this package reads.
type kubeconfig struct {
	CurrentContext string `yaml:"current-context"`
	Clusters       []struct {
		Name    string `yaml:"name"`
		Cluster struct {
			Server                   string `yaml:"server"`
			CertificateAuthority     string `yaml:"certificate-authority"`
			CertificateAuthorityData string `yaml:"certificate-authority-data"`
			InsecureSkipTLSVerify    bool   `yaml:"insecure-skip-tls-verify"`
			TLSServerName            string `yaml:"tls-server-name"`
			ProxyURL                 string `yaml:"proxy-url"`
		} `yaml:"cluster"`
	} `yaml:"clusters"`
	Contexts []struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster   string `yaml:"cluster"`
			User      string `yaml:"user"`
			Namespace string `yaml:"namespace"`
		} `yaml:"context"`
	} `yaml:"contexts"`
	Users []struct {
		Name string `yaml:"name"`
		User struct {
			ClientCertificate     string         `yaml:"client-certificate"`
			ClientCertificateData string         `yaml:"client-certificate-data"`
			ClientKey             string         `yaml:"client-key"`
			ClientKeyData         string         `yaml:"client-key-data"`
			Token                 string         `yaml:"token"`
			TokenFile             string         `yaml:"tokenFile"`
			Exec                  map[string]any `yaml:"exec"`
			AuthProvider          map[string]any `yaml:"auth-provider"`
			Username              string         `yaml:"username"`
		} `yaml:"user"`
	} `yaml:"users"`
}

// KubeconfigPaths returns the kubeconfig files to read: explicit when set,
// else $KUBECONFIG (a list), else ~/.kube/config.
func KubeconfigPaths(explicit string) []string {
	if explicit != "" {
		return []string{explicit}
	}
	if v := os.Getenv("KUBECONFIG"); v != "" {
		var out []string
		for _, p := range filepath.SplitList(v) {
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".kube", "config")}
}

// Connect returns a client for the named context of the kubeconfig files
// (KubeconfigPaths), or for the in-cluster service account when the bench
// itself runs in a pod and no kubeconfig file exists. contextName "" means
// the current context.
func Connect(kubeconfigPath, contextName string) (*Client, error) {
	paths := KubeconfigPaths(kubeconfigPath)
	var existing []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			existing = append(existing, p)
		}
	}
	if len(existing) == 0 {
		if os.Getenv(envServiceHost) != "" {
			return InCluster()
		}
		return nil, fmt.Errorf("no kubeconfig found (looked at %s) and not running in a cluster", strings.Join(paths, ", "))
	}
	return FromKubeconfig(existing, contextName)
}

// FromKubeconfig merges the files like kubectl does (the first file that
// names a cluster, user or context wins; the first current-context wins)
// and builds a client for contextName, or the current context when empty.
func FromKubeconfig(paths []string, contextName string) (*Client, error) {
	var merged kubeconfig
	dirs := map[string]string{} // entry name -> folder of the file that defined it, for relative paths
	seen := map[string]bool{}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read kubeconfig %s: %w", p, err)
		}
		var kc kubeconfig
		if err := yaml.Unmarshal(raw, &kc); err != nil {
			return nil, fmt.Errorf("parse kubeconfig %s: %w", p, err)
		}
		dir := filepath.Dir(p)
		if merged.CurrentContext == "" {
			merged.CurrentContext = kc.CurrentContext
		}
		for _, c := range kc.Clusters {
			if !seen["cluster/"+c.Name] {
				seen["cluster/"+c.Name], dirs["cluster/"+c.Name] = true, dir
				merged.Clusters = append(merged.Clusters, c)
			}
		}
		for _, c := range kc.Contexts {
			if !seen["context/"+c.Name] {
				seen["context/"+c.Name] = true
				merged.Contexts = append(merged.Contexts, c)
			}
		}
		for _, u := range kc.Users {
			if !seen["user/"+u.Name] {
				seen["user/"+u.Name], dirs["user/"+u.Name] = true, dir
				merged.Users = append(merged.Users, u)
			}
		}
	}
	name := contextName
	if name == "" {
		name = merged.CurrentContext
	}
	if name == "" {
		return nil, errors.New("kubeconfig has no current-context; set kubernetes.context")
	}
	ci := -1
	for i, c := range merged.Contexts {
		if c.Name == name {
			ci = i
		}
	}
	if ci < 0 {
		return nil, fmt.Errorf("kubeconfig has no context %q", name)
	}
	ctxe := merged.Contexts[ci].Context
	cl, us := -1, -1
	for i, c := range merged.Clusters {
		if c.Name == ctxe.Cluster {
			cl = i
		}
	}
	for i, u := range merged.Users {
		if u.Name == ctxe.User {
			us = i
		}
	}
	if cl < 0 {
		return nil, fmt.Errorf("context %q names cluster %q, which the kubeconfig does not define", name, ctxe.Cluster)
	}
	cluster := merged.Clusters[cl].Cluster
	cdir := dirs["cluster/"+ctxe.Cluster]
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cluster.TLSServerName}
	if cluster.InsecureSkipTLSVerify {
		return nil, fmt.Errorf("context %q sets insecure-skip-tls-verify; the driver refuses an API server it cannot verify", name)
	}
	caPEM, err := dataOrFile(cluster.CertificateAuthorityData, cluster.CertificateAuthority, cdir)
	if err != nil {
		return nil, fmt.Errorf("context %q certificate authority: %w", name, err)
	}
	if len(caPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("context %q certificate authority holds no PEM certificate", name)
		}
		tlsCfg.RootCAs = pool
	}
	c := &Client{Server: cluster.Server, Context: name, Namespace: ctxe.Namespace}
	if us >= 0 {
		u := merged.Users[us].User
		udir := dirs["user/"+ctxe.User]
		switch {
		case len(u.Exec) > 0:
			return nil, fmt.Errorf("context %q authenticates with an exec plugin, which the driver does not run; use a client certificate, a token or a token file (managed clusters: docs/kubernetes.md)", name)
		case len(u.AuthProvider) > 0:
			return nil, fmt.Errorf("context %q uses an auth-provider, which the driver does not support; use a client certificate, a token or a token file", name)
		case u.Username != "":
			return nil, fmt.Errorf("context %q uses basic auth, which the driver does not support", name)
		}
		certPEM, err := dataOrFile(u.ClientCertificateData, u.ClientCertificate, udir)
		if err != nil {
			return nil, fmt.Errorf("context %q client certificate: %w", name, err)
		}
		keyPEM, err := dataOrFile(u.ClientKeyData, u.ClientKey, udir)
		if err != nil {
			return nil, fmt.Errorf("context %q client key: %w", name, err)
		}
		if len(certPEM) > 0 || len(keyPEM) > 0 {
			pair, err := tls.X509KeyPair(certPEM, keyPEM)
			if err != nil {
				return nil, fmt.Errorf("context %q client certificate: %w", name, err)
			}
			tlsCfg.Certificates = []tls.Certificate{pair}
		}
		c.token = u.Token
		if u.TokenFile != "" {
			c.tokenFile = resolve(u.TokenFile, udir)
		}
	}
	if err := c.init(tlsCfg, cluster.ProxyURL); err != nil {
		return nil, fmt.Errorf("context %q: %w", name, err)
	}
	return c, nil
}

// InCluster builds a client from the pod's service account.
func InCluster() (*Client, error) {
	host, port := os.Getenv(envServiceHost), os.Getenv(envServicePort)
	if host == "" || port == "" {
		return nil, errors.New("not running in a cluster: KUBERNETES_SERVICE_HOST or KUBERNETES_SERVICE_PORT is unset")
	}
	caPEM, err := os.ReadFile(filepath.Join(saDir, "ca.crt"))
	if err != nil {
		return nil, fmt.Errorf("in-cluster CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("in-cluster CA holds no PEM certificate")
	}
	c := &Client{Server: "https://" + net.JoinHostPort(host, port), Context: "in-cluster", tokenFile: filepath.Join(saDir, "token")}
	if ns, err := os.ReadFile(filepath.Join(saDir, "namespace")); err == nil {
		c.Namespace = strings.TrimSpace(string(ns))
	}
	if err := c.init(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}, ""); err != nil {
		return nil, err
	}
	return c, nil
}

// init parses the server URL and builds both HTTP clients.
func (c *Client) init(tlsCfg *tls.Config, proxyURL string) error {
	u, err := url.Parse(c.Server)
	if err != nil || u.Host == "" {
		return fmt.Errorf("server %q is not a URL", c.Server)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("server %q is not https; the driver never sends credentials in clear text", c.Server)
	}
	c.base = u
	proxy := http.ProxyFromEnvironment
	if proxyURL != "" {
		pu, err := url.Parse(proxyURL)
		if err != nil {
			return fmt.Errorf("proxy-url %q: %w", proxyURL, err)
		}
		proxy = http.ProxyURL(pu)
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	c.hc = &http.Client{Transport: &http.Transport{Proxy: proxy, DialContext: dialer.DialContext, TLSClientConfig: tlsCfg.Clone(),
		ForceAttemptHTTP2: true, MaxIdleConnsPerHost: 8, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second}}
	wsTLS := tlsCfg.Clone()
	wsTLS.NextProtos = []string{"http/1.1"}
	c.ws = &http.Client{Transport: &http.Transport{Proxy: proxy, DialContext: dialer.DialContext, TLSClientConfig: wsTLS,
		TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}, TLSHandshakeTimeout: 10 * time.Second}}
	return nil
}

// dataOrFile returns base64 data when set, else the file's content.
func dataOrFile(data, file, dir string) ([]byte, error) {
	if data != "" {
		b, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, fmt.Errorf("decode base64: %w", err)
		}
		return b, nil
	}
	if file == "" {
		return nil, nil
	}
	return os.ReadFile(resolve(file, dir))
}

func resolve(p, dir string) string {
	if filepath.IsAbs(p) || dir == "" {
		return p
	}
	return filepath.Join(dir, p)
}

// bearer is the token to send, re-read from the token file when one is set.
func (c *Client) bearer() string {
	if c.tokenFile != "" {
		if b, err := os.ReadFile(c.tokenFile); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return c.token
}

// newRequest builds a request against the API server.
func (c *Client) newRequest(ctx context.Context, method, path string, query url.Values, body io.Reader) (*http.Request, error) {
	u := *c.base
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if t := c.bearer(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "heimdall-bench-k8s")
	return req, nil
}

// Do sends one JSON request: body (when not nil) is marshalled, out (when
// not nil) receives the decoded answer. A non-2xx answer is an *APIError.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var rd io.Reader
	contentType := "application/json"
	if body != nil {
		if p, ok := body.(mergePatch); ok {
			rd, contentType = bytes.NewReader(p), "application/merge-patch+json"
		} else {
			b, err := json.Marshal(body)
			if err != nil {
				return fmt.Errorf("encode %s %s: %w", method, path, err)
			}
			rd = bytes.NewReader(b)
		}
	}
	req, err := c.newRequest(ctx, method, path, query, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return readAPIError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, path, err)
	}
	return nil
}

// mergePatch is a JSON merge patch body for Do.
type mergePatch []byte

// Raw sends a GET and returns the body, capped at limit bytes (used for
// logs, which are text).
func (c *Client) Raw(ctx context.Context, path string, query url.Values, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := c.newRequest(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "*/*")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, readAPIError(resp)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// readAPIError turns a non-2xx answer into an *APIError, with the Status
// message when the body is one.
func readAPIError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var st struct {
		Reason  string `json:"reason"`
		Message string `json:"message"`
	}
	ae := &APIError{Code: resp.StatusCode, Reason: http.StatusText(resp.StatusCode)}
	if json.Unmarshal(b, &st) == nil && (st.Message != "" || st.Reason != "") {
		if st.Reason != "" {
			ae.Reason = st.Reason
		}
		ae.Message = st.Message
	} else if s := strings.TrimSpace(string(b)); s != "" {
		ae.Message = clip(s, 300)
	}
	return ae
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
