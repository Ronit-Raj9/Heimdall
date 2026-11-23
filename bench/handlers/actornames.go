package handlers

import (
	"context"
	"strings"

	"github.com/valyala/fasthttp"
)

// Raw actors shown to members (fix wave 2, D4, the G18 follow-up): a
// recorded actor (a user id or an email) gets a display name next to it
// (displayActor), and an email is left out of the answer for a caller who
// is not an org admin. The stored record keeps the actor as it was.

// formerMember is how a user id that is no longer a member of the
// organization is shown (fix wave 3, N221).
const formerMember = "a former member"

// actorNames is the member names of the caller's organization, looked up
// once for one answer.
type actorNames struct {
	ctx    context.Context
	names  map[string]string
	looked bool
}

func newActorNames(ctx context.Context) *actorNames { return &actorNames{ctx: ctx} }

// callerNames is newActorNames for a signed-in caller and nil (show keeps
// every actor as it is) for a local caller, the CLI or a handler test with
// no principal: local mode has one person and no members list (fix wave 3,
// N33).
func callerNames(ctx *fasthttp.RequestCtx) *actorNames {
	if isLocalCaller(ctx) {
		return nil
	}
	return newActorNames(ctx)
}

// show returns the actor to keep in the answer ("" for an email the caller
// may not see) and the display name ("" when it would only repeat the
// actor).
func (a *actorNames) show(actor string) (keep, name string) {
	if a == nil || strings.TrimSpace(actor) == "" {
		return actor, ""
	}
	if !a.looked {
		a.names, a.looked = orgMemberNames(a.ctx), true
	}
	name, keepRaw := displayActor(a.ctx, actor, a.names)
	if a.names != nil && name == strings.TrimSpace(actor) && strings.HasPrefix(name, "usr_") {
		// A user id with no member behind it: someone who has left the
		// organization. Only an admin keeps the id (fix wave 3, N221).
		keep = ""
		if keepRaw {
			keep = actor
		}
		return keep, formerMember
	}
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
