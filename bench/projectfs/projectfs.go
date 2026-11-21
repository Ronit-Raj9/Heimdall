// Package projectfs is the confined file store over an org's project folder:
// agents, benchmarks, experiments, policies and uploads. Phase 0 skeleton;
// lane L01 completes versioning, validation and zip extraction.
package projectfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Area is one top-level folder of the project.
type Area string // "agents","benchmarks","experiments","policies","uploads"

// The project areas.
const (
	AreaAgents      Area = "agents"
	AreaBenchmarks  Area = "benchmarks"
	AreaExperiments Area = "experiments"
	AreaPolicies    Area = "policies"
	AreaUploads     Area = "uploads"
)

// Entry is one file or folder.
type Entry struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Dir     bool      `json:"dir"`
	ModTime time.Time `json:"mod_time"`
	SHA256  string    `json:"sha256,omitempty"`
}

// Version is one stored version of a file.
type Version struct {
	ID, Path, SHA256, Actor, Note string
	Size                          int64
	At                            time.Time
}

// WriteOpts carry the actor, a note and an optimistic-lock hash.
type WriteOpts struct{ Actor, Note, IfMatch string } // IfMatch = expected sha256 ("" = any)

// Store is the confined project file store.
type Store interface {
	Root() string
	Resolve(a Area, rel string) (string, error) // rejects abs, "..", symlink escape, .git, .heimdall
	List(ctx context.Context, a Area, dir string, recursive bool) ([]Entry, error)
	Read(ctx context.Context, a Area, rel string) ([]byte, Entry, error)
	Write(ctx context.Context, a Area, rel string, data []byte, o WriteOpts) (Entry, error) // atomic, validated, versioned
	Delete(ctx context.Context, a Area, rel string, o WriteOpts) error
	Versions(ctx context.Context, a Area, rel string) ([]Version, error)
	ReadVersion(ctx context.Context, a Area, rel, id string) ([]byte, error)
	ExtractZip(ctx context.Context, a Area, dir string, r io.ReaderAt, size int64, o WriteOpts) ([]Entry, error)
}

// Errors.
var (
	ErrOutside        = errors.New("projectfs: path outside the project area")
	ErrTooLarge       = errors.New("projectfs: file too large")
	ErrInvalid        = errors.New("projectfs: invalid file")
	ErrConflict       = errors.New("projectfs: file changed since it was read")
	ErrNotFound       = errors.New("projectfs: not found")
	ErrNotImplemented = errors.New("projectfs: not implemented")
)

// Limits.
const (
	MaxFileBytes  = 1 << 20
	MaxZipBytes   = 50 << 20
	MaxZipEntries = 5000
)

// Validator checks a file before it is written.
type Validator func(a Area, rel string, data []byte) error

type validatorEntry struct {
	area Area
	glob string
	v    Validator
}

var (
	validatorsMu sync.RWMutex
	validators   []validatorEntry
)

// RegisterValidator adds a validator for files of area a matching glob
// (path.Match against the area-relative path). Lanes call it from init().
func RegisterValidator(a Area, glob string, v Validator) {
	validatorsMu.Lock()
	defer validatorsMu.Unlock()
	validators = append(validators, validatorEntry{a, glob, v})
}

func validate(a Area, rel string, data []byte) error {
	validatorsMu.RLock()
	defer validatorsMu.RUnlock()
	for _, e := range validators {
		if e.area != a {
			continue
		}
		if ok, _ := path.Match(e.glob, rel); !ok {
			continue
		}
		if err := e.v(a, rel, data); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	}
	return nil
}

func validArea(a Area) bool {
	switch a {
	case AreaAgents, AreaBenchmarks, AreaExperiments, AreaPolicies, AreaUploads:
		return true
	}
	return false
}

type store struct{ root string }

// Open returns the store over projectRoot.
func Open(projectRoot string) (Store, error) {
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("open project store: %w", err)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return &store{root: abs}, nil
}

func (s *store) Root() string { return s.root }

// cleanRel normalizes rel and rejects absolute paths, "..", .git and .heimdall.
func cleanRel(rel string) (string, error) {
	rel = strings.ReplaceAll(rel, "\\", "/")
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) || strings.ContainsRune(rel, 0) {
		return "", ErrOutside
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return "", ErrOutside
		}
		if part == ".git" || part == ".heimdall" {
			return "", ErrOutside
		}
	}
	c := path.Clean("/" + rel)[1:]
	return c, nil
}

func within(base, p string) bool {
	return p == base || strings.HasPrefix(p, base+string(os.PathSeparator))
}

func (s *store) Resolve(a Area, rel string) (string, error) {
	if !validArea(a) {
		return "", ErrOutside
	}
	c, err := cleanRel(rel)
	if err != nil {
		return "", err
	}
	base := filepath.Join(s.root, string(a))
	full := filepath.Join(base, filepath.FromSlash(c))
	if !within(base, full) {
		return "", ErrOutside
	}
	// Symlink escape: resolve the longest existing prefix.
	probe := full
	for {
		real, err := filepath.EvalSymlinks(probe)
		if err == nil {
			realBase, berr := filepath.EvalSymlinks(base)
			if berr != nil {
				realBase = base
			}
			if !within(realBase, real) {
				return "", ErrOutside
			}
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe || !within(s.root, parent) {
			break
		}
		probe = parent
	}
	return full, nil
}

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func (s *store) List(ctx context.Context, a Area, dir string, recursive bool) ([]Entry, error) {
	full, err := s.Resolve(a, dir)
	if err != nil {
		return nil, err
	}
	base := filepath.Join(s.root, string(a))
	st, err := os.Stat(full)
	if errors.Is(err, fs.ErrNotExist) {
		if dir == "" || dir == "." {
			return []Entry{}, nil
		}
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, ErrInvalid
	}
	out := []Entry{}
	walk := func(p string, d fs.DirEntry) error {
		name := d.Name()
		if name == ".git" || name == ".heimdall" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(base, p)
		out = append(out, Entry{Path: filepath.ToSlash(rel), Size: info.Size(), Dir: d.IsDir(), ModTime: info.ModTime()})
		return nil
	}
	if recursive {
		err = filepath.WalkDir(full, func(p string, d fs.DirEntry, err error) error {
			if err != nil || p == full {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return walk(p, d)
		})
	} else {
		var ents []fs.DirEntry
		ents, err = os.ReadDir(full)
		for _, d := range ents {
			_ = walk(filepath.Join(full, d.Name()), d)
		}
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (s *store) Read(ctx context.Context, a Area, rel string) ([]byte, Entry, error) {
	full, err := s.Resolve(a, rel)
	if err != nil {
		return nil, Entry{}, err
	}
	st, err := os.Stat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, Entry{}, ErrNotFound
	}
	if err != nil {
		return nil, Entry{}, err
	}
	if st.IsDir() {
		return nil, Entry{}, ErrInvalid
	}
	if st.Size() > MaxFileBytes {
		return nil, Entry{}, ErrTooLarge
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, Entry{}, err
	}
	c, _ := cleanRel(rel)
	return data, Entry{Path: c, Size: int64(len(data)), ModTime: st.ModTime(), SHA256: sum(data)}, nil
}

func (s *store) Write(ctx context.Context, a Area, rel string, data []byte, o WriteOpts) (Entry, error) {
	if len(data) > MaxFileBytes {
		return Entry{}, ErrTooLarge
	}
	full, err := s.Resolve(a, rel)
	if err != nil {
		return Entry{}, err
	}
	c, _ := cleanRel(rel)
	if c == "" || c == "." {
		return Entry{}, ErrInvalid
	}
	if o.IfMatch != "" {
		cur, err := os.ReadFile(full)
		if err != nil || sum(cur) != o.IfMatch {
			return Entry{}, ErrConflict
		}
	}
	if err := validate(a, c, data); err != nil {
		return Entry{}, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return Entry{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".pfs-*")
	if err != nil {
		return Entry{}, err
	}
	if _, err := io.Copy(tmp, bytes.NewReader(data)); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return Entry{}, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return Entry{}, err
	}
	if err := os.Rename(tmp.Name(), full); err != nil {
		os.Remove(tmp.Name())
		return Entry{}, err
	}
	return Entry{Path: c, Size: int64(len(data)), ModTime: time.Now(), SHA256: sum(data)}, nil
}

func (s *store) Delete(ctx context.Context, a Area, rel string, o WriteOpts) error {
	full, err := s.Resolve(a, rel)
	if err != nil {
		return err
	}
	c, _ := cleanRel(rel)
	if c == "" || c == "." {
		return ErrInvalid
	}
	st, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if o.IfMatch != "" && !st.IsDir() {
		cur, err := os.ReadFile(full)
		if err != nil || sum(cur) != o.IfMatch {
			return ErrConflict
		}
	}
	if st.IsDir() {
		return os.RemoveAll(full)
	}
	return os.Remove(full)
}

func (s *store) Versions(ctx context.Context, a Area, rel string) ([]Version, error) {
	return nil, nil
}

func (s *store) ReadVersion(ctx context.Context, a Area, rel, id string) ([]byte, error) {
	return nil, ErrNotImplemented
}

func (s *store) ExtractZip(ctx context.Context, a Area, dir string, r io.ReaderAt, size int64, o WriteOpts) ([]Entry, error) {
	return nil, ErrNotImplemented
}
