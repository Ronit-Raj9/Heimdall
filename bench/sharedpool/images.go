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

// Approval kinds of an approved request.
const (
	// ApprovalAutomatic: the server approved a digest-pinned image within
	// the size limit because the shared worker runs it under gVisor. Such
	// an image is only ever handed to a shared worker running gVisor.
	ApprovalAutomatic = "automatic"
	// ApprovalAdmin: a server admin approved it.
	ApprovalAdmin = "admin"
)

// MaxNoteLen caps a request note or decision.
const MaxNoteLen = 500

// ImageRequest is one org's request for a custom image.
type ImageRequest struct {
	ID          string `json:"id"`
	OrgID       string `json:"org_id"`
	Image       string `json:"image"`
	SizeBytes   int64  `json:"size_bytes"`
	Note        string `json:"note,omitempty"`
	Status      string `json:"status"`
	RequestedBy string `json:"requested_by,omitempty"`
	// RequestedByName and RequestedByEmail name the person who asked, on
	// the server admin's list only (fix wave 3, L3).
	RequestedByName  string `json:"requested_by_name,omitempty"`
	RequestedByEmail string `json:"requested_by_email,omitempty"`
	DecidedBy        string `json:"decided_by,omitempty"`
	Decision         string `json:"decision,omitempty"`
	Approval         string `json:"approval,omitempty"` // automatic | admin, for approved requests
	// NextStep tells the org what happens next, in plain words.
	NextStep  string     `json:"next_step,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
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

const imageCols = `id, org_id, image, size_bytes, note, status, requested_by, decided_by, decision, created_at, decided_at, approval`

func scanImage(r interface{ Scan(...any) error }) (*ImageRequest, error) {
	var v ImageRequest
	var created int64
	var decided sql.NullInt64
	if err := r.Scan(&v.ID, &v.OrgID, &v.Image, &v.SizeBytes, &v.Note, &v.Status, &v.RequestedBy, &v.DecidedBy, &v.Decision, &created, &decided, &v.Approval); err != nil {
		return nil, err
	}
	v.CreatedAt = db.Time(created)
	if decided.Valid {
		t := db.Time(decided.Int64)
		v.DecidedAt = &t
	}
	v.NextStep = nextStep(v)
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
	approval := ""
	if approve {
		approval = ApprovalAdmin
	}
	return s.decide(ctx, id, status, approval, actor, decision)
}

// AutoApprove approves a pending request automatically (ApprovalAutomatic).
// The caller checks AutoApprovable first.
func (s *ImageStore) AutoApprove(ctx context.Context, id, reason string) (*ImageRequest, error) {
	reason, err := cleanNote(reason)
	if err != nil {
		return nil, err
	}
	return s.decide(ctx, id, StatusApproved, ApprovalAutomatic, Actor, reason)
}

func (s *ImageStore) decide(ctx context.Context, id, status, approval, actor, decision string) (*ImageRequest, error) {
	res, err := s.DB.Exec(ctx, `UPDATE image_requests SET status = ?, decided_by = ?, decision = ?, decided_at = ?, approval = ? WHERE id = ? AND status = ?`,
		status, actor, decision, db.Millis(s.now()), approval, id, StatusPending)
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
	return s.ApprovedFor(ctx, orgID, true)
}

// ApprovedFor lists an org's approved images a shared worker may pull.
// gvisor is false for a worker that runs containers under runc: it never
// gets an image that was approved automatically on the strength of gVisor.
func (s *ImageStore) ApprovedFor(ctx context.Context, orgID string, gvisor bool) ([]string, error) {
	reqs, err := s.List(ctx, orgID, StatusApproved)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		if !gvisor && r.Approval == ApprovalAutomatic {
			continue
		}
		out = append(out, r.Image)
	}
	return out, nil
}

// nextStep is the plain next step of a request.
func nextStep(v ImageRequest) string {
	switch v.Status {
	case StatusPending:
		return "Waiting for a server admin. Images pinned by digest are approved automatically when the shared worker runs agents under gVisor."
	case StatusApproved:
		if v.Approval == ApprovalAutomatic {
			return "Ready: approved automatically. The shared worker runs this image under gVisor."
		}
		return "Ready: the shared worker may pull this image."
	case StatusRejected:
		return "Rejected: read the decision, then request another image or run it on a worker of your own."
	}
	return ""
}
