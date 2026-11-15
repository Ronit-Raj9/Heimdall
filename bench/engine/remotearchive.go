package engine

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Run folder files the remote path reads by name. They match the store's
// layout (DESIGN.md 16.1).
const (
	runJSONFile     = "run.json"
	sealJSONFile    = "seal.json"
	transitionsFile = "transitions.jsonl"
	metricsJSONFile = "metrics.json"
	// remoteFolder holds what the coordinator adds to an accepted run:
	// worker.json (who ran it) and seal.json (the worker's seal, byte for
	// byte). An upload that already has it is refused.
	remoteFolder = "remote"
	// maxArchiveEntries bounds the entries of one uploaded archive.
	maxArchiveEntries = 100000
)

// packRun writes the run folder dir as a gzip-compressed tar to w. Only
// regular files and folders go in, with slash paths relative to dir; a
// symlink or any other entry fails the pack, since the seal never covers one.
func packRun(dir string, w io.Writer) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.IsDir():
			return tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: rel + "/", Mode: 0o755})
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file", rel)
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: rel, Mode: 0o644, Size: info.Size()}); err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return fmt.Errorf("pack run folder %s: %w", dir, err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("pack run folder %s: %w", dir, err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("pack run folder %s: %w", dir, err)
	}
	return nil
}

// extractRun unpacks an uploaded archive into dst, which must not exist, and
// returns slash path -> sha256 of every file. It is the coordinator's only
// contact with worker bytes before the seal check, so it refuses rather than
// repairs: an absolute or non-local name, a ".." segment, a symlink, a hard
// link or any other non-regular entry, a duplicate name, more than
// maxArchiveEntries entries, or more than maxBytes of file content fails the
// whole extraction. Files are written 0644 and folders 0755.
func extractRun(r io.Reader, dst string, maxBytes int64) (map[string]string, error) {
	gz, err := gzip.NewReader(bufio.NewReader(r))
	if err != nil {
		return nil, fmt.Errorf("extract: not a gzip stream: %w", err)
	}
	defer gz.Close()
	if err := os.Mkdir(dst, 0o755); err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}
	tr := tar.NewReader(gz)
	files := map[string]string{}
	seen := map[string]bool{}
	var total int64
	for n := 0; ; n++ {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, fmt.Errorf("extract: read archive: %w", err)
		}
		if n >= maxArchiveEntries {
			return nil, fmt.Errorf("extract: more than %d entries", maxArchiveEntries)
		}
		name, err := archiveName(hdr.Name)
		if err != nil {
			return nil, fmt.Errorf("extract: %w", err)
		}
		if seen[name] {
			return nil, fmt.Errorf("extract: %s appears twice", name)
		}
		seen[name] = true
		target := filepath.Join(dst, filepath.FromSlash(name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return nil, fmt.Errorf("extract %s: %w", name, err)
			}
			continue
		case tar.TypeReg:
		default:
			return nil, fmt.Errorf("extract: %s is not a regular file or folder (tar type %q); symlinks, links and devices are refused", name, hdr.Typeflag)
		}
		if hdr.Size < 0 || total+hdr.Size > maxBytes {
			return nil, fmt.Errorf("extract: run folder is larger than %d bytes: %w", maxBytes, schemas.ErrUploadTooLarge)
		}
		total += hdr.Size
		sum, err := writeExtracted(dst, target, tr, hdr.Size)
		if err != nil {
			return nil, fmt.Errorf("extract %s: %w", name, err)
		}
		files[name] = sum
	}
}

// archiveName cleans one archive entry name and refuses any that could leave
// the extraction folder.
func archiveName(raw string) (string, error) {
	name := strings.TrimSuffix(raw, "/")
	if name == "" || strings.ContainsAny(name, "\\\x00") || path.IsAbs(name) {
		return "", fmt.Errorf("entry name %q is not a relative slash path", raw)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("entry name %q has an empty, . or .. segment", raw)
		}
	}
	if path.Clean(name) != name || !filepath.IsLocal(filepath.FromSlash(name)) {
		return "", fmt.Errorf("entry name %q is not local", raw)
	}
	return name, nil
}

// writeExtracted writes exactly size bytes from r to target and returns
// their sha256. Every parent folder of target must already be a real folder
// inside dst: a parent that is not (it was never created, or is a file) fails
// the write instead of being followed.
func writeExtracted(dst, target string, r io.Reader, size int64) (string, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	if err := insideReal(dst, filepath.Dir(target)); err != nil {
		return "", err
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, size))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if n != size {
		return "", fmt.Errorf("short file: %d of %d bytes", n, size)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// insideReal checks that dir resolves, symlinks followed, to a folder inside
// root. Extraction never creates a symlink, so this holds unless something
// else changed the folder while it was being written.
func insideReal(root, dir string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(realRoot, realDir)
	if err != nil || (rel != "." && !filepath.IsLocal(rel)) {
		return fmt.Errorf("folder %s resolves outside the extraction folder", dir)
	}
	return nil
}

// sealedUpload is an uploaded run folder whose worker seal checked out.
type sealedUpload struct {
	dir         string
	files       map[string]string // every file but seal.json
	run         *schemas.Run
	runRaw      []byte
	seal        *schemas.SealRecord
	sealRaw     []byte
	transitions []schemas.Transition
	metrics     map[string]schemas.Value
}

// uploadWant is what an uploaded run must be: the lease's slot and attempt,
// the experiment's manifest, and the run and seal the worker reported.
type uploadWant struct {
	expID         string
	slot          *schemas.Slot
	attempt       int
	manifestHash  string
	suiteLockHash string
	runID         string
	sealSHA256    string
}

// verifyUpload checks an extracted run folder against its own seal.json with
// the rules of store.Verify (every listed file present with the same sha256,
// nothing added; extraction already refused every non-regular entry), and
// that the seal and run.json name the leased slot, attempt and experiment.
// The returned error is the reason the upload is rejected.
func verifyUpload(dir string, files map[string]string, want uploadWant) (*sealedUpload, error) {
	sealRaw, err := os.ReadFile(filepath.Join(dir, sealJSONFile))
	if err != nil {
		return nil, fmt.Errorf("no seal.json: the run folder is not sealed")
	}
	if got := sha256Hex(sealRaw); got != want.sealSHA256 {
		return nil, fmt.Errorf("seal.json hashes to %s, the worker reported %s", got, want.sealSHA256)
	}
	var seal schemas.SealRecord
	if err := json.Unmarshal(sealRaw, &seal); err != nil {
		return nil, fmt.Errorf("decode seal.json: %v", err)
	}
	seal.Hash = want.sealSHA256
	if seal.RunID != want.runID {
		return nil, fmt.Errorf("seal.json names run %q, the worker reported %q", seal.RunID, want.runID)
	}
	if seal.ManifestHash != want.manifestHash {
		return nil, fmt.Errorf("seal.json manifest hash %q is not the experiment's %q", seal.ManifestHash, want.manifestHash)
	}
	rest := make(map[string]string, len(files))
	for name, sum := range files {
		if name == remoteFolder || strings.HasPrefix(name, remoteFolder+"/") {
			return nil, fmt.Errorf("%s is reserved for the coordinator", name)
		}
		if name != sealJSONFile {
			rest[name] = sum
		}
	}
	if err := compareSealFiles(seal.Files, rest); err != nil {
		return nil, err
	}
	up := &sealedUpload{dir: dir, files: rest, seal: &seal, sealRaw: sealRaw}
	if err := up.readRun(want); err != nil {
		return nil, err
	}
	return up, nil
}

// readRun decodes run.json, transitions.jsonl and metrics.json and checks the
// run's identity.
func (up *sealedUpload) readRun(want uploadWant) error {
	raw, err := os.ReadFile(filepath.Join(up.dir, runJSONFile))
	if err != nil {
		return fmt.Errorf("no run.json")
	}
	var run schemas.Run
	if err := json.Unmarshal(raw, &run); err != nil {
		return fmt.Errorf("decode run.json: %v", err)
	}
	switch {
	case run.RunID != want.runID:
		return fmt.Errorf("run.json names run %q, not %q", run.RunID, want.runID)
	case run.ExpID != want.expID || run.SlotID != want.slot.SlotID || run.PairID != want.slot.PairID:
		return fmt.Errorf("run.json is for %s/%s, not the leased slot %s/%s", run.ExpID, run.SlotID, want.expID, want.slot.SlotID)
	case run.Attempt != want.attempt:
		return fmt.Errorf("run.json attempt %d is not the leased attempt %d", run.Attempt, want.attempt)
	case run.ManifestHash != want.manifestHash || run.SuiteLockHash != want.suiteLockHash:
		return fmt.Errorf("run.json manifest or suite lock hash differs from the experiment's")
	case run.Timing.Sealed == nil || run.Stage != schemas.StageSeal:
		return fmt.Errorf("run.json does not record a sealed run (stage %q)", run.Stage)
	case run.Outcome == "" || run.Outcome == schemas.OutcomeUnknown:
		return fmt.Errorf("run.json has no outcome")
	}
	up.run, up.runRaw = &run, raw
	if up.transitions, err = readTransitions(filepath.Join(up.dir, transitionsFile)); err != nil {
		return err
	}
	if b, err := os.ReadFile(filepath.Join(up.dir, metricsJSONFile)); err == nil {
		if err := json.Unmarshal(b, &up.metrics); err != nil {
			return fmt.Errorf("decode metrics.json: %v", err)
		}
	}
	return nil
}

// compareSealFiles reports the first difference, in path order, between the
// seal's file list and the files that arrived.
func compareSealFiles(want, got map[string]string) error {
	paths := make([]string, 0, len(want)+len(got))
	for p := range want {
		paths = append(paths, p)
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		w, inWant := want[p]
		g, inGot := got[p]
		switch {
		case !inGot:
			return fmt.Errorf("file %s removed: %w", p, schemas.ErrSealMismatch)
		case !inWant:
			return fmt.Errorf("file %s added: %w", p, schemas.ErrSealMismatch)
		case w != g:
			return fmt.Errorf("file %s changed: %w", p, schemas.ErrSealMismatch)
		}
	}
	return nil
}

func readTransitions(p string) ([]schemas.Transition, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("no transitions.jsonl")
	}
	var out []schemas.Transition
	for i, line := range bytes.Split(b, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var t schemas.Transition
		if err := json.Unmarshal(line, &t); err != nil {
			return nil, fmt.Errorf("transitions.jsonl line %d: %v", i+1, err)
		}
		out = append(out, t)
	}
	return out, nil
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// sha256File returns the sha256 and size of a file.
func sha256File(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
