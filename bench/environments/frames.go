package environments

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"

	"github.com/maximhq/bifrost/bench/schemas"
)

// maxFramesExport caps the frames folder copied out of the tool proxy.
const maxFramesExport = 512 << 20

var frameExportName = regexp.MustCompile(`^([0-9]{4,6}\.(png|jpg|jpeg)|frames\.jsonl)$`)

// copyFrames copies the tool proxy's screen replay frames (the screenshots
// agents got, saved next to the ledger by toolproxy/frames.go) into the run
// folder as frames/. A run without frames leaves no folder. Best effort: a
// failure is logged, never fails the export.
func (e *dockerWorkspace) copyFrames(ctx context.Context, runID, dir string) {
	cctx, cancel := context.WithTimeout(ctx, exportTimeout)
	defer cancel()
	spool := filepath.Join(dir, ".frames.tar")
	defer os.Remove(spool)
	src := path.Join(path.Dir(toolProxyLedger), schemas.FramesDir)
	_, problem, err := e.exportPath(cctx, toolProxyContainer(runID), src, spool, maxFramesExport)
	if err != nil || problem != "" {
		e.cfg.Logger.Debug("no screen replay frames copied", "run", runID, "problem", problem)
		return
	}
	if err := untarFrames(spool, filepath.Join(dir, schemas.FramesDir)); err != nil {
		e.cfg.Logger.Warn("screen replay frames not unpacked", "run", runID, "error", err.Error())
	}
}

// untarFrames unpacks the docker cp tar of the frames folder into out. Only
// regular files with a frame name (or the index) are written, by base name,
// so nothing can land outside out.
func untarFrames(tarPath, out string) error {
	in, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(in)
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Base(hdr.Name)
		if hdr.Typeflag != tar.TypeReg || !frameExportName.MatchString(name) {
			continue
		}
		total += hdr.Size
		if total > maxFramesExport {
			return errors.New("frames over the export cap")
		}
		fh, err := os.OpenFile(filepath.Join(out, name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_, cerr := io.Copy(fh, io.LimitReader(tr, hdr.Size))
		if err := errors.Join(cerr, fh.Close()); err != nil {
			return err
		}
	}
}
