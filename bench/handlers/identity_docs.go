package handlers

import "github.com/maximhq/bifrost/bench/auth"

// Identity routes (Wave A): hosted-only platform routes.
func init() {
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "POST", Path: VerifyEmailPath, Tag: "auth", Summary: "Verify an email address with the token from a verification link; works once; 404 when unknown, expired, used or replaced",
			Request: auth.VerifyRequest{}, Response: auth.VerifyResult{}},
		RouteDoc{Method: "POST", Path: VerifyResendPath, Tag: "auth", Summary: "Send a new verification link to the signed-in user (mailed, or written to the server log when no mail sender is set)",
			Response: auth.VerifySent{}},
		RouteDoc{Method: "GET", Path: SignupSettingPath, Tag: "admin", Summary: "Effective signup mode (invite, allowlist, open, closed), allowed domains and where they come from (server admin)",
			Response: auth.SignupSettings{}},
		RouteDoc{Method: "POST", Path: SignupSettingPath, Tag: "admin", Summary: "Override the signup mode and allowed domains; an empty mode returns to the server config (server admin)",
			Request: auth.SignupSettingsRequest{}, Response: auth.SignupSettings{}},
	)
}
