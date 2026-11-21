package handlers

import (
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/valyala/fasthttp"
)

// Account safety (L22) routes are hosted-only platform routes.
func init() {
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "POST", Path: ResetLinkPath, Tag: "orgs", Summary: "Make a one-time password reset link for a member (owner; the token is shown once, valid 24 h)",
			Status: fasthttp.StatusCreated, Response: auth.ResetLink{}},
		RouteDoc{Method: "GET", Path: ResetTokenPath, Tag: "auth", Summary: "The account a reset link resets: email and expiry; 404 when unknown, expired or used",
			Response: auth.ResetInfo{}},
		RouteDoc{Method: "POST", Path: ResetTokenPath, Tag: "auth", Summary: "Set a new password with a reset link; the link works once and every session ends",
			Request: auth.ResetRequest{}, Status: fasthttp.StatusNoContent},
	)
}
