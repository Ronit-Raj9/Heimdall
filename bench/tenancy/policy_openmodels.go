package tenancy

// Open models on the org's own Modal account (Z05): any member of the org
// reads them; starting and stopping one spends the org's Modal credit and
// needs an admin, as the handler also checks (handlers/openmodels.go).
func init() {
	PlatformPolicy[key("GET", api+"/open-models/catalog")] = org(viewer)
	PlatformPolicy[key("GET", api+"/open-models")] = org(viewer)
	PlatformPolicy[key("POST", api+"/open-models")] = org(admin)
	PlatformPolicy[key("GET", api+"/open-models/modal-apps")] = org(admin)
	PlatformPolicy[key("GET", api+"/open-models/{id}")] = org(viewer)
	PlatformPolicy[key("POST", api+"/open-models/{id}/stop")] = org(admin)
}
