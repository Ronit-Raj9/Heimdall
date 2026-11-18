package evidencestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// tmpPrefix starts the name of a file a Dir write has not finished. List
// skips such files and nothing else is allowed to begin with it.
const tmpPrefix = ".evtmp-"

// Dir is a Blob over a local folder: a second disk, a mounted network share
// or a test. Every key is a file at the same relative path. Writes go to a
// temporary file that is hashed while it is written, synced and renamed into
// place, so a reader never sees half an object.
type Dir struct {
	root string
}

var _ Blob = (*Dir)(nil)

// NewDir opens (and creates) a mirror folder.
func NewDir(root string) (*Dir, error) {
	if root == "" {
		return nil, errors.New("mirror folder: empty path")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("mirror folder %s: %w", root, err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("mirror folder %s: %w", abs, err)
	}
	return &Dir{root: abs}, nil
}

// String implements Blob.
func (d *Dir) String() string { return "file://" + filepath.ToSlash(d.root) }

// Root returns the absolute folder.
func (d *Dir) Root() string { return d.root }

func (d *Dir) path(key string) (string, error) {
	if err := ValidKey(key); err != nil {
		return "", err
	}
	for _, seg := range strings.Split(key, "/") {
		if strings.HasPrefix(seg, tmpPrefix) {
			return "", fmt.Errorf("invalid object key %q: reserved name", key)
		}
	}
	return filepath.Join(d.root, filepath.FromSlash(key)), nil
}

// Put implements Blob. The bytes are hashed as they are written; a size or
// sha256 that differs from the input fails with ErrChecksum and leaves any
// previous object in place.
func (d *Dir) Put(ctx context.Context, key string, in PutInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if in.Body == nil || in.Size < 0 || len(in.SHA256) != 64 {
		return fmt.Errorf("put %s: body, size and sha256 are required", key)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	f, err := os.CreateTemp(filepath.Dir(p), tmpPrefix+"*")
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), &ctxReader{ctx: ctx, r: io.NewSectionReader(in.Body, 0, in.Size)})
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	if n != in.Size {
		return fmt.Errorf("put %s: wrote %d bytes, want %d: %w", key, n, in.Size, ErrChecksum)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != in.SHA256 {
		return fmt.Errorf("put %s: body hashes to %s, want %s: %w", key, got, in.SHA256, ErrChecksum)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	ok = true
	return syncDir(filepath.Dir(p))
}

// Get implements Blob. The info carries the size; the sha256 is left for the
// reader to compute (VerifyingReader), since a local folder records none.
func (d *Dir) Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, ObjectInfo{}, err
	}
	p, err := d.path(key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ObjectInfo{}, fmt.Errorf("get %s: %w", key, ErrNotFound)
		}
		return nil, ObjectInfo{}, fmt.Errorf("get %s: %w", key, err)
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, ObjectInfo{}, fmt.Errorf("get %s: not a regular file: %w", key, ErrNotFound)
	}
	return f, ObjectInfo{Key: key, Size: st.Size(), LastModified: st.ModTime().UTC()}, nil
}

// Head implements Blob. A local folder records no checksum, so Head hashes
// the file: the claim it returns is the file's real sha256.
func (d *Dir) Head(ctx context.Context, key string) (ObjectInfo, error) {
	rc, info, err := d.Get(ctx, key)
	if err != nil {
		return ObjectInfo{}, err
	}
	defer rc.Close()
	h := sha256.New()
	if _, err := io.Copy(h, &ctxReader{ctx: ctx, r: rc}); err != nil {
		return ObjectInfo{}, fmt.Errorf("head %s: %w", key, err)
	}
	info.SHA256 = hex.EncodeToString(h.Sum(nil))
	return info, nil
}

// List implements Blob.
func (d *Dir) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	if err := ValidPrefix(prefix); err != nil {
		return nil, err
	}
	// Walk the deepest folder the prefix names in full, then filter by the
	// string prefix the way object storage does.
	start := d.root
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		start = filepath.Join(d.root, filepath.FromSlash(prefix[:i]))
	}
	var out []ObjectInfo
	err := filepath.WalkDir(start, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				if p == start {
					return fs.SkipAll // nothing under the prefix
				}
				return nil // removed while listing
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(e.Name(), tmpPrefix) || !e.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(d.root, p)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		st, err := e.Info()
		if err != nil {
			return err
		}
		out = append(out, ObjectInfo{Key: key, Size: st.Size(), LastModified: st.ModTime().UTC()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", prefix, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Delete implements Blob. Folders left empty are removed up to the root.
func (d *Dir) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	for dir := filepath.Dir(p); dir != d.root && strings.HasPrefix(dir, d.root); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			break
		}
	}
	return nil
}

// ctxReader stops a copy when its context ends.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	defer f.Close()
	if err := f.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	return nil
}

// VerifyingReader wraps an object body. It hashes everything read and, at
// the end of the body, returns ErrChecksum instead of io.EOF when the size
// or sha256 differs from want, so a caller that copies to EOF can not miss a
// changed object.
func VerifyingReader(r io.Reader, wantSize int64, wantSHA256 string) io.Reader {
	return &verifyingReader{r: r, h: sha256.New(), size: wantSize, sha: wantSHA256}
}

type verifyingReader struct {
	r    io.Reader
	h    hash.Hash
	n    int64
	size int64
	sha  string
}

func (v *verifyingReader) Read(p []byte) (int, error) {
	n, err := v.r.Read(p)
	if n > 0 {
		v.h.Write(p[:n])
		v.n += int64(n)
		if v.n > v.size {
			return n, fmt.Errorf("object longer than %d bytes: %w", v.size, ErrChecksum)
		}
	}
	if errors.Is(err, io.EOF) {
		if v.n != v.size {
			return n, fmt.Errorf("object is %d bytes, want %d: %w", v.n, v.size, ErrChecksum)
		}
		if got := hex.EncodeToString(v.h.Sum(nil)); got != v.sha {
			return n, fmt.Errorf("object hashes to %s, want %s: %w", got, v.sha, ErrChecksum)
		}
	}
	return n, err
}
