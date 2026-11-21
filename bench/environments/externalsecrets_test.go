package environments

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// envDocker wraps fakeDocker: on the tool proxy's create it records the
// value of name the docker CLI process sees.
func envDocker(t *testing.T, name string) (utils.Docker, string, string) {
	t.Helper()
	inner, logPath := fakeDocker(t)
	dir := t.TempDir()
	envLog := filepath.Join(dir, "env.log")
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in "create --name bench-tp-"*) printf '%%s\n' "${%[1]s:-<unset>}" >> %[2]q ;; esac
exec %[3]q "$@"
`, name, envLog, inner.Binary)
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}, logPath, envLog
}

func TestStartServicesExternalOrgSecret(t *testing.T) {
	stubEnv(t, map[string]string{"HD_EXT_WEATHER_KEY": "server-env-value"}) // never used once the run has org secrets
	if _, set := os.LookupEnv("HD_EXT_WEATHER_KEY"); set {
		t.Skip("HD_EXT_WEATHER_KEY is set in the test environment")
	}
	docker, logPath, envLog := envDocker(t, "HD_EXT_WEATHER_KEY")
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", ToolProxyImage: "tp:1"})
	rc := externalRC(t, "", schemas.ExternalLive, false)
	rc.Secrets = map[string]string{"HD_EXT_WEATHER_KEY": "org-secret-value"}
	if _, _, err := e.StartServices(t.Context(), rc); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(envLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "org-secret-value" {
		t.Fatalf("tool proxy env = %q, want the org secret", got)
	}
	all := calls(t, logPath)
	if callWith(all, "create --name bench-tp-r_1", "-e HD_EXT_WEATHER_KEY --entrypoint") == "" {
		t.Fatalf("tool proxy not created with -e HD_EXT_WEATHER_KEY:\n%s", strings.Join(all, "\n"))
	}
	for _, c := range all {
		if strings.Contains(c, "org-secret-value") || strings.Contains(c, "server-env-value") {
			t.Fatalf("credential value on a docker command line: %s", c)
		}
	}
	for _, s := range utils.RunSecrets(rc) {
		if s == "org-secret-value" {
			return // scrubbed from plan.json and attach output
		}
	}
	t.Fatal("org secret value missing from utils.RunSecrets, so it is not redacted")
}

func TestStartServicesExternalOrgSecretMissing(t *testing.T) {
	stubEnv(t, map[string]string{"HD_EXT_WEATHER_KEY": "server-env-value"}) // a hosted run never falls back to it
	docker, logPath := fakeDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", ToolProxyImage: "tp:1"})
	rc := externalRC(t, "", schemas.ExternalLive, false)
	rc.Secrets = map[string]string{"OTHER": "x"}
	_, _, err := e.StartServices(t.Context(), rc)
	var se *ServiceError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "org secret HD_EXT_WEATHER_KEY is not set") || !strings.Contains(err.Error(), "Settings > Secrets") {
		t.Fatalf("err %v", err)
	}
	if got := calls(t, logPath); len(got) != 0 {
		t.Fatalf("docker called before refusing: %v", got)
	}
}
