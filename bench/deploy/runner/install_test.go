package runnerdeploy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const testToken = "hmr_abcdefgh_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func dryRunEnv(server string) []string {
	return append(os.Environ(),
		"HEIMDALL_SERVER="+server,
		"HEIMDALL_RUNNER_TOKEN="+testToken,
		"HEIMDALL_POOL=lab-box", "HEIMDALL_RUNNER_NAME=lab-box", "HEIMDALL_LABELS=platform=vm")
}

// TestOneLineInstallPipedToSh: `curl .../install.sh | sh` downloads the
// script again, runs it with bash, takes the token and the runner settings
// from the environment, and never prints the token. --foreground ends with
// the runner itself instead of a systemd unit.
func TestOneLineInstallPipedToSh(t *testing.T) {
	for _, tool := range []string{"sh", "bash", "curl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	var fetched int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/bench/runners/install.sh" {
			http.NotFound(w, r)
			return
		}
		fetched++
		_, _ = w.Write(InstallScript)
	}))
	defer srv.Close()
	cmd := exec.Command("sh", "-s", "--", "--dry-run", "--foreground", "--skip-images", "--skip-stack")
	cmd.Stdin = bytes.NewReader(InstallScript)
	cmd.Env = dryRunEnv(srv.URL)
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("piped install: %v\n%s", err, out)
	}
	s := string(out)
	if fetched != 1 {
		t.Fatalf("fetched the script %d times", fetched)
	}
	for _, want := range []string{"Disk:", "Starting the runner in the foreground (lab-box, pool lab-box)", "runner start -server " + srv.URL, "-labels platform=vm"} {
		if !strings.Contains(s, want) && !(want == "Disk:" && strings.Contains(s, "free at")) {
			t.Fatalf("output lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, testToken) || strings.Contains(s, "systemd unit") {
		t.Fatalf("token printed or unit written:\n%s", s)
	}
}

// TestInstallWithoutForegroundWritesUnit: the plain install keeps its
// systemd unit, and refuses a disk floor that is not a number.
func TestInstallWithoutForegroundWritesUnit(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	dir := t.TempDir()
	path := dir + "/install.sh"
	if err := os.WriteFile(path, InstallScript, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", path, "--dry-run", "--skip-images", "--skip-stack")
	cmd.Env = dryRunEnv("https://heimdall.example.com")
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(out), "runner status") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "systemd unit /etc/systemd/system/heimdall-runner.service") {
		t.Fatalf("no unit:\n%s", out)
	}
	bad := exec.Command("bash", path, "--dry-run", "--min-disk-gb", "lots")
	bad.Env = dryRunEnv("https://heimdall.example.com")
	if out, err := bad.CombinedOutput(); err == nil || !strings.Contains(string(out), "--min-disk-gb") {
		t.Fatalf("bad disk floor accepted: %v\n%s", err, out)
	}
}
