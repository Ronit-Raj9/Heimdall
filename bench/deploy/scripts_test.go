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
// Environment knobs let a test fail single steps: STUB_LS_RC (inventory ls
// commands), STUB_PAUSE_RC, STUB_PAUSED (server already paused),
// STUB_EVIDENCE_RC and STUB_SEAL_HALF (the seal check reports a
// half-published seal that many times before passing).
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
*" ps -aq "*) [ -z "${STUB_LS_RC:-}" ] || exit "$STUB_LS_RC"; ` + ps + ` ;;
*" ls -q "*) [ -z "${STUB_LS_RC:-}" ] || exit "$STUB_LS_RC" ;;
*" --status paused "*) [ -z "${STUB_PAUSED:-}" ] || echo 0123456789ab ;;
*" unpause heimdall "*) ;;
*" pause heimdall "*) exit "${STUB_PAUSE_RC:-0}" ;;
*" evidence backup "*) exit "${STUB_EVIDENCE_RC:-0}" ;;
*"seals.log"*) n=$(cat ` + logPath + `.seal 2>/dev/null || echo 0); if [ "$n" -lt "${STUB_SEAL_HALF:-0}" ]; then echo $((n + 1)) >` + logPath + `.seal; exit 75; fi ;;
*" network inspect "*) exit 1 ;;
*" ps -q "*) [ -z "${STUB_RUNNING:-}" ] || echo 0123456789ab ;;
*"inspect --format "*) echo sha256:feedfacecafe ;;
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
	if !strings.Contains(string(calls), "sh runs/projects releases") {
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
	pause, tar, unpause := strings.Index(calls, " pause heimdall"), strings.Index(calls, "sh runs releases"), strings.Index(calls, " unpause heimdall")
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

func runBackup(t *testing.T, extraEnv string, knobs ...string) (dir, calls, out string, err error) {
	t.Helper()
	bin, logPath := stubDocker(t, false)
	dir = t.TempDir()
	env := backupEnv(t, extraEnv)
	out, err = runScript(t, bin, append([]string{"HEIMDALL_ENV_FILE=" + env, "HEIMDALL_PROJECT=qc2-backup", "HEIMDALL_BACKUP_RETRY_SLEEP=0"}, knobs...), "hosted/backup.sh", "--dir", dir)
	b, _ := os.ReadFile(logPath)
	return dir, string(b), out, err
}

// TestBackupRefusedPauseNeverUnpauses is audit qc2-19-05: a pause the backup
// did not acquire is never undone by it, and an already paused server is
// left alone.
func TestBackupRefusedPauseNeverUnpauses(t *testing.T) {
	_, calls, out, err := runBackup(t, "", "STUB_PAUSE_RC=17")
	if err == nil || strings.Contains(calls, " unpause heimdall") {
		t.Fatalf("refused pause: err %v, calls:\n%s\n%s", err, calls, out)
	}
	_, calls, out, err = runBackup(t, "", "STUB_PAUSED=1")
	if err == nil || !strings.Contains(out, "already paused") || strings.Contains(calls, "pause heimdall") {
		t.Fatalf("already paused: err %v, calls:\n%s\n%s", err, calls, out)
	}
}

// TestBackupRetriesHalfPublishedSeal is audit qc2-19-01: a pause that froze
// a seal between seal.json and seals.log is released and retried, and the
// archive is only taken once the check passes.
func TestBackupRetriesHalfPublishedSeal(t *testing.T) {
	dir, calls, out, err := runBackup(t, "", "STUB_SEAL_HALF=2")
	if err != nil {
		t.Fatalf("backup.sh: %v\n%s", err, out)
	}
	if n := strings.Count(calls, " pause heimdall"); n != 3 {
		t.Fatalf("pauses = %d, want 3 (two retries):\n%s", n, calls)
	}
	if strings.LastIndex(calls, "seals.log") > strings.Index(calls, "sh runs releases") {
		t.Fatalf("archive taken before a passing seal check:\n%s", calls)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "heimdall-evidence-*.tar.gz")); len(m) != 1 {
		t.Fatalf("evidence archive = %v", m)
	}
	_, _, out, err = runBackup(t, "", "STUB_SEAL_HALF=99")
	if err == nil || !strings.Contains(out, "half-published") {
		t.Fatalf("persistent half seal: err %v\n%s", err, out)
	}
}

// TestBackupMirrorFailureStillArchivesProjects is audit qc2-19-03: a failed
// S3 sweep still archives the project folders, then fails the backup.
func TestBackupMirrorFailureStillArchivesProjects(t *testing.T) {
	dir, calls, out, err := runBackup(t, "HEIMDALL_S3_BUCKET=qc2-bucket\n", "STUB_EVIDENCE_RC=4")
	if err == nil || !strings.Contains(out, "backup incomplete") {
		t.Fatalf("mirror failure: err %v\n%s", err, out)
	}
	if !strings.Contains(calls, "sh runs/projects releases") {
		t.Fatalf("project folders not archived:\n%s", calls)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "heimdall-projects-*.tar.gz")); len(m) != 1 {
		t.Fatalf("projects archive = %v", m)
	}
}

// TestBackupArchivesMissingRoots is audit qc2-19-07: a root that does not
// exist yet (no organization, so no runs/projects) is created empty in the
// archive container instead of failing tar.
func TestBackupArchivesMissingRoots(t *testing.T) {
	b, err := os.ReadFile("hosted/backup.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `[ -e "$p" ] || mkdir -p "$p"`) {
		t.Fatal("backup.sh tars archive roots without creating missing ones")
	}
	root := t.TempDir()
	cmd := exec.Command("sh", "-c", `cd "$1" && shift && for p in "$@"; do [ -e "$p" ] || mkdir -p "$p"; done && tar -cf - "$@" >/dev/null`, "sh", root, "runs/projects", "releases")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tar of missing roots: %v\n%s", err, out)
	}
}

// TestSmokeInventoryFailureRefuses is audit qc2-19-04: a Docker inventory
// error never counts as an empty project, and an explicit name is claimed
// so a second smoke run on it is refused.
func TestSmokeInventoryFailureRefuses(t *testing.T) {
	bin, logPath := stubDocker(t, false)
	tmp := t.TempDir()
	env := []string{"TMPDIR=" + tmp, "SMOKE_PROJECT=qc2-inventory", "SMOKE_HTTPS_PORT=47993", "SMOKE_HTTP_PORT=47994"}
	out, err := runScript(t, bin, append(env, "STUB_LS_RC=1"), "hosted/smoke.sh", "--no-build")
	if err == nil || !strings.Contains(out, "inventory failed") {
		t.Fatalf("inventory failure: err %v\n%s", err, out)
	}
	calls, _ := os.ReadFile(logPath)
	if strings.Contains(string(calls), " down") || strings.Contains(string(calls), " up ") {
		t.Fatalf("smoke.sh went on after a failed inventory:\n%s", calls)
	}
	if err := os.Mkdir(filepath.Join(tmp, "heimdall-smoke-qc2-inventory.lock"), 0o700); err != nil {
		t.Fatalf("claim not released on exit: %v", err)
	}
	out, err = runScript(t, bin, env, "hosted/smoke.sh", "--no-build")
	if err == nil || !strings.Contains(out, "claimed by another smoke run") {
		t.Fatalf("claimed project: err %v\n%s", err, out)
	}
}

// TestSmokeOverridesCallerImage is audit qc2-19-06: smoke exports its own
// image tags, so a caller's exported production tag is never rebuilt, and a
// smoke tag naming a production image is refused.
func TestSmokeOverridesCallerImage(t *testing.T) {
	bin, _ := stubDocker(t, false)
	env := []string{"TMPDIR=" + t.TempDir(), "SMOKE_PROJECT=qc2-image", "SMOKE_HTTPS_PORT=47995", "SMOKE_HTTP_PORT=47996", "HEIMDALL_IMAGE=heimdall/server:prod"}
	out, err := runScript(t, bin, append(env, "SMOKE_IMAGE=heimdall/server:prod"), "hosted/smoke.sh", "--no-build")
	if err == nil || !strings.Contains(out, "production image tag") {
		t.Fatalf("production smoke tag: err %v\n%s", err, out)
	}
	b, err := os.ReadFile("hosted/smoke.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "export HEIMDALL_IMAGE=$smoke_image HEIMDALL_RUNNER_IMAGE=$smoke_runner_image") {
		t.Fatal("smoke.sh does not export its image tags over the caller's")
	}
}

// TestUpgradeRollbackTagPerStack is live-test l13: upgrade.sh tagged the
// running image repo:previous whatever HEIMDALL_IMAGE said, so upgrading a
// second stack on the same host (its own project and image tag) moved the
// first stack's rollback image. The default :local tag keeps :previous; any
// other tag gets :<tag>-previous.
func TestUpgradeRollbackTagPerStack(t *testing.T) {
	cases := []struct {
		image, want string
	}{
		{"", "tag sha256:feedfacecafe heimdall/server:previous"},
		{"heimdall/server:local", "tag sha256:feedfacecafe heimdall/server:previous"},
		{"heimdall/server:hb-ops", "tag sha256:feedfacecafe heimdall/server:hb-ops-previous"},
		{"registry.example:5000/heimdall/server", "tag sha256:feedfacecafe registry.example:5000/heimdall/server:previous"},
	}
	for _, tc := range cases {
		t.Run(tc.image, func(t *testing.T) {
			bin, logPath := stubDocker(t, false)
			env := []string{"HEIMDALL_ENV_FILE=" + backupEnv(t, ""), "HEIMDALL_PROJECT=l13-upgrade", "STUB_RUNNING=1", "HEIMDALL_IMAGE=" + tc.image}
			out, err := runScript(t, bin, env, "hosted/upgrade.sh", "--no-pull", "--no-backup")
			if err != nil {
				t.Fatalf("upgrade.sh: %v\n%s", err, out)
			}
			b, _ := os.ReadFile(logPath)
			if !strings.Contains(string(b), tc.want+"\n") {
				t.Fatalf("want %q in docker calls:\n%s", tc.want, b)
			}
		})
	}
}
