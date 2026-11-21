package handlers

import (
	"strings"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/valyala/fasthttp"
)

// signedActor returns the name a review or a release approval is recorded
// under, taken from the signed-in principal and never from the request body
// (audit PROD_PROD2 P2/N8). ok is false when the caller has no identity to
// bind the record to; msg then says why.
//
//   - A user session records "email (user id)".
//   - An API token records its creator: "email (user id) via token <id>"
//     when the token still has a creator, else "token <id>", so the record
//     always names a credential the org issued.
//   - Local mode (no auth, KindLocal, or no principal on the request at all,
//     which is how the CLI and handler tests reach the handler) keeps the
//     self-declared name from the body, or "local" when it is empty: there is
//     no one signed in to bind it to.
//   - Anonymous and runner principals are refused.
//
// Records written before this change keep their stored text unchanged; the
// release and review hash chains cover the stored string, so nothing in an
// old record moves.
func signedActor(ctx *fasthttp.RequestCtx, declared string) (name string, ok bool, msg string) {
	p, set := auth.Lookup(ctx)
	if !set || p == nil || p.Kind == auth.KindLocal {
		if d := strings.TrimSpace(declared); d != "" {
			return d, true, ""
		}
		return "local", true, ""
	}
	switch p.Kind {
	case auth.KindUser:
		if p.UserID == "" {
			return "", false, "the signed-in session has no user"
		}
		return userLabel(p), true, ""
	case auth.KindToken:
		if p.UserID != "" {
			return userLabel(p) + " via token " + p.TokenID, true, ""
		}
		if p.TokenID != "" {
			return "token " + p.TokenID, true, ""
		}
	}
	return "", false, "sign in to record this under your name"
}

func userLabel(p *auth.Principal) string {
	if p.Email != "" {
		return p.Email + " (" + p.UserID + ")"
	}
	return p.UserID
}

// isLocalCaller reports whether the request runs without hosted auth: no
// principal set, or the local-mode principal.
func isLocalCaller(ctx *fasthttp.RequestCtx) bool {
	p, set := auth.Lookup(ctx)
	return !set || p == nil || p.Kind == auth.KindLocal
}
