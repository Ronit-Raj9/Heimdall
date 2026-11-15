package environments

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Artifact stream limits. The helper's stream is read with every cap
// enforced again on the bench side, so a helper that misbehaves cannot make
// the bench store more than the artifact caps allow.
const (
	// artifactManifest is the first member of the helper's tar stream: the
	// JSON list of every match, in order.
	artifactManifest = "manifest.json"
	// maxArtifactManifestBytes caps that manifest.
	maxArtifactManifestBytes = 4 << 20
	// maxArtifactTrailerBytes caps what may follow the end of the tar
	// stream (block padding).
	maxArtifactTrailerBytes = 1 << 20
)

// artifactScript runs in the trusted helper over agent-written files, with
// the workspace read-only and no network. argv holds the workspace roots
// (the first is the one artifact paths are relative to); BENCH_SPECS holds
// the specs as JSON, BENCH_TOTAL the run's total byte cap and BENCH_MATCHES
// the cap on files per glob. For each spec it expands the glob (sorted, not
// recursive), resolves every match with all symlinks followed and refuses
// one that ends outside the roots, is not a regular file, has a name that
// may not be copied, is over a cap or reuses a name already copied for the
// spec. It writes a tar stream to stdout: manifest.json (every match, with
// its error when it is not copied) and then one member per copied file,
// named by its index in the manifest. The name rule mirrors
// utils.ArtifactNameOK, which the bench checks again.
const artifactScript = `import glob, io, json, os, re, stat, sys, tarfile
roots = [os.path.realpath(r) for r in sys.argv[1:] if r]
specs = json.loads(os.environ["BENCH_SPECS"])
total_cap = int(os.environ["BENCH_TOTAL"])
match_cap = int(os.environ["BENCH_MATCHES"])
name_ok = re.compile(r"[A-Za-z0-9_][A-Za-z0-9._-]{0,127}\Z").match
os.chdir(roots[0])
def inside(p):
    return any(p == r or p.startswith(r + "/") for r in roots)
entries, blobs, total = [], [], 0
for s in specs:
    cap = s["max_bytes"]
    found = sorted(glob.glob(s["path"]))
    if not found:
        entries.append({"id": s["id"], "source": s["path"], "error": "missing: no file matches " + s["path"]})
        continue
    seen = {}
    for src in found[:match_cap]:
        e = {"id": s["id"], "name": os.path.basename(src), "source": src}
        entries.append(e)
        real = os.path.realpath(src)
        if not inside(real):
            e["error"] = "outside the workspace"
            continue
        try:
            st = os.stat(real)
        except OSError:
            st = None
        if st is None or not stat.S_ISREG(st.st_mode):
            e["error"] = "not a regular file"
            continue
        if not name_ok(e["name"]) or e["name"] in (".", ".."):
            e["error"] = "file name not allowed"
            continue
        if st.st_size > cap:
            e["error"], e["bytes"] = "over the per-file cap of %d bytes" % cap, st.st_size
            continue
        if e["name"] in seen:
            e["error"] = "name collides with " + seen[e["name"]]
            continue
        with open(real, "rb") as f:
            data = f.read(cap + 1)
        if len(data) > cap:
            e["error"], e["bytes"] = "over the per-file cap of %d bytes" % cap, len(data)
            continue
        if total + len(data) > total_cap:
            e["error"], e["bytes"] = "over the run's total cap of %d bytes" % total_cap, len(data)
            continue
        total += len(data)
        seen[e["name"]] = src
        e["bytes"] = len(data)
        blobs.append((len(entries) - 1, data))
    if len(found) > match_cap:
        entries.append({"id": s["id"], "source": s["path"], "error": "too many matches: %d, cap %d" % (len(found), match_cap)})
out = tarfile.open(fileobj=sys.stdout.buffer, mode="w|")
def add(name, data):
    ti = tarfile.TarInfo(name)
    ti.size, ti.mode = len(data), 0o644
    out.addfile(ti, io.BytesIO(data))
add("manifest.json", json.dumps(entries).encode())
for i, data in blobs:
    add(str(i), data)
out.close()
sys.stdout.buffer.flush()
`

// artifactRequest is one spec as the helper reads it from BENCH_SPECS.
type artifactRequest struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	MaxBytes int64  `json:"max_bytes"`
}

// artifactEntry is one manifest entry of the helper's stream.
type artifactEntry struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Source string `json:"source"`
	Bytes  int64  `json:"bytes"`
	Error  string `json:"error"`
}

// ExportArtifacts copies the files of an artifact task (OutputSpec.Artifacts
// of the task, else the suite) out of the workspace volume in a trusted
// helper with no network and the workspace read-only, into
// artifacts/<id>/<name> in the run folder, and returns one entry per match.
// The artifacts folder is always created, empty when nothing was copied, so
// the verifier's mount exists. A match the agent got wrong (missing, outside
// the workspace through a symlink, not a regular file, a name that may not
// be copied, over a size cap, a name collision, too many matches) is an
// entry with Error set. The error return is kept for the export itself
// breaking: the helper failing, a malformed stream or a write that fails.
func (e *dockerWorkspace) ExportArtifacts(ctx context.Context, rc *schemas.RunContext) ([]schemas.ArtifactFile, error) {
	expID, runID, err := runIDs(rc)
	if err != nil {
		return nil, err
	}
	if rc.Handle == nil || rc.Dir == "" {
		return nil, fmt.Errorf("export artifacts for run %s: needs a workspace handle and a run folder", runID)
	}
	specs := utils.OutputArtifacts(rc.Suite, rc.Task)
	reqs := make([]artifactRequest, 0, len(specs))
	for _, a := range specs {
		if err := utils.CheckArtifactSpec(a); err != nil {
			return nil, fmt.Errorf("export artifacts for run %s: %w", runID, err)
		}
		reqs = append(reqs, artifactRequest{ID: a.ID, Path: a.Path, MaxBytes: utils.ArtifactMaxBytes(a)})
	}
	dir := filepath.Join(rc.Dir, utils.ArtifactsDir)
	if err := resetDir(dir); err != nil {
		return nil, fmt.Errorf("export artifacts for run %s: %w", runID, err)
	}
	if len(specs) == 0 {
		return nil, nil
	}
	payload, err := json.Marshal(reqs)
	if err != nil {
		return nil, fmt.Errorf("export artifacts for run %s: %w", runID, err)
	}
	wp := workspacePath(rc)
	args := e.helperArgs(helperName(runID, "artifacts"), expID, runID, []string{"DAC_OVERRIDE", "DAC_READ_SEARCH"})
	args = append(args, "-v", rc.Handle.Volume+":"+defaultWorkdir+":ro")
	if wp != "" && wp != defaultWorkdir {
		args = append(args, "-v", rc.Handle.Volume+":"+wp+":ro")
	}
	args = append(args, "-e", "BENCH_SPECS="+string(payload),
		"-e", "BENCH_TOTAL="+strconv.FormatInt(utils.MaxArtifactTotalBytes, 10),
		"-e", "BENCH_MATCHES="+strconv.Itoa(utils.MaxArtifactMatches), "--entrypoint", "python3")
	args = append(removeEntrypoint(args), e.helperImage(rc), "-I", "-c", artifactScript, defaultWorkdir, wp)
	cctx, cancel := context.WithTimeout(ctx, exportTimeout)
	defer cancel()
	files, err := e.runArtifactHelper(cctx, args, dir, specs)
	if err != nil {
		// Leave no half-copied files behind: the run has no artifacts.
		if rerr := resetDir(dir); rerr != nil {
			err = errors.Join(err, rerr)
		}
		return nil, fmt.Errorf("export artifacts for run %s: %w", runID, err)
	}
	return files, nil
}

// runArtifactHelper runs the helper and reads its stream as it arrives.
func (e *dockerWorkspace) runArtifactHelper(ctx context.Context, args []string, dir string, specs []schemas.ArtifactSpec) ([]schemas.ArtifactFile, error) {
	cmd := exec.CommandContext(ctx, dockerBinary(e.cfg.Docker), args...)
	var errBuf strings.Builder
	cmd.Stderr = &cappedWriter{w: &errBuf, limit: 4096}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start helper: %w", err)
	}
	files, rerr := readArtifactStream(stdout, dir, specs)
	if rerr == nil {
		n, err := io.Copy(io.Discard, io.LimitReader(stdout, maxArtifactTrailerBytes))
		switch {
		case err != nil:
			rerr = fmt.Errorf("read helper stream: %w", err)
		case n >= maxArtifactTrailerBytes:
			rerr = errors.New("helper wrote past the end of its stream")
		}
	}
	if rerr != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	werr := cmd.Wait()
	stderr := truncate(strings.TrimSpace(errBuf.String()), 500)
	switch {
	case ctx.Err() != nil:
		return nil, fmt.Errorf("helper did not finish in %s", exportTimeout)
	case rerr != nil && werr != nil:
		return nil, fmt.Errorf("%w (helper: %v: %s)", rerr, werr, stderr)
	case rerr != nil:
		return nil, rerr
	case werr != nil:
		return nil, fmt.Errorf("helper: %v: %s", werr, stderr)
	}
	return files, nil
}

// readArtifactStream reads the helper's tar stream into dir: the manifest,
// then the copied files. Every cap is enforced again here: a manifest entry
// the helper accepted that breaks a rule becomes an entry with Error set and
// its bytes are dropped. A stream that does not follow the format is an
// error.
func readArtifactStream(r io.Reader, dir string, specs []schemas.ArtifactSpec) ([]schemas.ArtifactFile, error) {
	tr := tar.NewReader(r)
	hdr, err := tr.Next()
	if err != nil {
		return nil, fmt.Errorf("read helper stream: %w", err)
	}
	if hdr.Name != artifactManifest || hdr.Typeflag != tar.TypeReg || hdr.Size < 0 || hdr.Size > maxArtifactManifestBytes {
		return nil, fmt.Errorf("helper stream does not start with a %s of at most %d bytes", artifactManifest, maxArtifactManifestBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(tr, maxArtifactManifestBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", artifactManifest, err)
	}
	var entries []artifactEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("parse %s: %w", artifactManifest, err)
	}
	files, want, err := checkArtifactEntries(entries, specs)
	if err != nil {
		return nil, err
	}
	last := -1
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read helper stream: %w", err)
		}
		i, err := strconv.Atoi(hdr.Name)
		if err != nil || i <= last || i >= len(entries) || entries[i].Error != "" || hdr.Typeflag != tar.TypeReg || hdr.Size != entries[i].Bytes {
			return nil, fmt.Errorf("helper stream member %q does not match its manifest", hdr.Name)
		}
		last = i
		if !want[i] {
			continue // refused here; the tar reader skips its bytes
		}
		delete(want, i)
		if err := writeArtifact(tr, dir, &files[i], hdr.Size); err != nil {
			return nil, err
		}
	}
	if len(want) > 0 {
		return nil, fmt.Errorf("helper stream lacks %d file(s) its manifest lists", len(want))
	}
	return files, nil
}

// checkArtifactEntries turns the manifest into run entries and applies the
// artifact rules again: the id must be a spec's, the name must be allowed,
// and the per-file cap, the run's total cap, the match cap and the name
// collision rule hold. It returns the indexes of the entries whose bytes are
// to be stored. It is pure.
func checkArtifactEntries(entries []artifactEntry, specs []schemas.ArtifactSpec) ([]schemas.ArtifactFile, map[int]bool, error) {
	if len(entries) > len(specs)*(utils.MaxArtifactMatches+1) {
		return nil, nil, fmt.Errorf("%s lists %d entries for %d artifacts", artifactManifest, len(entries), len(specs))
	}
	byID := make(map[string]schemas.ArtifactSpec, len(specs))
	for _, a := range specs {
		byID[a.ID] = a
	}
	files := make([]schemas.ArtifactFile, len(entries))
	want := map[int]bool{}
	matches := map[string]int{}
	names := map[string]string{}
	var total int64
	for i, en := range entries {
		a, ok := byID[en.ID]
		if !ok {
			return nil, nil, fmt.Errorf("%s names unknown artifact %q", artifactManifest, en.ID)
		}
		if en.Bytes < 0 {
			return nil, nil, fmt.Errorf("%s gives %s a negative size", artifactManifest, en.Source)
		}
		f := schemas.ArtifactFile{ID: en.ID, Name: en.Name, Source: en.Source, Bytes: en.Bytes, Error: en.Error}
		if en.Name != "" {
			if matches[en.ID]++; matches[en.ID] > utils.MaxArtifactMatches {
				return nil, nil, fmt.Errorf("%s lists more than %d matches for %s", artifactManifest, utils.MaxArtifactMatches, en.ID)
			}
		}
		if f.Error == "" {
			limit, key := utils.ArtifactMaxBytes(a), en.ID+"/"+en.Name
			switch {
			case !utils.ArtifactNameOK(en.Name):
				f.Error = "file name not allowed"
			case en.Bytes > limit:
				f.Error = fmt.Sprintf("over the per-file cap of %d bytes", limit)
			case names[key] != "":
				f.Error = "name collides with " + names[key]
			case total+en.Bytes > utils.MaxArtifactTotalBytes:
				f.Error = fmt.Sprintf("over the run's total cap of %d bytes", utils.MaxArtifactTotalBytes)
			default:
				total += en.Bytes
				names[key] = en.Source
				want[i] = true
			}
		}
		files[i] = f
	}
	return files, want, nil
}

// writeArtifact stores one file at artifacts/<id>/<name> under the run
// folder (0644, folders 0755) and records its path and hash.
func writeArtifact(r io.Reader, dir string, f *schemas.ArtifactFile, size int64) error {
	sub := filepath.Join(dir, f.ID)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", sub, err)
	}
	// Explicit modes, whatever the umask: the verifier reads these files as
	// another user.
	if err := os.Chmod(sub, 0o755); err != nil {
		return fmt.Errorf("chmod %s: %w", sub, err)
	}
	out := filepath.Join(sub, f.Name)
	fh, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", out, err)
	}
	n, err := io.Copy(fh, io.LimitReader(r, size))
	if err := errors.Join(err, fh.Chmod(0o644), fh.Close()); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	if n != size {
		return fmt.Errorf("helper stream cut %s short: %d of %d bytes", f.Source, n, size)
	}
	sum, err := utils.SHA256File(out)
	if err != nil {
		return err
	}
	f.Path, f.SHA256 = utils.ArtifactRunPath(f.ID, f.Name), sum
	return nil
}

// resetDir makes dir an empty folder (0755).
func resetDir(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clear %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	return nil
}
