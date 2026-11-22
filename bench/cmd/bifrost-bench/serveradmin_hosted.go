package main

// Server admin area (web parity L23): the /api/bench/admin/ platform routes
// of hosted mode (handlers/serveradmin.go) over the server database, the
// runners service and the quota store.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/server"
)

// adminQuotaStore returns the platform quota store the admin routes edit and
// whether it keeps its writes. The shared pool area (L03) owns the real
// Postgres store (quotaOf); without a database the admin page edits the stub
// and says so (persisted false).
var adminQuotaStore = func(env *hostedEnv) (quota.Store, bool) { return quotaOf(env), env.db != nil }

// maxAdminUsers caps the users list.
const maxAdminUsers = 5000

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		var dir handlers.AdminDirectory
		var rs handlers.AdminRunners
		if env.db != nil {
			dir = adminDirectory{d: env.db}
			rs = runnersOf(env).svc
		}
		q, persisted := adminQuotaStore(env)
		return handlers.NewServerAdminHandler(env.base.cfg.Server.Mode, dir, rs, q, persisted, runnerAudit(env)), nil
	})
}

// adminDirectory reads every user and org of the server database.
type adminDirectory struct{ d *db.DB }

func (a adminDirectory) Users(ctx context.Context) ([]handlers.AdminUser, error) {
	// Server admin status is by user id or verified email, never an email
	// match alone (audit B1).
	admins := map[string]bool{}
	list, err := auth.ServerAdmins(ctx, a.d)
	if err != nil {
		return nil, err
	}
	for _, e := range list {
		admins[e.UserID] = true
	}
	rows, err := a.d.Query(ctx, `SELECT u.id, u.email, u.name, u.github_id, u.disabled, u.created_at,
		(SELECT COUNT(*) FROM memberships m WHERE m.user_id = u.id)
		FROM users u ORDER BY u.created_at, u.id LIMIT ?`, maxAdminUsers)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	out := []handlers.AdminUser{}
	for rows.Next() {
		var u handlers.AdminUser
		var gh sql.NullString
		var dis, created int64
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &gh, &dis, &created, &u.Orgs); err != nil {
			return nil, err
		}
		u.GitHub, u.Disabled, u.CreatedAt = gh.Valid && gh.String != "", dis != 0, db.Time(created)
		u.ServerAdmin = admins[u.ID]
		out = append(out, u)
	}
	return out, rows.Err()
}

func (a adminDirectory) SetUserDisabled(ctx context.Context, userID string, disabled bool) error {
	err := a.d.SetUserDisabled(ctx, userID, disabled)
	if errors.Is(err, db.ErrNotFound) {
		return handlers.ErrAdminNotFound
	}
	return err
}

func (a adminDirectory) Orgs(ctx context.Context) ([]handlers.AdminOrg, error) {
	orgs, err := a.d.ListOrgs(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	rows, err := a.d.Query(ctx, `SELECT org_id, COUNT(*) FROM memberships GROUP BY org_id`)
	if err != nil {
		return nil, fmt.Errorf("count members: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		counts[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]handlers.AdminOrg, 0, len(orgs))
	for _, o := range orgs {
		out = append(out, handlers.AdminOrg{ID: o.ID, Slug: o.Slug, Name: o.Name, PublicRead: o.PublicRead,
			Members: counts[o.ID], CreatedAt: o.CreatedAt})
	}
	return out, nil
}
