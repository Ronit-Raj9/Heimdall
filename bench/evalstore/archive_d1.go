package evalstore

import (
	"encoding/json"

	"os"
	"path/filepath"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// storedArchived is the archive mark of the stored file of id, empty when
// the file is missing or unreadable. The caller holds the lock.
func (s *Store) storedArchived(id string) schemas.Archived {
	data, err := os.ReadFile(filepath.Join(s.dir, id+".json"))
	if err != nil {
		return schemas.Archived{}
	}
	var ev struct {
		schemas.Archived
	}
	if json.Unmarshal(data, &ev) != nil {
		return schemas.Archived{}
	}
	return ev.Archived
}

// SetArchived archives (by not empty) or unarchives (by empty, at ignored)
// evaluation id and returns it as stored. Only the archive fields change;
// nothing else of the record is touched (UI fix wave D1, decision 6).
func (s *Store) SetArchived(id string, archive bool, by string, at time.Time) (*schemas.Evaluation, error) {
	mu := lockFor(s.dir)
	mu.Lock()
	defer mu.Unlock()
	ev, err := s.Load(id)
	if err != nil {
		return nil, err
	}
	if archive {
		if !ev.IsArchived() {
			t := at.UTC()
			ev.Archived = schemas.Archived{ArchivedAt: &t, ArchivedBy: by}
		}
	} else {
		ev.Archived = schemas.Archived{}
	}
	if err := s.write(ev); err != nil {
		return nil, err
	}
	return ev, nil
}
