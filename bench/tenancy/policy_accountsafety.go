package tenancy

// Account safety (L22): owner reset links and the public reset page.
func init() {
	PlatformPolicy[key("POST", api+"/orgs/{org}/members/{user}/reset-link")] = pathOrg(owner)
	PlatformPolicy[key("GET", api+"/auth/reset/{token}")] = public()  // the handler checks the token, rate limited per IP
	PlatformPolicy[key("POST", api+"/auth/reset/{token}")] = public() // the token is the credential; works once
}
