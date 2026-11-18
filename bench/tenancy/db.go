package tenancy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db"
)

// DBDirectory is the Directory over the database area's orgs and
// memberships tables.
type DBDirectory struct{ DB *db.DB }

var _ Directory = DBDirectory{}

func orgOf(o *db.Org) Org {
	return Org{ID: o.ID, Slug: o.Slug, Name: o.Name, PublicRead: o.PublicRead}
}

// OrgByID implements Directory.
func (d DBDirectory) OrgByID(ctx context.Context, id string) (Org, error) {
	o, err := d.DB.OrgByID(ctx, id)
	if errors.Is(err, db.ErrNotFound) {
		return Org{}, fmt.Errorf("%w: %s", ErrOrgNotFound, id)
	}
	if err != nil {
		return Org{}, err
	}
	return orgOf(o), nil
}

// OrgBySlug implements Directory.
func (d DBDirectory) OrgBySlug(ctx context.Context, slug string) (Org, error) {
	o, err := d.DB.OrgBySlug(ctx, slug)
	if errors.Is(err, db.ErrNotFound) {
		return Org{}, fmt.Errorf("%w: %s", ErrOrgNotFound, slug)
	}
	if err != nil {
		return Org{}, err
	}
	return orgOf(o), nil
}

// MemberRole implements Directory.
func (d DBDirectory) MemberRole(ctx context.Context, orgID, userID string) (auth.Role, error) {
	if userID == "" {
		return "", ErrNotMember
	}
	r, err := d.DB.RoleOf(ctx, orgID, userID)
	if errors.Is(err, db.ErrNotFound) {
		return "", ErrNotMember
	}
	if err != nil {
		return "", err
	}
	return auth.Role(r), nil
}

// DBAudit is the AuditSink over audit_log.
type DBAudit struct{ DB *db.DB }

var _ AuditSink = DBAudit{}

// Audit implements AuditSink.
func (a DBAudit) Audit(ctx context.Context, e AuditEntry) error {
	var details json.RawMessage
	if len(e.Details) > 0 {
		details = db.AuditDetails(e.Details)
	}
	_, err := a.DB.AppendAudit(ctx, db.AuditEntry{
		OrgID: e.OrgID, ActorType: e.ActorType, ActorID: e.ActorID, Action: e.Action,
		TargetType: e.TargetType, TargetID: e.TargetID, IP: e.IP, At: e.At, Details: details,
	})
	return err
}
