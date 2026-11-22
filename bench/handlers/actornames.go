package handlers

import (
	"context"
	"strings"
)

// Raw actors shown to members (fix wave 2, D4, the G18 follow-up): a
// recorded actor (a user id or an email) gets a display name next to it
// (displayActor), and an email is left out of the answer for a caller who
// is not an org admin. The stored record keeps the actor as it was.

// actorNames is the member names of the caller's organization, looked up
// once for one answer.
type actorNames struct {
	ctx    context.Context
	names  map[string]string
	looked bool
}

func newActorNames(ctx context.Context) *actorNames { return &actorNames{ctx: ctx} }

// show returns the actor to keep in the answer ("" for an email the caller
// may not see) and the display name ("" when it would only repeat the
// actor).
func (a *actorNames) show(actor string) (keep, name string) {
	if strings.TrimSpace(actor) == "" {
		return actor, ""
	}
	if !a.looked {
		a.names, a.looked = orgMemberNames(a.ctx), true
	}
	name, keepRaw := displayActor(a.ctx, actor, a.names)
	if strings.Contains(name, "@") {
		name, _, _ = strings.Cut(name, "@")
	}
	keep = actor
	if !keepRaw && strings.Contains(actor, "@") {
		keep = ""
	}
	if name == actor {
		name = ""
	}
	return keep, name
}
