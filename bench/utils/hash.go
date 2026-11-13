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
