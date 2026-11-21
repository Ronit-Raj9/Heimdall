package release

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const (
	dirPerm  fs.FileMode = 0o755
	filePerm fs.FileMode = 0o644
)

// appendRelease holds the log lock while it checks the chain, gives rec its
// id and prev_hash, writes the evidence pack, hashes the record and appends
// it with fsync. The lock spans the pack because release.json inside it
// carries prev_hash. A broken chain refuses: appending to it would bury the
// break under a valid-looking tail.
func (l *Log) appendRelease(rec *schemas.ReleaseRecord, pack packer) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.d.Root, dirPerm); err != nil {
		return fmt.Errorf("create release: releases root: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(l.d.Root, LogFile), os.O_RDWR|os.O_APPEND|os.O_CREATE, filePerm)
	if err != nil {
		return fmt.Errorf("create release: open %s: %w", LogFile, err)
	}
	unlock, err := lockFile(f)
	if err != nil {
		return errors.Join(fmt.Errorf("create release: %w", err), f.Close())
	}
	defer func() {
		if err := unlock(); err != nil {
			l.d.Logger.Warn("release log unlock failed", "error", err.Error())
		}
	}()
	b, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("create release: read %s: %w", LogFile, err)
	}
	recs, err := parseChain(b)
	if err != nil {
		return &RefusedError{Reason: fmt.Sprintf("the release log does not verify (%v); repair or move %s first", err, LogFile), Err: ErrChainBroken}
	}
	if len(recs) > 0 {
		rec.PrevHash = recs[len(recs)-1].Hash
	}
	dir, err := l.newReleaseDir(rec)
	if err != nil {
		return err
	}
	if err := l.writeEvidence(rec, pack, dir); err != nil {
		return errors.Join(err, os.RemoveAll(dir))
	}
	if err := appendRecord(f, rec); err != nil {
		return errors.Join(err, os.RemoveAll(dir))
	}
	return nil
}

// newReleaseDir picks a fresh release id and creates its folder; mkdir is
// what makes the id unique.
func (l *Log) newReleaseDir(rec *schemas.ReleaseRecord) (string, error) {
	for range 8 {
		id := utils.NewID("rel", 10)
		dir := filepath.Join(l.d.Root, id)
		err := os.Mkdir(dir, dirPerm)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("create release: %w", err)
		}
		rec.ReleaseID = id
		rec.EvidencePath = id + "/" + EvidenceFile
		return dir, nil
	}
	return "", errors.New("create release: no free release id")
}

// packer lists the files of a record's evidence pack. It runs under the
// log lock, after the record has its id and prev_hash.
type packer func(rec *schemas.ReleaseRecord) ([]packFile, error)

// writeEvidence builds the pack, writes it atomically and records its hash.
func (l *Log) writeEvidence(rec *schemas.ReleaseRecord, pack packer, dir string) error {
	files, err := pack(rec)
	if err != nil {
		return fmt.Errorf("create release: evidence pack: %w", err)
	}
	zipped, err := buildPack(files)
	if err != nil {
		return fmt.Errorf("create release: evidence pack: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(dir, EvidenceFile), zipped); err != nil {
		return fmt.Errorf("create release: write evidence pack: %w", err)
	}
	rec.EvidenceSHA256 = utils.SHA256Bytes(zipped)
	return nil
}

// appendRecord hashes rec and appends its canonical JSON as one line, then
// fsyncs. One write per line, so a reader never sees half a record unless the
// write itself was cut short, which VerifyChain reports.
func appendRecord(f *os.File, rec *schemas.ReleaseRecord) error {
	h, err := recordHash(rec)
	if err != nil {
		return fmt.Errorf("create release: %w", err)
	}
	rec.Hash = h
	line, err := utils.CanonicalJSON(rec)
	if err != nil {
		return fmt.Errorf("create release: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("create release: append %s: %w", LogFile, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("create release: sync %s: %w", LogFile, err)
	}
	return nil
}

// recordHash is the sha256 of the record's canonical JSON with hash empty.
func recordHash(rec *schemas.ReleaseRecord) (string, error) {
	c := *rec
	c.Hash = ""
	h, err := utils.HashJSON(&c)
	if err != nil {
		return "", fmt.Errorf("hash release record: %w", err)
	}
	return h, nil
}

// readLog reads the whole log without locking: it is append-only, so a
// reader sees a prefix, at worst with a torn last line.
func (l *Log) readLog() ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(l.d.Root, LogFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", LogFile, err)
	}
	return b, nil
}

// parseChain decodes every line and verifies the chain. It returns the
// records it could decode, in log order, and the first problem found wrapped
// in ErrChainBroken, or nil when the chain verifies.
func parseChain(b []byte) ([]schemas.ReleaseRecord, error) {
	var recs []schemas.ReleaseRecord
	var first error
	fail := func(format string, args ...any) {
		if first == nil {
			first = fmt.Errorf("%w: "+format, append([]any{ErrChainBroken}, args...)...)
		}
	}
	if len(b) == 0 {
		return nil, nil
	}
	lines := bytes.Split(b, []byte{'\n'})
	prev := ""
	seen := map[string]bool{}
	for i, line := range lines {
		n := i + 1
		if i == len(lines)-1 {
			if len(line) > 0 {
				fail("line %d has no newline: a torn write", n)
			}
			break
		}
		rec, err := decodeLine(line)
		if err != nil {
			fail("line %d: %v", n, err)
			continue
		}
		recs = append(recs, *rec)
		if rec.PrevHash != prev {
			fail("line %d: prev_hash %q, want %q", n, rec.PrevHash, prev)
		}
		if h, err := recordHash(rec); err != nil || h != rec.Hash {
			fail("line %d: release %s does not hash to its hash field: the record was edited", n, rec.ReleaseID)
		}
		if seen[rec.ReleaseID] {
			fail("line %d: release %s appears twice", n, rec.ReleaseID)
		}
		seen[rec.ReleaseID] = true
		prev = rec.Hash
	}
	return recs, first
}

// decodeLine decodes one strict record line: known fields only, and in the
// canonical form the log writes, so no byte of a line goes unchecked.
func decodeLine(line []byte) (*schemas.ReleaseRecord, error) {
	if len(line) == 0 {
		return nil, errors.New("empty line")
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	var rec schemas.ReleaseRecord
	if err := dec.Decode(&rec); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	canon, err := utils.CanonicalJSON(&rec)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(canon, line) {
		return nil, fmt.Errorf("release %s is not in canonical form: the line was edited", rec.ReleaseID)
	}
	return &rec, nil
}

// writeFileAtomic writes a temp file beside target, fsyncs it and renames it
// over target, then fsyncs the folder.
func writeFileAtomic(target string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", filepath.Base(target), err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	if werr == nil {
		werr = tmp.Chmod(0o444)
	}
	if err := errors.Join(werr, tmp.Close()); err != nil {
		return errors.Join(fmt.Errorf("write %s: %w", filepath.Base(target), err), os.Remove(name))
	}
	if err := os.Rename(name, target); err != nil {
		return errors.Join(fmt.Errorf("rename %s: %w", filepath.Base(target), err), os.Remove(name))
	}
	d, err := os.Open(filepath.Dir(target))
	if err != nil {
		return fmt.Errorf("open dir of %s: %w", filepath.Base(target), err)
	}
	if err := errors.Join(d.Sync(), d.Close()); err != nil {
		return fmt.Errorf("sync dir of %s: %w", filepath.Base(target), err)
	}
	return nil
}
