package runnerdeploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const testToken = "hmr_abcdefgh_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func dryRunEnv(server string) []string {
	return append(os.Environ(),
		"HEIMDALL_SERVER="+server,
		"HEIMDALL_RUNNER_TOKEN="+testToken,
		"HEIMDALL_POOL=lab-box", "HEIMDALL_RUNNER_NAME=lab-box", "HEIMDALL_LABELS=platform=vm",
		"HEIMDALL_REF="+testCommit)
}

const testCommit = "0123456789abcdef0123456789abcdef01234567"

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

// writeScript writes the installer to dir with its last line (main "$@")
// replaced by tail, so a test can call its functions on their own.
func writeScript(t *testing.T, dir, tail string) string {
	t.Helper()
	src := string(InstallScript)
	if !strings.HasSuffix(src, "\nmain \"$@\"\n") {
		t.Fatal("install.sh no longer ends with main \"$@\"")
	}
	src = strings.TrimSuffix(src, "main \"$@\"\n") + tail + "\n"
	path := filepath.Join(dir, "install.sh")
	if err := os.WriteFile(path, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func dryRun(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	path := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(path, InstallScript, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", append([]string{path}, args...)...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestInstallDefaultsToServerDownloads: with no --repo the project bundle and
// the binary come from the server's /downloads at its commit, each checked
// against its .sha256, and nothing is cloned or built.
func TestInstallDefaultsToServerDownloads(t *testing.T) {
	out, _ := dryRun(t, dryRunEnv("https://heimdall.example.com"), "--dry-run", "--skip-images", "--skip-stack")
	for _, want := range []string{
		"https://heimdall.example.com/downloads/bench-" + testCommit + ".tar.gz",
		"https://heimdall.example.com/downloads/bench-" + testCommit + ".tar.gz.sha256",
		"https://heimdall.example.com/downloads/bifrost-bench-linux-",
		"sha256sum -c",
		"from server",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"git clone", "github.com", "Building bin/bifrost-bench"} {
		if strings.Contains(out, bad) {
			t.Fatalf("server install still uses %q:\n%s", bad, out)
		}
	}
}

// TestInstallGitIsOptIn: --repo keeps the clone and the source build.
func TestInstallGitIsOptIn(t *testing.T) {
	out, _ := dryRun(t, dryRunEnv("https://heimdall.example.com"), "--dry-run", "--skip-images", "--skip-stack",
		"--repo", "https://git.example.com/acme/bifrost.git")
	for _, want := range []string{"git clone --no-checkout -- https://git.example.com/acme/bifrost.git", "Building bin/bifrost-bench", "from git"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "/downloads/") {
		t.Fatalf("git install downloads from the server:\n%s", out)
	}
}

// TestInstallServerNeedsCommit: a server download needs the server's commit;
// a missing one or a branch name stops with the way out.
func TestInstallServerNeedsCommit(t *testing.T) {
	env := dryRunEnv("https://heimdall.example.com")
	env = append(env[:len(env)-1], "HEIMDALL_REF=")
	out, err := dryRun(t, env, "--dry-run")
	if err == nil || !strings.Contains(out, "the server did not say which commit it runs") || !strings.Contains(out, "--repo") {
		t.Fatalf("no commit accepted: %v\n%s", err, out)
	}
	out, err = dryRun(t, dryRunEnv("https://heimdall.example.com"), "--dry-run", "--ref", "main")
	if err == nil || !strings.Contains(out, "is not a commit") {
		t.Fatalf("branch accepted for a server download: %v\n%s", err, out)
	}
	out, err = dryRun(t, dryRunEnv("https://heimdall.example.com"), "--dry-run", "--source", "zip")
	if err == nil || !strings.Contains(out, "--source must be server or git") {
		t.Fatalf("bad source accepted: %v\n%s", err, out)
	}
}

func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// TestServerDownloadChecksSha: server_download fetches the file and its
// .sha256 from /downloads, refuses a mismatch, and names --repo when the
// server has no download; bundle_prefix finds the bench tree and refuses
// paths that leave it.
func TestServerDownloadChecksSha(t *testing.T) {
	for _, tool := range []string{"bash", "curl", "tar", "sha256sum"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	good := tgz(t, map[string]string{"bench/deploy/bench.yaml": "runs_root: runs\n"})
	flat := tgz(t, map[string]string{"deploy/bench.yaml": "runs_root: runs\n"})
	evil := tgz(t, map[string]string{"bench/deploy/bench.yaml": "x", "../escape": "x"})
	files := map[string][]byte{"bench-good.tar.gz": good, "bench-flat.tar.gz": flat, "bench-evil.tar.gz": evil, "bench-bad.tar.gz": good}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/downloads/")
		if base, ok := strings.CutSuffix(name, ".sha256"); ok {
			b, ok := files[base]
			if !ok {
				http.NotFound(w, r)
				return
			}
			sum := sha256.Sum256(b)
			if base == "bench-bad.tar.gz" {
				sum = sha256.Sum256([]byte("other"))
			}
			_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + base + "\n"))
			return
		}
		b, ok := files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	call := func(body string) (string, error) {
		dir := t.TempDir()
		path := writeScript(t, dir, "SERVER="+srv.URL+"\nREF=abc1234\n"+body)
		cmd := exec.Command("bash", path)
		cmd.Env = append(os.Environ(), "HEIMDALL_SERVER="+srv.URL)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := call(`server_download bench-good.tar.gz "$PWD/b.tgz" && printf 'prefix=[%s]\n' "$(bundle_prefix "$PWD/b.tgz")"`); err != nil || !strings.Contains(out, "prefix=[bench/]") {
		t.Fatalf("good bundle: %v\n%s", err, out)
	}
	if out, err := call(`server_download bench-flat.tar.gz "$PWD/b.tgz" && printf 'prefix=[%s]\n' "$(bundle_prefix "$PWD/b.tgz")"`); err != nil || !strings.Contains(out, "prefix=[]") {
		t.Fatalf("flat bundle: %v\n%s", err, out)
	}
	if out, err := call(`server_download bench-bad.tar.gz "$PWD/b.tgz"`); err == nil || !strings.Contains(out, "does not match") {
		t.Fatalf("bad sha accepted: %v\n%s", err, out)
	}
	if out, err := call(`server_download bench-gone.tar.gz "$PWD/b.tgz"`); err == nil || !strings.Contains(out, "the server has no download") || !strings.Contains(out, "--repo") {
		t.Fatalf("missing download: %v\n%s", err, out)
	}
	if out, err := call(`server_download bench-evil.tar.gz "$PWD/b.tgz" && bundle_prefix "$PWD/b.tgz"`); err == nil || !strings.Contains(out, ".. path") {
		t.Fatalf("escaping bundle accepted: %v\n%s", err, out)
	}
}
