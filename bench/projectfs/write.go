package projectfs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// tmpPrefix names the temporary files of an atomic write; List hides them.
const tmpPrefix = ".pfs-"

// rootLocks serialize writes per project root, so an If-Match check, the
// rename and the version record happen as one step even across Store values
// opened over the same folder.
var rootLocks sync.Map // root -> *sync.Mutex

func (s *store) lock() func() {
	m, _ := rootLocks.LoadOrStore(s.root, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// writeAtomic writes data to full through a temporary file and a rename.
func writeAtomic(full string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), tmpPrefix+"*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), full); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// checkMatch compares the current file's hash with ifMatch ("" = any).
func checkMatch(full, ifMatch string) error {
	if ifMatch == "" {
		return nil
	}
	cur, err := os.ReadFile(full)
	if err != nil || sum(cur) != ifMatch {
		return ErrConflict
	}
	return nil
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
	if st, err := os.Stat(full); err == nil && st.IsDir() {
		return Entry{}, ErrInvalid
	}
	if err := validate(a, c, data); err != nil {
		return Entry{}, err
	}
	unlock := s.lock()
	defer unlock()
	if err := checkMatch(full, o.IfMatch); err != nil {
		return Entry{}, err
	}
	if err := writeAtomic(full, data); err != nil {
		return Entry{}, err
	}
	e := Entry{Path: c, Size: int64(len(data)), ModTime: time.Now().UTC(), SHA256: sum(data)}
	if err := s.recordVersion(a, c, data, o, false); err != nil {
		return e, err
	}
	return e, nil
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
	unlock := s.lock()
	defer unlock()
	st, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if st.IsDir() {
		if o.IfMatch != "" {
			return ErrInvalid
		}
		return os.RemoveAll(full)
	}
	if err := checkMatch(full, o.IfMatch); err != nil {
		return err
	}
	if err := os.Remove(full); err != nil {
		return err
	}
	return s.recordVersion(a, c, nil, o, true)
}
