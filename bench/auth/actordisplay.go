package auth

import (
	"context"
	"strings"

	"github.com/maximhq/bifrost/bench/db"
)

// ActorUsers is what ActorDisplay reads; *db.DB has both methods.
type ActorUsers interface {
	UserByID(ctx context.Context, id string) (*db.User, error)
	UserByEmail(ctx context.Context, email string) (*db.User, error)
}

// removedUser is how a user id with no account behind it is shown: a
// platform page never prints a raw usr_ id (fix wave 3, L3).
const removedUser = "a removed user"

// ActorDisplay is how a recorded actor (a user id or an email) is shown on
// the platform admin pages (fix wave 3, L3): the account's display name
// (the name, else the part of the email before "@") and its email. An email
// with no account is named by the part before "@"; a user id with no
// account reads "a removed user"; any other actor ("schedule:x", "system")
// is returned as it is with no email. Server admins may see emails, so the
// caller decides where the email goes (a title, never the text).
func ActorDisplay(ctx context.Context, dir ActorUsers, actor string) (name, email string) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return "", ""
	}
	var u *db.User
	if dir != nil {
		switch {
		case strings.HasPrefix(actor, "usr_"):
			u, _ = dir.UserByID(ctx, actor)
		case strings.Contains(actor, "@"):
			u, _ = dir.UserByEmail(ctx, actor)
		}
	}
	if u != nil {
		local, _, _ := strings.Cut(u.Email, "@")
		if n := strings.TrimSpace(u.Name); n != "" {
			return n, u.Email
		}
		return local, u.Email
	}
	switch {
	case strings.HasPrefix(actor, "usr_"):
		return removedUser, ""
	case strings.Contains(actor, "@"):
		local, _, _ := strings.Cut(actor, "@")
		return local, actor
	}
	return actor, ""
}
