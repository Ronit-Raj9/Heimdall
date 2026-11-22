package environments

import (
	"archive/tar"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// gvisorCpMissing is what the daemon answers to docker cp of a file written
// inside a gVisor sandbox.
const gvisorCpMissing = "Error response from daemon: Could not find the file /state/run.json in container x"

// fakeCopyDocker writes a stand-in docker binary for copies. `docker cp` to
// stdout prints cpTar (a file path) or, when cpTar is "", fails with cpErr;
// `docker cp` to a host path copies cpFile there or fails with cpErr;
// `docker exec ... tar` prints execTar or, when execTar is "", fails with
// execErr. Every call is logged on one line.
func fakeCopyDocker(t *testing.T, cpTar, cpFile, cpErr, execTar, execErr string) (utils.Docker, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %[1]q
case "$1" in
  cp)
    if [ "$3" = "-" ]; then
      if [ -n %[2]q ]; then cat %[2]q; exit 0; fi
    else
      if [ -n %[3]q ]; then cp %[3]q "$3"; exit 0; fi
    fi
    echo %[4]q >&2; exit 1 ;;
  exec)
    case "$*" in
      *" tar "*) if [ -n %[5]q ]; then cat %[5]q; exit 0; fi; echo %[6]q >&2; exit 1 ;;
    esac ;;
esac
exit 0
`, logPath, cpTar, cpFile, cpErr, execTar, execErr)
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}, logPath
}

// singleFileTar writes a tar holding one regular file and returns its path.
func singleFileTar(t *testing.T, name, data string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stream.tar")
	fh, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(fh)
	if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	fh.Close()
	return p
}

func execCalls(t *testing.T, logPath string) []string {
	var out []string
	for _, c := range calls(t, logPath) {
		if strings.HasPrefix(c, "exec ") {
			out = append(out, c)
		}
	}
	return out
}

func TestExportPathFallsBackToExecWhenCpCannotSeeFile(t *testing.T) {
	stream := singleFileTar(t, "run.json", `{"calls":[]}`)
	d, logPath := fakeCopyDocker(t, "", "", gvisorCpMissing, stream, "")
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: d})
	out := filepath.Join(t.TempDir(), "state.json")
	n, problem, err := e.exportPath(context.Background(), "bench-r_x-svc-agentdojo", "/state/run.json", out, 1<<20)
	if err != nil || problem != "" {
		t.Fatalf("exportPath: n=%d problem=%q err=%v", n, problem, err)
	}
	if b, _ := os.ReadFile(out); string(b) != `{"calls":[]}` || n != int64(len(b)) {
		t.Fatalf("state file %q (n=%d)", b, n)
	}
	ex := execCalls(t, logPath)
	if len(ex) != 1 || ex[0] != "exec bench-r_x-svc-agentdojo tar -C /state -cf - run.json" {
		t.Fatalf("exec calls %q", ex)
	}
}

func TestExportPathUsesCpWhenItWorks(t *testing.T) {
	stream := singleFileTar(t, "run.json", "from cp")
	d, logPath := fakeCopyDocker(t, stream, "", "", "", "exec must not run")
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: d})
	out := filepath.Join(t.TempDir(), "state.json")
	if _, problem, err := e.exportPath(context.Background(), "c", "/state/run.json", out, 1<<20); err != nil || problem != "" {
		t.Fatalf("problem=%q err=%v", problem, err)
	}
	if b, _ := os.ReadFile(out); string(b) != "from cp" {
		t.Fatalf("state file %q", b)
	}
	if ex := execCalls(t, logPath); len(ex) != 0 {
		t.Fatalf("exec ran although docker cp worked: %q", ex)
	}
}

func TestExportPathBothFail(t *testing.T) {
	d, _ := fakeCopyDocker(t, "", "", gvisorCpMissing, "", "tar: run.json: Cannot stat: No such file or directory")
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: d})
	out := filepath.Join(t.TempDir(), "state.json")
	_, problem, err := e.exportPath(context.Background(), "c", "/state/run.json", out, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	want := "copy /state/run.json failed: docker cp could not see the file and docker exec tar failed: tar: run.json: Cannot stat: No such file or directory"
	if problem != want {
		t.Fatalf("problem %q, want %q", problem, want)
	}
}

func TestExportPathOtherCpErrorDoesNotFallBack(t *testing.T) {
	d, logPath := fakeCopyDocker(t, "", "", "Error response from daemon: No such container: c", "", "")
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: d})
	_, problem, err := e.exportPath(context.Background(), "c", "/state/run.json", filepath.Join(t.TempDir(), "s"), 1<<20)
	if err != nil || problem != "copy /state/run.json failed: Error response from daemon: No such container: c" {
		t.Fatalf("problem=%q err=%v", problem, err)
	}
	if ex := execCalls(t, logPath); len(ex) != 0 {
		t.Fatalf("exec ran: %q", ex)
	}
}

func TestCopyFileOutFallsBackForAdapterFaultLedger(t *testing.T) {
	stream := singleFileTar(t, "adapter.fault.json", `{"entries":1}`)
	d, logPath := fakeCopyDocker(t, "", "", "Error response from daemon: Could not find the file /ledger/adapter.fault.json in container h", stream, "")
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: d})
	dst := filepath.Join(t.TempDir(), "adapter.fault.json")
	if err := e.copyFileOut(context.Background(), "h", "/ledger/adapter.fault.json", dst, 1<<20); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != `{"entries":1}` {
		t.Fatalf("ledger %q", b)
	}
	if ex := execCalls(t, logPath); len(ex) != 1 || ex[0] != "exec h tar -C /ledger -cf - adapter.fault.json" {
		t.Fatalf("exec calls %q", ex)
	}
	if _, err := os.Stat(dst + ".tar"); !os.IsNotExist(err) {
		t.Fatalf("spool left behind: %v", err)
	}
}

func TestCopyFileOutUsesCpWhenItWorks(t *testing.T) {
	src := filepath.Join(t.TempDir(), "ledger")
	if err := os.WriteFile(src, []byte("from cp"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, logPath := fakeCopyDocker(t, "", src, "", "", "")
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: d})
	dst := filepath.Join(t.TempDir(), "adapter.fault.json")
	if err := e.copyFileOut(context.Background(), "h", "/ledger/adapter.fault.json", dst, 1<<20); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "from cp" {
		t.Fatalf("ledger %q", b)
	}
	if ex := execCalls(t, logPath); len(ex) != 0 {
		t.Fatalf("exec ran: %q", ex)
	}
}

func TestCopyRecordingFallsBackToExec(t *testing.T) {
	stream := singleFileTar(t, "session.har", `{"log":{"entries":[]}}`)
	d, logPath := fakeCopyDocker(t, "", "", "Error response from daemon: Could not find the file /tmp/rec/session.har in container b", stream, "")
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: d})
	var total int64
	recs := e.copyRecording(context.Background(), "b", t.TempDir(), "browser",
		schemas.RecordingFile{ID: "har", Path: "/tmp/rec/session.har", Kind: schemas.RecordingHAR}, &total)
	if len(recs) != 1 || recs[0].Error != "" || recs[0].Bytes == 0 {
		t.Fatalf("records %+v", recs)
	}
	if ex := execCalls(t, logPath); len(ex) != 1 {
		t.Fatalf("exec calls %q", ex)
	}
}

func TestCopyRecordingMissingKeepsNote(t *testing.T) {
	d, _ := fakeCopyDocker(t, "", "", "Error response from daemon: Could not find the file /tmp/rec/session.har in container b", "",
		"Error response from daemon: container b is not running")
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: d})
	var total int64
	f := schemas.RecordingFile{ID: "har", Path: "/tmp/rec/session.har", Kind: schemas.RecordingHAR}
	recs := e.copyRecording(context.Background(), "b", t.TempDir(), "browser", f, &total)
	if len(recs) != 1 || recs[0].Error != recordingMissingNote(f.Kind, f.Path) {
		t.Fatalf("records %+v", recs)
	}
}
