package sharedpool

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

func init() { db.Register("sharedpool", migrations) }

// Image request states.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
)

// MaxNoteLen caps a request note or decision.
const MaxNoteLen = 500

// ImageRequest is one org's request for a custom image.
type ImageRequest struct {
	ID          string     `json:"id"`
	OrgID       string     `json:"org_id"`
	Image       string     `json:"image"`
	SizeBytes   int64      `json:"size_bytes"`
	Note        string     `json:"note,omitempty"`
	Status      string     `json:"status"`
	RequestedBy string     `json:"requested_by,omitempty"`
	DecidedBy   string     `json:"decided_by,omitempty"`
	Decision    string     `json:"decision,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	DecidedAt   *time.Time `json:"decided_at,omitempty"`
}

// ImageStore keeps image requests in the database.
type ImageStore struct {
	DB  *db.DB
	Now func() time.Time
}

func (s *ImageStore) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

const imageCols = `id, org_id, image, size_bytes, note, status, requested_by, decided_by, decision, created_at, decided_at`

func scanImage(r interface{ Scan(...any) error }) (*ImageRequest, error) {
	var v ImageRequest
	var created int64
	var decided sql.NullInt64
	if err := r.Scan(&v.ID, &v.OrgID, &v.Image, &v.SizeBytes, &v.Note, &v.Status, &v.RequestedBy, &v.DecidedBy, &v.Decision, &created, &decided); err != nil {
		return nil, err
	}
	v.CreatedAt = db.Time(created)
	if decided.Valid {
		t := db.Time(decided.Int64)
		v.DecidedAt = &t
	}
	return &v, nil
}

func cleanNote(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) > MaxNoteLen {
		return "", fmt.Errorf("note must be at most %d characters", MaxNoteLen)
	}
	return s, nil
}

// Request files a pending request. The caller checks the digest pin and
// the size limit first (CheckImageRef, CheckImageSize). A second pending or
// approved request for the same image returns the existing one.
func (s *ImageStore) Request(ctx context.Context, orgID, image string, size int64, note, actor string) (*ImageRequest, error) {
	note, err := cleanNote(note)
	if err != nil {
		return nil, err
	}
	row := s.DB.QueryRow(ctx, `SELECT `+imageCols+` FROM image_requests WHERE org_id = ? AND image = ? AND status <> ? ORDER BY created_at DESC LIMIT 1`,
		orgID, image, StatusRejected)
	if v, err := scanImage(row); err == nil {
		return v, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read image requests: %w", err)
	}
	id := db.NewID("img")
	if _, err := s.DB.Exec(ctx, `INSERT INTO image_requests (id, org_id, image, size_bytes, note, status, requested_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, orgID, image, size, note, StatusPending, actor, db.Millis(s.now())); err != nil {
		return nil, fmt.Errorf("file image request: %w", err)
	}
	return s.Get(ctx, id)
}

// Get returns one request.
func (s *ImageStore) Get(ctx context.Context, id string) (*ImageRequest, error) {
	v, err := scanImage(s.DB.QueryRow(ctx, `SELECT `+imageCols+` FROM image_requests WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read image request: %w", err)
	}
	return v, nil
}

// List returns requests, newest first: one org's (orgID != "") or every
// org's, optionally only those in one status.
func (s *ImageStore) List(ctx context.Context, orgID, status string) ([]ImageRequest, error) {
	q := `SELECT ` + imageCols + ` FROM image_requests WHERE 1 = 1`
	var args []any
	if orgID != "" {
		q += ` AND org_id = ?`
		args = append(args, orgID)
	}
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT 500`
	rows, err := s.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list image requests: %w", err)
	}
	defer rows.Close()
	out := []ImageRequest{}
	for rows.Next() {
		v, err := scanImage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// Decide approves or rejects a pending request.
func (s *ImageStore) Decide(ctx context.Context, id string, approve bool, actor, decision string) (*ImageRequest, error) {
	decision, err := cleanNote(decision)
	if err != nil {
		return nil, err
	}
	status := StatusRejected
	if approve {
		status = StatusApproved
	}
	res, err := s.DB.Exec(ctx, `UPDATE image_requests SET status = ?, decided_by = ?, decision = ?, decided_at = ? WHERE id = ? AND status = ?`,
		status, actor, decision, db.Millis(s.now()), id, StatusPending)
	if err != nil {
		return nil, fmt.Errorf("decide image request: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := s.Get(ctx, id); err != nil {
			return nil, err
		}
		return nil, ErrNotPending
	}
	return s.Get(ctx, id)
}

// Approved lists an org's approved images.
func (s *ImageStore) Approved(ctx context.Context, orgID string) ([]string, error) {
	reqs, err := s.List(ctx, orgID, StatusApproved)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, r.Image)
	}
	return out, nil
}
