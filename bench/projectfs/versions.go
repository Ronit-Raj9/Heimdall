package projectfs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// Versions live under <root>/.heimdall/versions/: blobs/<sha256> holds each
// distinct content once, index.jsonl has one line per write or delete.
const (
	versionsDir   = ".heimdall/versions"
	indexFile     = "index.jsonl"
	blobsDir      = "blobs"
	deletedNote   = "deleted"
	maxIndexBytes = 64 << 20
)

type indexLine struct {
	Area Area `json:"area"`
	Version
}

var versionSeq atomic.Uint64

func (s *store) versionsRoot() string { return filepath.Join(s.root, filepath.FromSlash(versionsDir)) }

// recordVersion appends one index line (and the blob) for a write or a
// delete. The caller holds the root lock.
func (s *store) recordVersion(a Area, rel string, data []byte, o WriteOpts, deleted bool) error {
	vr := s.versionsRoot()
	now := time.Now().UTC()
	v := Version{
		ID:    fmt.Sprintf("%s-%06d", now.Format("20060102T150405.000000000Z"), versionSeq.Add(1)%1000000),
		Path:  rel,
		Actor: o.Actor,
		Note:  o.Note,
		At:    now,
	}
	if deleted {
		v.Note = deletedNote
	} else {
		v.SHA256 = sum(data)
		v.Size = int64(len(data))
		blob := filepath.Join(vr, blobsDir, v.SHA256)
		if _, err := os.Stat(blob); errors.Is(err, fs.ErrNotExist) {
			if err := writeAtomic(blob, data); err != nil {
				return fmt.Errorf("projectfs: store version: %w", err)
			}
		}
	}
	line, err := json.Marshal(indexLine{Area: a, Version: v})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(vr, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(vr, indexFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("projectfs: version index: %w", err)
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// readIndex returns the index lines of one file, newest first.
func (s *store) readIndex(ctx context.Context, a Area, rel string) ([]Version, error) {
	f, err := os.Open(filepath.Join(s.versionsRoot(), indexFile))
	if errors.Is(err, fs.ErrNotExist) {
		return []Version{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := []Version{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	needle := []byte(`"path":` + mustJSON(rel))
	var read int64
	for sc.Scan() {
		b := sc.Bytes()
		read += int64(len(b))
		if read > maxIndexBytes {
			break
		}
		if !bytes.Contains(b, needle) {
			continue
		}
		var l indexLine
		if json.Unmarshal(b, &l) != nil || l.Area != a || l.Path != rel {
			continue
		}
		out = append(out, l.Version)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// The index is in write order; newest first for callers.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (s *store) Versions(ctx context.Context, a Area, rel string) ([]Version, error) {
	if _, err := s.Resolve(a, rel); err != nil {
		return nil, err
	}
	c, _ := cleanRel(rel)
	if c == "" || c == "." {
		return nil, ErrInvalid
	}
	return s.readIndex(ctx, a, c)
}

func (s *store) ReadVersion(ctx context.Context, a Area, rel, id string) ([]byte, error) {
	vs, err := s.Versions(ctx, a, rel)
	if err != nil {
		return nil, err
	}
	for _, v := range vs {
		if v.ID != id {
			continue
		}
		if v.SHA256 == "" || strings.ContainsAny(v.SHA256, "/\\.") {
			return nil, ErrNotFound
		}
		data, err := os.ReadFile(filepath.Join(s.versionsRoot(), blobsDir, v.SHA256))
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return data, err
	}
	return nil, ErrNotFound
}
