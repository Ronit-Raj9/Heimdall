package projectfs

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"
)

// skippedZipEntry reports archive clutter that is dropped silently.
func skippedZipEntry(name string) bool {
	base := path.Base(name)
	return strings.HasPrefix(name, "__MACOSX/") || base == ".DS_Store" || base == "Thumbs.db"
}

// ExtractZip unpacks the archive r into dir of area a. Every entry name is
// checked before anything is written (absolute paths, "..", .git,
// .heimdall, symlinks and symlink escapes refuse the whole archive), and the
// archive is bounded by MaxZipBytes (compressed and uncompressed) and
// MaxZipEntries. YAML files go through the registered validators. Files up
// to MaxFileBytes are versioned like Write.
func (s *store) ExtractZip(ctx context.Context, a Area, dir string, r io.ReaderAt, size int64, o WriteOpts) ([]Entry, error) {
	if size > MaxZipBytes {
		return nil, ErrTooLarge
	}
	if _, err := s.Resolve(a, dir); err != nil {
		return nil, err
	}
	cdir, _ := cleanRel(dir)
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("%w: not a zip archive: %v", ErrInvalid, err)
	}
	if len(zr.File) > MaxZipEntries {
		return nil, fmt.Errorf("%w: archive has %d entries, the limit is %d", ErrTooLarge, len(zr.File), MaxZipEntries)
	}
	type item struct {
		f    *zip.File
		rel  string
		full string
	}
	var items []item
	var declared uint64
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if skippedZipEntry(name) {
			continue
		}
		if f.Mode()&fs.ModeSymlink != 0 || (f.Mode()&fs.ModeType != 0 && !f.FileInfo().IsDir()) {
			return nil, fmt.Errorf("%w: %s: links and special files are not allowed", ErrOutside, f.Name)
		}
		if strings.HasPrefix(name, "/") || (len(name) > 1 && name[1] == ':') {
			return nil, fmt.Errorf("%w: %s", ErrOutside, f.Name)
		}
		if _, err := cleanRel(name); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrOutside, f.Name)
		}
		rel := path.Join(cdir, name)
		full, err := s.Resolve(a, rel)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", err, f.Name)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		declared += f.UncompressedSize64
		if declared > MaxZipBytes {
			return nil, fmt.Errorf("%w: archive expands past %d bytes", ErrTooLarge, MaxZipBytes)
		}
		c, _ := cleanRel(rel)
		items = append(items, item{f, c, full})
	}
	// Read and validate everything before the first write.
	datas := make([][]byte, len(items))
	var total int64
	for i, it := range items {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		rc, err := it.f.Open()
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalid, it.f.Name, err)
		}
		data, err := io.ReadAll(io.LimitReader(rc, MaxZipBytes-total+1))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalid, it.f.Name, err)
		}
		total += int64(len(data))
		if total > MaxZipBytes {
			return nil, fmt.Errorf("%w: archive expands past %d bytes", ErrTooLarge, MaxZipBytes)
		}
		if err := validate(a, it.rel, data); err != nil {
			return nil, err
		}
		datas[i] = data
	}
	unlock := s.lock()
	defer unlock()
	out := make([]Entry, 0, len(items))
	for i, it := range items {
		if st, err := os.Lstat(it.full); err == nil && st.IsDir() {
			return out, fmt.Errorf("%w: %s is a folder in the project", ErrInvalid, it.rel)
		}
		if err := writeAtomic(it.full, datas[i]); err != nil {
			return out, err
		}
		if len(datas[i]) <= MaxFileBytes {
			if err := s.recordVersion(a, it.rel, datas[i], o, false); err != nil {
				return out, err
			}
		}
		out = append(out, Entry{Path: it.rel, Size: int64(len(datas[i])), ModTime: time.Now().UTC(), SHA256: sum(datas[i])})
	}
	return out, nil
}
