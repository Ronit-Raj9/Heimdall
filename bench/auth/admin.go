package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/db"
)

// Operator actions for the CLI (`bifrost-bench user ...`): they run with
// shell access to the server and its database, so they need no principal
// and are audited as actor "system".

// CreatedUser is what CreateUser did.
type CreatedUser struct {
	User       *User
	Org        *Org
	OrgCreated bool
	Role       Role
}

// CreateUser creates a password account and gives it role in org, an org
// id or slug; an org that does not exist is created with that name. The
// password must pass CheckPasswordPolicy. Works on an empty install, which
// is how a closed-signup server gets its first owner.
func CreateUser(ctx context.Context, d *db.DB, hasher *Hasher, email, name, password, org string, role Role) (*CreatedUser, error) {
	email = NormalizeEmail(email)
	if !validEmail(email) {
		return nil, fmt.Errorf("a valid email address is required")
	}
	if !role.Valid() {
		return nil, fmt.Errorf("role must be viewer, member, admin or owner")
	}
	org = strings.TrimSpace(org)
	if org == "" {
		return nil, fmt.Errorf("an organization (id, slug or new name) is required")
	}
	if err := CheckPasswordPolicy(password, email); err != nil {
		return nil, err
	}
	if hasher == nil {
		hasher = NewHasher(DefaultArgon2Params)
	}
	hash, err := hasher.Hash(password)
	if err != nil {
		return nil, err
	}
	st := &store{d: d, now: time.Now}
	out := &CreatedUser{Role: role}
	err = d.Tx(ctx, func(tx *sql.Tx) error {
		u, err := st.insertUser(ctx, tx, email, name, hash, "")
		if errors.Is(err, db.ErrConflict) {
			return fmt.Errorf("a user with email %s already exists", email)
		}
		if err != nil {
			return err
		}
		out.User = u
		o, err := st.orgByID(ctx, tx, org)
		if errors.Is(err, db.ErrNotFound) {
			o, err = st.orgBySlug(ctx, tx, org)
		}
		if errors.Is(err, db.ErrNotFound) {
			o, err = st.insertOrg(ctx, tx, org)
			out.OrgCreated = true
		}
		if err != nil {
			return err
		}
		out.Org = o
		return st.grantMembership(ctx, tx, o.ID, u.ID, role)
	})
	if err != nil {
		return nil, err
	}
	details, _ := json.Marshal(map[string]any{"via": "cli", "role": string(role), "org_created": out.OrgCreated})
	_, _ = d.AppendAudit(ctx, db.AuditEntry{OrgID: out.Org.ID, ActorType: db.ActorSystem, ActorID: "cli", Action: "auth.user_create",
		TargetType: "user", TargetID: out.User.ID, Details: details})
	return out, nil
}

// SetPassword replaces a user's password (account recovery from the CLI)
// and ends every session of the user.
func SetPassword(ctx context.Context, d *db.DB, hasher *Hasher, email, password string) (*User, error) {
	email = NormalizeEmail(email)
	if err := CheckPasswordPolicy(password, email); err != nil {
		return nil, err
	}
	if hasher == nil {
		hasher = NewHasher(DefaultArgon2Params)
	}
	hash, err := hasher.Hash(password)
	if err != nil {
		return nil, err
	}
	st := &store{d: d, now: time.Now}
	var u *User
	err = d.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if u, err = st.userByEmail(ctx, tx, email); err != nil {
			if errors.Is(err, db.ErrNotFound) {
				return fmt.Errorf("no user with email %s", email)
			}
			return err
		}
		if err := st.setPasswordHash(ctx, tx, u.ID, hash); err != nil {
			return err
		}
		return st.deleteUserSessions(ctx, tx, u.ID, "")
	})
	if err != nil {
		return nil, err
	}
	details, _ := json.Marshal(map[string]any{"via": "cli"})
	_, _ = d.AppendAudit(ctx, db.AuditEntry{ActorType: db.ActorSystem, ActorID: "cli", Action: "auth.password_set",
		TargetType: "user", TargetID: u.ID, Details: details})
	return u, nil
}
