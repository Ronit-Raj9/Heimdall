package tenancy

// Identity (Wave A): email verification and the server admin's signup
// setting. The verify route is public (the token is the credential, rate
// limited per IP); resend needs a session; the signup setting routes are
// userOnly and the handler answers 403 unless the user is a server admin.
func init() {
	PlatformPolicy[key("POST", api+"/auth/verify-email")] = public()
	PlatformPolicy[key("POST", api+"/auth/verify-email/resend")] = userOnly()
	PlatformPolicy[key("GET", api+"/admin/signup")] = userOnly()
	PlatformPolicy[key("POST", api+"/admin/signup")] = userOnly()
}
