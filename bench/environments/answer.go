package environments

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// AnswerFile is the run file that holds the exported answer.
const AnswerFile = "answer.txt"

// maxAnswerBytes caps the exported answer; a longer one is cut and marked
// truncated.
const maxAnswerBytes = 256 << 10

// Answer helper exit codes.
const (
	answerMissing = 3 // no regular file at the path
	answerEscapes = 4 // the path resolves outside the workspace
)

// answerScript runs in the trusted helper over agent-written files, with the
// workspace read-only. It resolves the answer path with every symlink
// followed, refuses a path that ends outside the workspace mounts, and prints
// at most BENCH_CAP+1 bytes of a regular file.
const answerScript = `import os, sys
roots = [r for r in sys.argv[2:] if r]
p = os.path.realpath(os.path.join(roots[0], sys.argv[1]))
if not any(p == r or p.startswith(r + "/") for r in roots):
    sys.exit(4)
if not os.path.isfile(p):
    sys.exit(3)
with open(p, "rb") as f:
    sys.stdout.buffer.write(f.read(int(os.environ["BENCH_CAP"]) + 1))
`

// ExportAnswer reads the agent's answer file (OutputSpec.AnswerPath of the
// task, else the suite, else answer.txt, relative to the suite workdir) from
// the workspace volume in a trusted helper with no network, writes it to
// answer.txt in the run folder and returns it with source file. A missing
// file, or one whose path leaves the workspace through a symlink, returns
// source missing and writes nothing: the run kernel may then use the final
// message and record that source. An answer over 256 KiB is cut and marked
// truncated.
func (e *dockerWorkspace) ExportAnswer(ctx context.Context, rc *schemas.RunContext) (*schemas.Answer, error) {
	expID, runID, err := runIDs(rc)
	if err != nil {
		return nil, err
	}
	if rc.Handle == nil || rc.Dir == "" {
		return nil, fmt.Errorf("export answer for run %s: needs a workspace handle and a run folder", runID)
	}
	rel, err := answerRel(rc)
	if err != nil {
		return nil, fmt.Errorf("export answer for run %s: %w", runID, err)
	}
	wp := workspacePath(rc)
	args := e.helperArgs(helperName(runID, "answer"), expID, runID, []string{"DAC_OVERRIDE", "DAC_READ_SEARCH"})
	args = append(args, "-v", rc.Handle.Volume+":"+defaultWorkdir+":ro")
	if wp != "" && wp != defaultWorkdir {
		args = append(args, "-v", rc.Handle.Volume+":"+wp+":ro")
	}
	args = append(args, "-e", "BENCH_CAP="+strconv.Itoa(maxAnswerBytes), "--entrypoint", "python3")
	args = append(removeEntrypoint(args), e.helperImage(rc), "-I", "-c", answerScript, rel, defaultWorkdir, wp)
	res, err := e.cfg.Docker.Run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("export answer for run %s: %w", runID, err)
	}
	switch res.ExitCode {
	case 0:
	case answerMissing:
		return &schemas.Answer{Source: "missing"}, nil
	case answerEscapes:
		e.cfg.Logger.Warn("answer path leaves the workspace; answer is missing", "run", runID, "path", rel)
		return &schemas.Answer{Source: "missing"}, nil
	default:
		return nil, fmt.Errorf("export answer for run %s: exit %d: %s", runID, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	data, truncated := res.Stdout, false
	if len(data) > maxAnswerBytes {
		data, truncated = data[:maxAnswerBytes], true
	}
	if err := os.WriteFile(filepath.Join(rc.Dir, AnswerFile), data, 0o644); err != nil {
		return nil, fmt.Errorf("export answer for run %s: write %s: %w", runID, AnswerFile, err)
	}
	return &schemas.Answer{Text: string(data), Source: "file", SHA256: utils.SHA256Bytes(data), Truncated: truncated}, nil
}

// answerRel is the answer file relative to the workspace root: the answer
// path under the suite workdir, which must lie in the workspace (mounted at
// /workspace, and at the task image's workspace path when there is one). It
// is pure.
func answerRel(rc *schemas.RunContext) (string, error) {
	p := utils.AnswerContainerPath(rc.Suite, rc.Task)
	if !path.IsAbs(utils.AnswerPath(rc.Suite, rc.Task)) {
		if wp := workspacePath(rc); wp != "" {
			p = path.Join(wp, utils.AnswerPath(rc.Suite, rc.Task))
		}
	} else {
		p = path.Clean(utils.AnswerPath(rc.Suite, rc.Task))
	}
	for _, root := range []string{defaultWorkdir, workspacePath(rc)} {
		if root == "" {
			continue
		}
		if rel, ok := strings.CutPrefix(p, root+"/"); ok && rel != "" {
			return rel, nil
		}
	}
	return "", fmt.Errorf("answer path %s is not inside the workspace", p)
}

// removeEntrypoint drops the helper's default --entrypoint /bin/sh from args,
// keeping a later one.
func removeEntrypoint(args []string) []string {
	out := make([]string, 0, len(args))
	skipped := false
	for i := 0; i < len(args); i++ {
		if !skipped && args[i] == "--entrypoint" && i+1 < len(args) && args[i+1] == "/bin/sh" {
			skipped = true
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}
