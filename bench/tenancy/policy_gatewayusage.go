package tenancy

// Gateway usage (lane C03, handlers/gatewayusage.go): the heimdall Bifrost
// plugin pushes usage rows with a shared secret the handler checks, so the
// hosted push is a public platform route (each row carries its org id).
// Reading a run's rows is for viewers of the org, and the handler hides rows
// tagged with another org.
func init() {
	PlatformPolicy[key("POST", api+"/gateway/usage")] = public() // the handler checks HEIMDALL_GATEWAY_USAGE_SECRET
	OrgPolicy[key("POST", api+"/gateway/usage")] = deny("the hosted server takes gateway usage on the platform route with the shared secret")
	OrgPolicy[key("GET", api+"/gateway/usage")] = org(viewer)
}
