package tenancy

// Lane L10: incident notes on the public status page. Platform routes; the
// handler limits them to server admins (handlers/statusincidents.go).
func init() {
	PlatformPolicy[key("POST", api+"/status/incidents")] = userOnly()
	PlatformPolicy[key("POST", api+"/status/incidents/{id}")] = userOnly()
}
