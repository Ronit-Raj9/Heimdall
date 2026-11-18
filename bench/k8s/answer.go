package k8s

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// ExportAnswer implements schemas.AnswerEnvironment with the Docker driver's
// answer script, run in the pod's bench helper on its read-only view of the
// workspace: the same path rules (a symlink out of the workspace is a
// missing answer), the same 256 KiB cap, the same answer.txt.
func (d *Driver) ExportAnswer(ctx context.Context, rc *schemas.RunContext) (*schemas.Answer, error) {
	r, err := d.ref(rc)
	if err != nil {
		return nil, err
	}
	if rc.Handle == nil || rc.Dir == "" {
		return nil, fmt.Errorf("export answer for run %s: needs a workspace handle and a run folder", r.runID)
	}
	rel, err := environments.AnswerRel(rc)
	if err != nil {
		return nil, fmt.Errorf("export answer for run %s: %w", r.runID, err)
	}
	cctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	out, err := d.cfg.Client.ExecOutput(cctx, r.ns, podName, ctrBench,
		[]string{"env", "BENCH_CAP=" + strconv.Itoa(environments.MaxAnswerBytes), "python3", "-I", "-c", environments.AnswerScript, rel, environments.DefaultWorkdir, ""},
		nil, int64(environments.MaxAnswerBytes)+1)
	var ee *ExitError
	switch {
	case errors.As(err, &ee) && ee.Code == environments.AnswerExitMissing:
		return &schemas.Answer{Source: "missing"}, nil
	case errors.As(err, &ee) && ee.Code == environments.AnswerExitEscapes:
		d.cfg.Logger.Warn("answer path leaves the workspace; answer is missing", "run", r.runID, "path", rel)
		return &schemas.Answer{Source: "missing"}, nil
	case err != nil && !errors.As(err, &ee) && len(out) > environments.MaxAnswerBytes:
		// Over the cap: ExecOutput reports the cut; the answer is truncated.
	case err != nil:
		return nil, fmt.Errorf("export answer for run %s: %w", r.runID, err)
	}
	data, truncated := out, false
	if len(data) > environments.MaxAnswerBytes {
		data, truncated = data[:environments.MaxAnswerBytes], true
	}
	if err := os.WriteFile(filepath.Join(rc.Dir, environments.AnswerFile), data, 0o644); err != nil {
		return nil, fmt.Errorf("export answer for run %s: write %s: %w", r.runID, environments.AnswerFile, err)
	}
	return &schemas.Answer{Text: string(data), Source: "file", SHA256: utils.SHA256Bytes(data), Truncated: truncated}, nil
}

// ExportArtifacts implements schemas.ArtifactEnvironment with the Docker
// driver's artifact script, run in the bench helper on its read-only view of
// the workspace; its tar stream comes back over exec and is read with every
// cap enforced again, into the run's artifacts folder. A broken export
// leaves the folder empty.
func (d *Driver) ExportArtifacts(ctx context.Context, rc *schemas.RunContext) ([]schemas.ArtifactFile, error) {
	r, err := d.ref(rc)
	if err != nil {
		return nil, err
	}
	if rc.Handle == nil || rc.Dir == "" {
		return nil, fmt.Errorf("export artifacts for run %s: needs a workspace handle and a run folder", r.runID)
	}
	env, specs, err := environments.ArtifactEnv(rc)
	if err != nil {
		return nil, fmt.Errorf("export artifacts for run %s: %w", r.runID, err)
	}
	dir, err := environments.ResetArtifactsDir(rc)
	if err != nil {
		return nil, fmt.Errorf("export artifacts for run %s: %w", r.runID, err)
	}
	if len(specs) == 0 {
		return nil, nil
	}
	argv := []string{"env"}
	for _, k := range []string{"BENCH_SPECS", "BENCH_TOTAL", "BENCH_MATCHES"} {
		argv = append(argv, k+"="+env[k])
	}
	argv = append(argv, "python3", "-I", "-c", environments.ArtifactScript, environments.DefaultWorkdir)
	cctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	pr, pw := io.Pipe()
	var stderr capBuffer
	stderr.limit = 4096
	done := make(chan struct{})
	var code int
	var xerr error
	go func() {
		defer close(done)
		code, xerr = d.cfg.Client.Exec(cctx, r.ns, podName, ctrBench, argv, StreamOptions{Stdout: pw, Stderr: &stderr})
		pw.CloseWithError(io.EOF)
	}()
	files, rerr := environments.ReadArtifactStream(pr, dir, specs)
	if rerr != nil {
		cancel()
	}
	pr.Close()
	<-done
	switch {
	case rerr == nil && xerr == nil && code == 0:
		return files, nil
	case xerr == nil && code != 0 && rerr == nil:
		rerr = fmt.Errorf("helper exited %d: %s", code, stderr.String())
	case rerr == nil:
		rerr = xerr
	}
	if err := resetArtifacts(rc); err != nil {
		rerr = errors.Join(rerr, err)
	}
	return nil, fmt.Errorf("export artifacts for run %s: %w", r.runID, rerr)
}

func resetArtifacts(rc *schemas.RunContext) error {
	_, err := environments.ResetArtifactsDir(rc)
	return err
}
