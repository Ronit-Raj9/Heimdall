package sharedpool

// Org project transfer (PROD2 P0 isolation (a)): the shared runner no longer
// mounts the server's whole data volume. Each org's worker loop downloads
// only its own org's project with that org's runner token
// (GET /api/bench/workers/project, a gzip tar), before each lease, and
// unpacks it into its own folder. The archive holds suites, agents,
// experiments and policies; never .git, the .heimdall/ state (webhooks,
// tokens) except frozen agent versions, or the org's release log.

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Project transfer limits.
const (
	// MaxProjectBytes caps the unpacked size of one org's project.
	MaxProjectBytes = 2 << 30
	// MaxProjectFiles caps its file count.
	MaxProjectFiles = 200000
	// ProjectPath is the route the shared runner fetches a project from.
	ProjectPath = "/api/bench/workers/project"
)

// ErrProjectTooBig refuses a project above the transfer limits.
var ErrProjectTooBig = errors.New("organization project is larger than the shared worker accepts")

// ProjectSkip reports whether the transfer leaves rel out (dir: the whole
// folder). .git and .heimdall are left out, except the frozen agent version
// snapshots under .heimdall/agent-versions, which a pinned agent
// (agents/<id>.yaml@v<N>) reads when the runner re-plans; so is the
// top-level releases/ folder (the org's release log).
func ProjectSkip(rel string, isDir bool) (skip, dir bool) {
	r := filepath.ToSlash(rel)
	if r == "." {
		return false, false
	}
	keep := filepath.ToSlash(schemas.AgentVersionsDir)
	switch {
	case r == ".heimdall" && isDir:
		return false, false
	case r == keep || strings.HasPrefix(r, keep+"/"):
		return false, false
	case strings.HasPrefix(r, ".heimdall/") || r == ".heimdall":
		return true, isDir
	case r == "releases" && isDir:
		return true, true
	}
	base := filepath.Base(rel)
	if isDir && (base == ".git" || base == ".heimdall") {
		return true, true
	}
	return false, false
}

type projectFile struct {
	rel  string
	dir  bool
	mode fs.FileMode
	size int64
	mod  time.Time
}

// walkProject lists what the transfer holds, sorted, with the limits applied.
func walkProject(root string) ([]projectFile, error) {
	var out []projectFile
	var total int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if skip, dir := ProjectSkip(rel, d.IsDir()); skip {
			if dir {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || (!d.IsDir() && !d.Type().IsRegular()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		f := projectFile{rel: filepath.ToSlash(rel), dir: d.IsDir(), mode: info.Mode().Perm(), mod: info.ModTime().UTC().Truncate(time.Second)}
		if !f.dir {
			f.size = info.Size()
			total += f.size
		}
		out = append(out, f)
		if len(out) > MaxProjectFiles || total > MaxProjectBytes {
			return ErrProjectTooBig
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, nil
}

// ProjectETag is a cheap version of the project: a hash of every
// transferred path with its size, mode and modification time.
func ProjectETag(root string) (string, error) {
	files, err := walkProject(root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%t\x00%o\x00%d\x00%d\n", f.rel, f.dir, f.mode, f.size, f.mod.Unix())
	}
	return `"` + hex.EncodeToString(h.Sum(nil))[:32] + `"`, nil
}

// WriteProject writes root as a gzip tar to w.
func WriteProject(w io.Writer, root string) error {
	files, err := walkProject(root)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		hdr := &tar.Header{Name: f.rel, Mode: int64(f.mode), ModTime: f.mod, Format: tar.FormatPAX}
		if f.dir {
			hdr.Typeflag, hdr.Name = tar.TypeDir, f.rel+"/"
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			continue
		}
		hdr.Typeflag, hdr.Size = tar.TypeReg, f.size
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		src, err := os.Open(filepath.Join(root, filepath.FromSlash(f.rel)))
		if err != nil {
			return err
		}
		_, err = io.CopyN(tw, src, f.size)
		src.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", f.rel, err)
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// ExtractProject unpacks a WriteProject archive into the empty or missing
// folder dst: regular files and folders only, every path inside dst,
// within the transfer limits. Modification times are kept.
func ExtractProject(r io.Reader, dst string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("project archive: %w", err)
	}
	defer gz.Close()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var total int64
	n := 0
	type dirTime struct {
		path string
		mod  time.Time
	}
	var dirs []dirTime
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("project archive: %w", err)
		}
		n++
		if n > MaxProjectFiles {
			return ErrProjectTooBig
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		clean := filepath.Clean(filepath.FromSlash(name))
		if name == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("project archive: unsafe path %q", hdr.Name)
		}
		if skip, _ := ProjectSkip(clean, hdr.Typeflag == tar.TypeDir); skip {
			return fmt.Errorf("project archive: unexpected path %q", hdr.Name)
		}
		out := filepath.Join(dst, clean)
		mode := fs.FileMode(hdr.Mode).Perm() | 0o444
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(out, 0o755); err != nil {
				return err
			}
			dirs = append(dirs, dirTime{out, hdr.ModTime})
		case tar.TypeReg:
			total += hdr.Size
			if hdr.Size < 0 || total > MaxProjectBytes {
				return ErrProjectTooBig
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			if mode&0o111 != 0 {
				mode |= 0o111
			}
			f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			_, err = io.CopyN(f, tr, hdr.Size)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return fmt.Errorf("project archive %s: %w", name, err)
			}
			if err := os.Chmod(out, mode); err != nil {
				return err
			}
			if err := os.Chtimes(out, hdr.ModTime, hdr.ModTime); err != nil {
				return err
			}
		default:
			return fmt.Errorf("project archive: %q is not a file or folder", hdr.Name)
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Chtimes(dirs[i].path, dirs[i].mod, dirs[i].mod)
	}
	return nil
}
