package tenancy

// Library registry pull tokens (Z13, bench/imagepin): docker clients reach
// the token endpoint with basic auth, which the auth middleware does not
// read, so the route is public here and the handler checks the credential
// itself (an org API or runner token, or the sync identity), with a cap on
// refused credentials per address (handlers/registrytoken.go).
func init() {
	PlatformPolicy[key("GET", api+"/registry/token")] = public()
	PlatformPolicy[key("POST", api+"/registry/token")] = public()
}
