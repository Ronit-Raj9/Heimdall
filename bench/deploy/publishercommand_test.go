package deploy

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The runner image starts bifrost-bench itself (ENTRYPOINT in
// Dockerfile.server), so a service that keeps that entrypoint gives only the
// arguments: a command that names the program again makes it print its usage
// and exit, and the service restarts for ever. The image publisher did
// exactly that on its first deploy.
func TestPublisherCommandDoesNotRepeatTheProgram(t *testing.T) {
	b, err := os.ReadFile("hosted/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Services map[string]struct {
			Entrypoint any      `yaml:"entrypoint"`
			Command    []string `yaml:"command"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	s, ok := c.Services["image-publisher"]
	if !ok {
		t.Fatal("no image-publisher service")
	}
	if s.Entrypoint != nil {
		t.Skip("image-publisher sets its own entrypoint")
	}
	if len(s.Command) < 2 || s.Command[0] != "registry" || s.Command[1] != "publisher" {
		t.Fatalf("image-publisher command must start with the arguments \"registry publisher\", got %v", s.Command)
	}
	df, err := os.ReadFile("hosted/Dockerfile.server")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^ENTRYPOINT \["/usr/local/bin/bifrost-bench"\]`).Match(df) {
		t.Fatal("Dockerfile.server no longer starts bifrost-bench as the entrypoint: check the image-publisher command")
	}
}

// A default run of registry-sync.sh must not count an image that was never
// built on this host as a failure: there is nothing to pull for a local name.
func TestRegistrySyncSkipsNeverBuiltDefaults(t *testing.T) {
	b, err := os.ReadFile("hosted/registry-sync.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	skip := strings.Index(s, "not built on this host yet")
	pull := strings.Index(s, "is not on this host; pulling it")
	if skip < 0 || pull < 0 || skip > pull {
		t.Fatal("registry-sync.sh must skip a never-built image of the default list before it tries to pull it")
	}
	if !strings.Contains(s, `if [ -z "$named" ]; then`) {
		t.Fatal("the skip must apply only when no image was named on the command line")
	}
}
