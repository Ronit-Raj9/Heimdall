package tenancy

// Fix wave 3 lane H06 (N216): a sandbox platform's image registry login.
// Admin of the current org, like the other worker platform routes
// (policy_workerplatforms.go).
func init() {
	PlatformPolicy[key("POST", api+"/worker-platforms/{platform}/registry")] = org(admin)
	PlatformPolicy[key("POST", api+"/worker-platforms/{platform}/registry/remove")] = org(admin)
}
