package builder

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/issuebench"
)

// Repository states run_tests can run in.
const (
	StateBase      = "base"
	StateBaseTests = "base+tests"
	StateGold      = "gold"
)

// overlay is the content of every file a state changes relative to base;
// nil content means the file does not exist in that state.
type overlay map[string][]byte

// patchPaths lists the files a unified diff touches.
func patchPaths(patch string) []string {
	seen := map[string]bool{}
	for _, line := range strings.Split(patch, "\n") {
		for _, p := range []string{"--- a/", "+++ b/"} {
			if strings.HasPrefix(line, p) {
				f := strings.TrimSpace(strings.TrimPrefix(line, p))
				if i := strings.IndexByte(f, '\t'); i >= 0 {
					f = f[:i]
				}
				if f != "" && !strings.Contains(f, "..") && !filepath.IsAbs(f) {
					seen[f] = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// buildOverlay applies patches to a copy of the touched files of repoDir
// (host side, with host git) and returns base content and state content.
func buildOverlay(ctx context.Context, repoDir string, patches ...string) (base, state overlay, err error) {
	files := map[string]bool{}
	for _, p := range patches {
		for _, f := range patchPaths(p) {
			files[f] = true
		}
	}
	tmp, err := os.MkdirTemp("", "hb-overlay-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)
	base, state = overlay{}, overlay{}
	for f := range files {
		b, rerr := os.ReadFile(filepath.Join(repoDir, f))
		if rerr != nil {
			base[f] = nil
			continue
		}
		base[f] = b
		dst := filepath.Join(tmp, f)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return nil, nil, err
		}
	}
	for i, p := range patches {
		if strings.TrimSpace(p) == "" {
			continue
		}
		cmd := exec.CommandContext(ctx, "git", "apply", "--whitespace=nowarn", "-")
		cmd.Dir = tmp
		cmd.Env = append(os.Environ(), "GIT_DIR="+filepath.Join(tmp, ".nogit"))
		cmd.Stdin = strings.NewReader(p)
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		if err := cmd.Run(); err != nil {
			return nil, nil, fmt.Errorf("apply patch %d: %v: %s", i+1, err, tail(buf.String(), 400))
		}
	}
	for f := range files {
		b, rerr := os.ReadFile(filepath.Join(tmp, f))
		if rerr != nil {
			state[f] = nil
			continue
		}
		state[f] = b
	}
	return base, state, nil
}

// stateOverlay returns the overlay of one state for one candidate.
func stateOverlay(ctx context.Context, repoDir string, c issuebench.Candidate, state string) (base, st overlay, err error) {
	switch state {
	case StateBase:
		return buildOverlay(ctx, repoDir)
	case StateBaseTests:
		return buildOverlay(ctx, repoDir, c.TestPatch)
	case StateGold:
		return buildOverlay(ctx, repoDir, c.TestPatch, c.FixPatch)
	}
	return nil, nil, fmt.Errorf("unknown state %q (base, base+tests or gold)", state)
}

// overlayScript writes (or removes) every file of o under Workdir.
func overlayScript(o overlay) string {
	keys := make([]string, 0, len(o))
	for k := range o {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, f := range keys {
		q := shellQuote(Workdir + "/" + f)
		if o[f] == nil {
			fmt.Fprintf(&b, "rm -f %s\n", q)
			continue
		}
		fmt.Fprintf(&b, "mkdir -p \"$(dirname %s)\" && echo %s | base64 -d > %s\n", q, base64.StdEncoding.EncodeToString(o[f]), q)
	}
	return b.String()
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
