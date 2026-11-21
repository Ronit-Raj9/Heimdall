package store

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// followUpFile is <parent exp>/followup.json: the one follow-up the
// parent's pre-declared combination alpha is bound to.
const followUpFile = "followup.json"

// FollowUpBinding is the content of followup.json. It is written once and
// never replaced, in the parent's folder, so archiving, restoring or deleting
// a child never frees the parent's alpha for another child (audit qc2-03-01).
type FollowUpBinding struct {
	ParentID   string    `json:"parent_id"`
	ParentHash string    `json:"parent_hash"`
	ChildID    string    `json:"child_id"`
	BoundAt    time.Time `json:"bound_at"`
}

// BindFollowUp binds childID as the follow-up of parentID at parentHash
// unless a binding already exists, and returns the bound child: the existing
// one, or childID. A binding recorded for another parent hash is an error.
func (s *FSStore) BindFollowUp(parentID, parentHash, childID string) (string, error) {
	if err := validateID("exp_id", parentID); err != nil {
		return "", fmt.Errorf("bind follow-up: %w", err)
	}
	if err := validateID("exp_id", childID); err != nil {
		return "", fmt.Errorf("bind follow-up: %w", err)
	}
	if got, ok, err := s.FollowUp(parentID, parentHash); err != nil || ok {
		return got, err
	}
	var parent schemas.Manifest
	found, err := readOptionalJSON(filepath.Join(s.expDir(parentID), manifestFile), &parent)
	if err != nil {
		return "", fmt.Errorf("bind follow-up: %w", err)
	}
	if !found {
		return "", fmt.Errorf("bind follow-up: experiment %s: %w", parentID, schemas.ErrNotFound)
	}
	if parent.Hash != parentHash {
		// A child of another freeze of the parent never joins its
		// combination (verdict.AuthorizedFollowUp), so it binds nothing.
		return "", fmt.Errorf("bind follow-up: %s extends %s at %s, its manifest is %s: %w", childID, parentID, parentHash, parent.Hash, schemas.ErrNotFound)
	}
	b, err := marshalIndent(&FollowUpBinding{ParentID: parentID, ParentHash: parentHash, ChildID: childID, BoundAt: s.now().UTC()})
	if err != nil {
		return "", fmt.Errorf("bind follow-up: %w", err)
	}
	if err := writeFileExclusive(filepath.Join(s.expDir(parentID), followUpFile), b); err != nil {
		if errors.Is(err, fs.ErrExist) || errors.Is(err, schemas.ErrExists) {
			got, _, err := s.FollowUp(parentID, parentHash) // a concurrent bind won
			return got, err
		}
		return "", fmt.Errorf("bind follow-up %s: %w", parentID, err)
	}
	return childID, nil
}

// FollowUp returns the child bound to parentID, if any.
func (s *FSStore) FollowUp(parentID, parentHash string) (string, bool, error) {
	if err := validateID("exp_id", parentID); err != nil {
		return "", false, fmt.Errorf("follow-up: %w", err)
	}
	var b FollowUpBinding
	found, err := readOptionalJSON(filepath.Join(s.expDir(parentID), followUpFile), &b)
	if err != nil || !found {
		return "", false, err
	}
	if b.ParentHash != parentHash {
		return "", false, fmt.Errorf("follow-up binding of %s names parent hash %s, the manifest is %s: %w", parentID, b.ParentHash, parentHash, schemas.ErrSealMismatch)
	}
	return b.ChildID, true, nil
}
