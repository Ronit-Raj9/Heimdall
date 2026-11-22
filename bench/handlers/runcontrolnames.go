package handlers

// Display names for run control actors (J6.log-user). The change log and the
// pause record keep the user id; GET /experiments/{id}/controls adds the
// name next to it. The org handlers have no members store, so the hosted
// server installs a resolver here (cmd/bifrost-bench/membernames_hosted.go).
// Local mode has none and ids stay as they are.

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/valyala/fasthttp"
)

var (
	memberNamesMu sync.RWMutex
	memberNames   func(ctx context.Context, orgID string) (map[string]string, error)
)

// SetMemberNames installs the resolver from user id, and from lower-case
// email, to display text (the name, else the part of the email before "@")
// for an organization; nil removes it.
func SetMemberNames(f func(ctx context.Context, orgID string) (map[string]string, error)) {
	memberNamesMu.Lock()
	defer memberNamesMu.Unlock()
	memberNames = f
}

// memberNamesTimeout bounds the members lookup: names are a nicety and
// never hold up the controls.
const memberNamesTimeout = 2 * time.Second

// orgMemberNames returns the display names of the organization of the
// principal on ctx; nil when there is no resolver, no organization or the
// lookup failed.
func orgMemberNames(ctx context.Context) map[string]string {
	memberNamesMu.RLock()
	f := memberNames
	memberNamesMu.RUnlock()
	if f == nil {
		return nil
	}
	p, ok := auth.Lookup(ctx)
	if !ok || p.OrgID == "" {
		return nil
	}
	base := ctx
	if _, ok := ctx.(*fasthttp.RequestCtx); ok {
		// A request context ends only when the server stops (and has no
		// server in handler tests); the timeout bounds the lookup.
		base = auth.WithPrincipal(context.Background(), p)
	}
	work, cancel := context.WithTimeout(base, memberNamesTimeout)
	defer cancel()
	names, err := f(work, p.OrgID)
	if err != nil {
		return nil
	}
	return names
}

// nameControls fills the display names of the user who paused and of each
// change log actor that is a member of the organization.
func nameControls(ctx context.Context, out *RunControls) {
	names := orgMemberNames(ctx)
	if len(names) == 0 {
		return
	}
	if out.State != nil && out.State.PausedBy != "" {
		out.PausedByName = names[out.State.PausedBy]
	}
	for _, e := range out.Log {
		if n := names[e.Actor]; n != "" {
			if out.ActorNames == nil {
				out.ActorNames = map[string]string{}
			}
			out.ActorNames[e.Actor] = n
		}
	}
}

// displayActor is how a person recorded as actor (a user id or an email) is
// shown to the caller (fix wave 2, D4): the member's display name when the
// actor is a member (names from orgMemberNames, keyed by user id and by
// lower-case email), else, for an email, the part before "@", else the
// actor as is. keepRaw is true only for an org admin (or the local
// single-user principal), who may also see the raw actor, email included.
func displayActor(ctx context.Context, actor string, names map[string]string) (name string, keepRaw bool) {
	if p, ok := auth.Lookup(ctx); ok && p.Can(auth.RoleAdmin) {
		keepRaw = true
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return "", keepRaw
	}
	if n := names[actor]; n != "" {
		return n, keepRaw
	}
	if n := names[strings.ToLower(actor)]; n != "" {
		return n, keepRaw
	}
	if local, _, ok := strings.Cut(actor, "@"); ok {
		return local, keepRaw
	}
	return actor, keepRaw
}
