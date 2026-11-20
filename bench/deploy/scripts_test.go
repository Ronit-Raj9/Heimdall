// Package deploy holds no Go code; its tests run the operator scripts and
// the Makefile with a stub docker that only records what it is asked to do,
// so no container, volume or network is ever touched.
package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubDocker writes a docker (and curl, jq, openssl) stub into a folder
// and returns that folder and the log of docker invocations. The docker
// stub prints a container id for `ps -aq` when existing is true, a fake
// dump for pg_dump, archive bytes for tar -cf and an evidence line for help.
func stubDocker(t *testing.T, existing bool) (bin, logPath string) {
	t.Helper()
	bin = t.TempDir()
	logPath = filepath.Join(bin, "docker.log")
	ps := ""
	if existing {
		ps = "echo 0123456789ab"
	}
	script := `#!/usr/bin/env bash
printf '%s\n' "$*" >>` + logPath + `
case " $* " in
*" ps -aq "*) ` + ps + ` ;;
*" network inspect "*) exit 1 ;;
*" pg_dump "*) echo dump ;;
*" tar "*"-cf"*) echo archive ;;
*" help"*) printf '  evidence   mirror and restore\n' ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"curl", "jq", "openssl"} {
		if err := os.WriteFile(filepath.Join(bin, c), []byte("#!/bin/sh\necho 00\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin, logPath
}

func runScript(t *testing.T, bin string, env []string, script string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Env = append(append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH")), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestMakeDownKeepsVolumes is audit 5-A-02: `make down` is the documented
// stop and must keep the named volumes; deleting them is clean-volumes.
func TestMakeDownKeepsVolumes(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	dry := func(target string) string {
		out, err := exec.Command("make", "-s", "-n", "-C", "..", target).CombinedOutput()
		if err != nil {
			t.Fatalf("make -n %s: %v\n%s", target, err, out)
		}
		return string(out)
	}
	if out := dry("down"); !strings.Contains(out, " down") || strings.Contains(out, "-v") || strings.Contains(out, "--volumes") {
		t.Fatalf("make down = %q, want a stop that keeps volumes", out)
	}
	if out := dry("clean-volumes"); !strings.Contains(out, " down -v") {
		t.Fatalf("make clean-volumes = %q, want down -v", out)
	}
}

// TestSmokeRefusesExistingProject is audit 4-D-07: smoke.sh must not arm its
// volume-deleting teardown for a compose project that already exists.
func TestSmokeRefusesExistingProject(t *testing.T) {
	bin, logPath := stubDocker(t, true)
	out, err := runScript(t, bin, []string{"SMOKE_PROJECT=fix-c6-retained", "SMOKE_HTTPS_PORT=47991", "SMOKE_HTTP_PORT=47992"}, "hosted/smoke.sh", "--no-build")
	if err == nil || !strings.Contains(out, "already has containers") {
		t.Fatalf("smoke.sh on an existing project: err %v\n%s", err, out)
	}
	calls, _ := os.ReadFile(logPath)
	if strings.Contains(string(calls), "down") {
		t.Fatalf("smoke.sh tore down an existing project:\n%s", calls)
	}
}

// TestSmokeDefaultProjectIsUnique: two invocations without SMOKE_PROJECT
// never share a project name (audit 4-D-07).
func TestSmokeDefaultProjectIsUnique(t *testing.T) {
	b, err := os.ReadFile("hosted/smoke.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "HEIMDALL_PROJECT=${SMOKE_PROJECT:-heimdall-smoke-$(date -u +%Y%m%d%H%M%S)-$$}") {
		t.Fatal("smoke.sh default project name is not unique per invocation")
	}
}

func backupEnv(t *testing.T, extra string) string {
	t.Helper()
	env := filepath.Join(t.TempDir(), "hosted.env")
	if err := os.WriteFile(env, []byte("HEIMDALL_DOMAIN=fix-c6.test\nHEIMDALL_PG_PASSWORD=00\n"+extra), 0o600); err != nil {
		t.Fatal(err)
	}
	return env
}

// TestBackupS3ArchivesProjects is audit 4-D-01: with the S3 mirror the
// backup must still capture runs/projects and releases, which the mirror
// does not hold.
func TestBackupS3ArchivesProjects(t *testing.T) {
	bin, logPath := stubDocker(t, false)
	dir := t.TempDir()
	env := backupEnv(t, "HEIMDALL_S3_BUCKET=fix-c6-bucket\n")
	out, err := runScript(t, bin, []string{"HEIMDALL_ENV_FILE=" + env, "HEIMDALL_PROJECT=fix-c6-backup"}, "hosted/backup.sh", "--dir", dir)
	if err != nil {
		t.Fatalf("backup.sh: %v\n%s", err, out)
	}
	calls, _ := os.ReadFile(logPath)
	if !strings.Contains(string(calls), "evidence backup") {
		t.Fatalf("no evidence backup:\n%s", calls)
	}
	if !strings.Contains(string(calls), "-cf - runs/projects releases") {
		t.Fatalf("project folders not archived:\n%s", calls)
	}
	m, _ := filepath.Glob(filepath.Join(dir, "heimdall-projects-*.tar.gz"))
	if len(m) != 1 {
		t.Fatalf("projects archive = %v", m)
	}
}

// TestBackupArchivePausesServer is audit 4-D-02: the volume archive is
// taken with the server paused, and the server is unpaused afterwards.
func TestBackupArchivePausesServer(t *testing.T) {
	bin, logPath := stubDocker(t, false)
	dir := t.TempDir()
	env := backupEnv(t, "")
	out, err := runScript(t, bin, []string{"HEIMDALL_ENV_FILE=" + env, "HEIMDALL_PROJECT=fix-c6-backup"}, "hosted/backup.sh", "--dir", dir)
	if err != nil {
		t.Fatalf("backup.sh: %v\n%s", err, out)
	}
	b, _ := os.ReadFile(logPath)
	calls := string(b)
	pause, tar, unpause := strings.Index(calls, " pause heimdall"), strings.Index(calls, "-cf - runs releases"), strings.Index(calls, " unpause heimdall")
	if pause < 0 || tar < 0 || unpause < 0 || !(pause < tar && tar < unpause) {
		t.Fatalf("want pause, tar, unpause in order:\n%s", calls)
	}
	if strings.Contains(calls, "exec -T heimdall tar") {
		t.Fatalf("archive read from the live server container:\n%s", calls)
	}
	m, _ := filepath.Glob(filepath.Join(dir, "heimdall-evidence-*.tar.gz"))
	if len(m) != 1 {
		t.Fatalf("evidence archive = %v", m)
	}
}
