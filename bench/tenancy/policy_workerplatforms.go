package tenancy

// Bring your own sandbox platform (X19a): Modal, Daytona and E2B
// connections and the org's default worker. Admin of the current org, as
// the handler also checks (handlers/workerplatforms.go).
func init() {
	PlatformPolicy[key("GET", api+"/worker-platforms")] = org(admin)
	PlatformPolicy[key("POST", api+"/worker-platforms/default")] = org(admin)
	PlatformPolicy[key("POST", api+"/worker-platforms/{platform}/connect")] = org(admin)
	PlatformPolicy[key("POST", api+"/worker-platforms/{platform}/test")] = org(admin)
	PlatformPolicy[key("POST", api+"/worker-platforms/{platform}/disconnect")] = org(admin)
}
