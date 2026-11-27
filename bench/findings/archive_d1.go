package findings

import (
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// SetArchived archives (archive true) or unarchives finding id under the
// store lock (UI fix wave D1, decision 6). Only the archive fields change:
// the state, history and updated_at stay as they were, so archiving never
// moves a finding in the state rules or in the newest-change order.
func SetArchived(st Store, id string, archive bool, by string, at time.Time) (*Finding, error) {
	return Update(st, id, func(f *Finding) error {
		switch {
		case !archive:
			f.Archived = schemas.Archived{}
		case !f.IsArchived():
			t := at.UTC()
			f.Archived = schemas.Archived{ArchivedAt: &t, ArchivedBy: by}
		}
		return nil
	})
}
