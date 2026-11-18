// Package miniotest starts a throwaway MinIO server in Docker for
// integration tests of the S3 mirror. It never pulls an image: it uses
// HEIMDALL_TEST_MINIO_IMAGE, else minio/minio when present locally, else
// bitnamilegacy/minio:latest (MinIO stopped publishing minio/minio), and
// skips the test when Docker or the image is missing. Every container
// carries the bifrost-bench=1 label and is removed in t.Cleanup.
package miniotest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// ImageEnv names the environment variable that overrides the image.
const ImageEnv = "HEIMDALL_TEST_MINIO_IMAGE"

const (
	upstreamImage = "minio/minio"
	bitnamiImage  = "bitnamilegacy/minio:latest"
	// Domain is the MINIO_DOMAIN the server is started with, so
	// virtual-host requests (bucket.<Domain>) work; it is never resolved,
	// HTTPClient dials it to the mapped port.
	Domain        = "minio.hd-ev.test"
	region        = "us-east-1"
	startTimeout  = 90 * time.Second
	dockerTimeout = 60 * time.Second
)

// Server is a running MinIO container.
type Server struct {
	Endpoint  string // path-style endpoint, http://127.0.0.1:<port>
	AccessKey string
	SecretKey string
	Region    string
	Domain    string // virtual-host domain; use VirtualEndpoint with HTTPClient
	Port      string // host port mapped to the container's 9000
	Container string // container name, for logs
}

// VirtualEndpoint is http://<Domain>:<port>. Only HTTPClient can reach it.
func (s Server) VirtualEndpoint() string {
	return "http://" + s.Domain + ":" + s.Port
}

// HTTPClient returns a client that dials Domain and every subdomain of it
// (bucket.<Domain>) to 127.0.0.1:<Port>, and every other address normally,
// so virtual-host style works without DNS.
func (s Server) HTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext:     NewHostDialer(s.Domain, "127.0.0.1:"+s.Port),
		IdleConnTimeout: 30 * time.Second,
	}}
}

// NewHostDialer returns a DialContext that sends connections for domain and
// any subdomain of it, on any port, to target; other addresses dial as
// usual.
func NewHostDialer(domain, target string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second}
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return d.DialContext(ctx, network, target)
		}
		return d.DialContext(ctx, network, addr)
	}
}

// Start runs MinIO and waits until it reports live. It skips t when the
// docker CLI, the daemon or a local image is missing, and fails t when a
// present image does not start.
func Start(t testing.TB) Server {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("miniotest: docker CLI not found")
	}
	if _, err := docker("info", "--format", "{{.ServerVersion}}"); err != nil {
		t.Skipf("miniotest: docker daemon unavailable: %v", err)
	}
	image, err := pickImage()
	if err != nil {
		t.Skipf("miniotest: %v", err)
	}
	s := Server{
		AccessKey: "hdev" + randHex(t, 6),
		SecretKey: randHex(t, 20),
		Region:    region,
		Domain:    Domain,
		Container: "hd-ev-minio-" + randHex(t, 6),
	}
	args := []string{"run", "-d", "--name", s.Container, "--pull", "never",
		"--label", "bifrost-bench=1", "--label", "bifrost-bench.component=miniotest",
		"-p", "127.0.0.1::9000",
		"-e", "MINIO_ROOT_USER=" + s.AccessKey,
		"-e", "MINIO_ROOT_PASSWORD=" + s.SecretKey,
		"-e", "MINIO_DOMAIN=" + s.Domain,
	}
	if strings.Contains(image, "bitnami") {
		args = append(args, "--entrypoint", "/opt/bitnami/minio/bin/minio", image, "server", "/tmp/data", "--address", ":9000")
	} else {
		args = append(args, image, "server", "/data", "--address", ":9000")
	}
	t.Cleanup(func() {
		if out, err := docker("rm", "-f", "-v", s.Container); err != nil && !strings.Contains(out, "No such container") {
			t.Errorf("miniotest: remove %s: %v: %s", s.Container, err, out)
		}
	})
	if out, err := docker(args...); err != nil {
		t.Fatalf("miniotest: docker run %s: %v: %s", image, err, out)
	}
	out, err := docker("port", s.Container, "9000/tcp")
	if err != nil {
		t.Fatalf("miniotest: docker port: %v: %s", err, out)
	}
	s.Port, err = parsePort(out)
	if err != nil {
		t.Fatalf("miniotest: %v", err)
	}
	s.Endpoint = "http://127.0.0.1:" + s.Port
	if err := waitLive(s.Endpoint); err != nil {
		logs, lerr := docker("logs", "--tail", "50", s.Container)
		t.Fatalf("miniotest: %s not live: %v\nlogs (err %v):\n%s", s.Container, err, lerr, logs)
	}
	return s
}

// pickImage returns the image to run without pulling: the env override,
// else minio/minio, else bitnamilegacy/minio:latest, whichever is local.
func pickImage() (string, error) {
	if img := os.Getenv(ImageEnv); img != "" {
		if !imagePresent(img) {
			return "", fmt.Errorf("image %s (from %s) not present locally", img, ImageEnv)
		}
		return img, nil
	}
	for _, img := range []string{upstreamImage, bitnamiImage} {
		if imagePresent(img) {
			return img, nil
		}
	}
	return "", fmt.Errorf("no MinIO image present locally (%s or %s); set %s", upstreamImage, bitnamiImage, ImageEnv)
}

func imagePresent(img string) bool {
	_, err := docker("image", "inspect", "--format", "{{.Id}}", img)
	return err == nil
}

// parsePort reads the host port from `docker port` output such as
// "127.0.0.1:49153", possibly one line per address family.
func parsePort(out string) (string, error) {
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		_, port, err := net.SplitHostPort(strings.TrimSpace(line))
		if err == nil && port != "" && port != "0" {
			return port, nil
		}
	}
	return "", fmt.Errorf("no host port in docker port output %q", out)
}

// waitLive polls MinIO's liveness endpoint until it answers 200.
func waitLive(endpoint string) error {
	deadline := time.Now().Add(startTimeout)
	client := &http.Client{Timeout: 2 * time.Second}
	var last error
	for time.Now().Before(deadline) {
		resp, err := client.Get(endpoint + "/minio/health/live")
		if err == nil {
			cerr := resp.Body.Close()
			if resp.StatusCode == http.StatusOK && cerr == nil {
				return nil
			}
			last = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			last = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	if last == nil {
		last = errors.New("timed out")
	}
	return last
}

func docker(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func randHex(t testing.TB, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("miniotest: random: %v", err)
	}
	return hex.EncodeToString(b)
}
