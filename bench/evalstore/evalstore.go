// Package evalstore keeps evaluations (schemas.Evaluation) as one JSON file
// each under the project's hidden folder: <root>/.heimdall/evaluations/<id>.json.
// The root is the org's project root, so in hosted mode every org has its own
// folder. projectfs never serves .heimdall, so these files are reachable only
// through the evaluations API.
package evalstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ErrNotFound is returned for an unknown evaluation id.
var ErrNotFound = errors.New("evaluation not found")

var idRe = regexp.MustCompile(`^ev_[a-z0-9_]{1,64}$`)

// ValidID reports whether id is a well-formed evaluation id.
func ValidID(id string) bool { return idRe.MatchString(id) }

// Dir is the evaluations folder of a project root.
func Dir(root string) string { return filepath.Join(root, ".heimdall", "evaluations") }

var locks sync.Map // dir -> *sync.Mutex

func lockFor(dir string) *sync.Mutex {
	m, _ := locks.LoadOrStore(dir, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// Store reads and writes the evaluations of one project root.
type Store struct{ dir string }

// Open returns the store of project root.
func Open(root string) *Store { return &Store{dir: Dir(root)} }

// Save writes ev atomically.
func (s *Store) Save(ev *schemas.Evaluation) error {
	if !ValidID(ev.ID) {
		return fmt.Errorf("evaluation id %q is not valid", ev.ID)
	}
	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return err
	}
	mu := lockFor(s.dir)
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, "."+ev.ID+"-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(s.dir, ev.ID+".json"))
}

// Load reads one evaluation.
func (s *Store) Load(id string) (*schemas.Evaluation, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	data, err := os.ReadFile(filepath.Join(s.dir, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	var ev schemas.Evaluation
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, fmt.Errorf("read evaluation %s: %w", id, err)
	}
	return &ev, nil
}

// List returns every readable evaluation, newest first. Unreadable files are
// skipped.
func (s *Store) List() ([]*schemas.Evaluation, error) {
	ents, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*schemas.Evaluation
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		if ev, err := s.Load(strings.TrimSuffix(name, ".json")); err == nil {
			out = append(out, ev)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// Delete removes one evaluation (lane X16). ErrNotFound when it is not
// there.
func (s *Store) Delete(id string) error {
	if !ValidID(id) {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	mu := lockFor(s.dir)
	mu.Lock()
	defer mu.Unlock()
	err := os.Remove(filepath.Join(s.dir, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return err
}
