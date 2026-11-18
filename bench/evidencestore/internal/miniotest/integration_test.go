//go:build integration

package miniotest

import (
	"net/http"
	"os/exec"
	"strings"
	"testing"
)

// TestStartAndCleanup starts a server, checks it answers on both
// endpoints, and checks the container is gone after cleanup.
func TestStartAndCleanup(t *testing.T) {
	var name string
	t.Run("start", func(t *testing.T) {
		s := Start(t)
		name = s.Container
		for _, c := range []struct {
			client *http.Client
			url    string
		}{
			{http.DefaultClient, s.Endpoint + "/minio/health/live"},
			{s.HTTPClient(), s.VirtualEndpoint() + "/minio/health/live"},
		} {
			resp, err := c.client.Get(c.url)
			if err != nil {
				t.Fatal(err)
			}
			if err := resp.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s: status %d", c.url, resp.StatusCode)
			}
		}
	})
	if name == "" {
		return
	}
	out, err := exec.Command("docker", "ps", "-a", "--filter", "name="+name, "--format", "{{.Names}}").CombinedOutput()
	if err != nil {
		t.Fatalf("docker ps: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("container %s still exists after cleanup", name)
	}
}
