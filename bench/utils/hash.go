package utils

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SHA256Bytes returns the hex sha256 of b.
func SHA256Bytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// SHA256File returns the hex sha256 of the file at path.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// TreeHash returns a sha256 over every regular file under dir: relative path,
// a NUL, the file's sha256, a newline, in sorted path order. Symlinks and
// other non-regular files are hashed by their mode and link target only.
func TreeHash(dir string) (string, error) {
	files, err := HashFiles(dir)
	if err != nil {
		return "", err
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var buf bytes.Buffer
	for _, p := range paths {
		buf.WriteString(p)
		buf.WriteByte(0)
		buf.WriteString(files[p])
		buf.WriteByte('\n')
	}
	return SHA256Bytes(buf.Bytes()), nil
}

// HashFiles returns relative path -> sha256 for every file under dir.
func HashFiles(dir string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			out[rel] = SHA256Bytes([]byte("symlink:" + target))
			return nil
		}
		if !d.Type().IsRegular() {
			out[rel] = SHA256Bytes([]byte("special:" + d.Type().String()))
			return nil
		}
		sum, err := SHA256File(path)
		if err != nil {
			return err
		}
		out[rel] = sum
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("hash tree %s: %w", dir, err)
	}
	return out, nil
}

// CanonicalTreeHash hashes a source tree with the benchmark's task-package
// rules. Paths use slash separators and bytewise ordering. Regular-file bytes
// are read unchanged, including binary data and newlines. Modes are reduced to
// 0644 or 0755 so umask differences do not change a task hash. Symlinks are
// hashed as links and are never followed. Generated, repository, and
// secret-bearing paths are excluded.
func CanonicalTreeHash(dir string) (string, error) {
	entries := make([]canonicalTreeEntry, 0)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if shouldExcludeCanonicalPath(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		entry, err := canonicalTreeEntryFor(path, rel, d)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash canonical tree %s: %w", dir, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	b, err := CanonicalJSON(entries)
	if err != nil {
		return "", fmt.Errorf("encode canonical tree %s: %w", dir, err)
	}
	return SHA256Bytes(b), nil
}

type canonicalTreeEntry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Mode   uint32 `json:"mode"`
	Digest string `json:"digest"`
}

func canonicalTreeEntryFor(path, rel string, d fs.DirEntry) (canonicalTreeEntry, error) {
	entry := canonicalTreeEntry{Path: rel}
	if d.Type()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return canonicalTreeEntry{}, fmt.Errorf("read symlink %s: %w", rel, err)
		}
		entry.Kind, entry.Mode = "symlink", 0o777
		entry.Digest = SHA256Bytes([]byte(target))
		return entry, nil
	}
	info, err := d.Info()
	if err != nil {
		return canonicalTreeEntry{}, fmt.Errorf("stat %s: %w", rel, err)
	}
	if !info.Mode().IsRegular() {
		entry.Kind, entry.Mode = "special", uint32(info.Mode().Type())
		entry.Digest = SHA256Bytes([]byte(info.Mode().String()))
		return entry, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return canonicalTreeEntry{}, fmt.Errorf("read %s: %w", rel, err)
	}
	entry.Kind = "file"
	entry.Mode = 0o644
	if info.Mode().Perm()&0o111 != 0 {
		entry.Mode = 0o755
	}
	entry.Digest = SHA256Bytes(b)
	return entry, nil
}

func shouldExcludeCanonicalPath(rel string, d fs.DirEntry) bool {
	parts := strings.Split(rel, "/")
	for _, part := range parts {
		lower := strings.ToLower(part)
		switch lower {
		case ".git", "graphify-out", "runs", ".runs", "node_modules", "__pycache__":
			return true
		}
	}
	base := strings.ToLower(filepath.Base(rel))
	if d.IsDir() {
		return false
	}
	if base == ".ds_store" || strings.HasSuffix(base, ".pyc") || strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, ".swp") {
		return true
	}
	if strings.HasPrefix(base, ".env") || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") {
		return true
	}
	for _, marker := range []string{"secret", "credential", "password"} {
		if strings.Contains(base, marker) {
			return true
		}
	}
	return false
}

// CanonicalJSON marshals v with sorted map keys and no HTML escaping, so the
// same value always hashes the same.
func CanonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("canonical json: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// HashJSON returns the sha256 of v's canonical JSON.
func HashJSON(v any) (string, error) {
	b, err := CanonicalJSON(v)
	if err != nil {
		return "", err
	}
	return SHA256Bytes(b), nil
}
