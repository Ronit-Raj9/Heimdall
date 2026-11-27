package schemas

import "time"

// Archived marks an object a person put away (UI fix wave D1, decision 6):
// lists leave it out unless asked, it still opens by id, and nothing is
// deleted. Archiving never changes a seal, hash, score or verdict: every
// computed number (leaderboards, verdicts, usage, spend) still counts it.
// Both fields are empty while the object is not archived.
type Archived struct {
	ArchivedAt *time.Time `json:"archived_at,omitempty"` // RFC 3339
	ArchivedBy string     `json:"archived_by,omitempty"` // the actor id who archived it
}

// IsArchived reports whether the object is archived.
func (a Archived) IsArchived() bool { return a.ArchivedAt != nil }
