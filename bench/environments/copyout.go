package environments

// Copies out of a service container (fix wave 2, E3). `docker cp` reads a
// container's file system through the Docker daemon. Under gVisor (runsc) a
// file the service writes inside its sandbox lives in gVisor's own root
// overlay, which the daemon cannot see, so `docker cp` answers "Could not
// find the file ... in container ..." although the file is there. Every copy
// out of a running container therefore falls back to `docker exec <c> tar`,
// which runs inside the sandbox and streams the same tar `docker cp` would.

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
)

// copyOutViaExec streams src out of the running container as a tar (the
// stream `docker cp <c>:<src> -` gives) by running tar inside it. It returns
// tar's or docker's stderr (capped) and the command error.
func (e *dockerWorkspace) copyOutViaExec(ctx context.Context, container, src string, w io.Writer) (stderr string, err error) {
	var errBuf strings.Builder
	capped := &cappedWriter{w: &errBuf, limit: 2048}
	cmd := exec.CommandContext(ctx, dockerBinary(e.cfg.Docker), "exec", container, "tar", "-C", path.Dir(src), "-cf", "-", path.Base(src))
	cmd.Stdout, cmd.Stderr = w, capped
	err = cmd.Run()
	return strings.TrimSpace(errBuf.String()), err
}

// cpCannotSee reports a `docker cp` failure that means the daemon could not
// see the path: the file is missing, or it lives inside a gVisor sandbox.
func cpCannotSee(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "could not find the file") || strings.Contains(s, "no such file or directory")
}

// spoolCopy is the result of copying a path out of a container into a
// spool file as a tar stream.
type spoolCopy struct {
	failed     bool   // neither docker cp nor the exec fallback produced the stream
	cpStderr   string // docker cp's stderr when it failed
	fellBack   bool   // docker cp could not see the path and docker exec tar ran
	execStderr string // the fallback's stderr when it failed
	truncated  bool   // the stream was over cap
}

// copyToSpool writes the tar stream of src in container into spool (created
// or truncated), at most limit bytes: `docker cp` first, then, when docker cp
// could not see the path, `docker exec tar`. The error is a local file error
// only; copy failures are in the result.
func (e *dockerWorkspace) copyToSpool(ctx context.Context, container, src, spool string, limit int64) (spoolCopy, error) {
	var res spoolCopy
	fh, err := os.OpenFile(spool, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return res, err
	}
	w := &cappedWriter{w: fh, limit: limit}
	var errBuf strings.Builder
	cmd := exec.CommandContext(ctx, dockerBinary(e.cfg.Docker), "cp", container+":"+src, "-")
	cmd.Stdout, cmd.Stderr = w, &errBuf
	runErr := cmd.Run()
	if runErr != nil && w.err == nil && cpCannotSee(errBuf.String()) {
		res.fellBack = true
		if err := fh.Truncate(0); err != nil {
			fh.Close()
			return res, err
		}
		if _, err := fh.Seek(0, io.SeekStart); err != nil {
			fh.Close()
			return res, err
		}
		w = &cappedWriter{w: fh, limit: limit}
		res.execStderr, runErr = e.copyOutViaExec(ctx, container, src, w)
		if runErr != nil && res.execStderr == "" {
			res.execStderr = runErr.Error()
		}
	}
	if err := errors.Join(w.err, fh.Close()); err != nil {
		return res, err
	}
	if runErr != nil {
		res.failed = true
		res.cpStderr = strings.TrimSpace(errBuf.String())
		return res, nil
	}
	res.truncated = w.truncated
	return res, nil
}

// copyFileOut copies the regular file src out of container to the host file
// dst, like `docker cp <c>:<src> <dst>`, with the exec tar fallback when
// docker cp cannot see the file. limit caps the fallback's stream.
func (e *dockerWorkspace) copyFileOut(ctx context.Context, container, src, dst string, limit int64) error {
	_, cpErr := e.cfg.Docker.MustRun(ctx, "cp", container+":"+src, dst)
	if cpErr == nil || !cpCannotSee(cpErr.Error()) {
		return cpErr
	}
	spool := dst + ".tar"
	defer os.Remove(spool)
	res, err := e.copyToSpool(ctx, container, src, spool, limit)
	switch {
	case err != nil:
		return fmt.Errorf("write %s: %w", spool, err)
	case res.failed:
		return fmt.Errorf("copy %s failed: docker cp could not see the file and docker exec tar failed: %s", src, truncate(res.execStderr, 300))
	case res.truncated:
		return fmt.Errorf("copy %s: over its %d byte cap", src, limit)
	}
	return extractSingleFile(spool, dst, limit)
}

// extractSingleFile writes the one regular file of the tar at spool to dst.
func extractSingleFile(spool, dst string, limit int64) error {
	in, err := os.Open(spool)
	if err != nil {
		return err
	}
	defer in.Close()
	tr := tar.NewReader(in)
	hdr, err := tr.Next()
	if err != nil {
		return fmt.Errorf("copy stream is not a tar: %w", err)
	}
	if hdr.Typeflag != tar.TypeReg {
		return fmt.Errorf("copy of %s is not a regular file", hdr.Name)
	}
	if hdr.Size > limit {
		return fmt.Errorf("%s is %d bytes, over its %d byte cap", hdr.Name, hdr.Size, limit)
	}
	data, err := io.ReadAll(io.LimitReader(tr, limit))
	if err != nil {
		return fmt.Errorf("read %s: %w", hdr.Name, err)
	}
	return os.WriteFile(dst, data, 0o644)
}
