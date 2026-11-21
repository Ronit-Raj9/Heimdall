package tenancy

// Shared runner pool and guardrails (web parity lane L03). The admin routes
// are userOnly here; their handler also checks auth.IsServerAdmin. The
// assignments route is public: its handler checks the shared runner secret.
func init() {
	PlatformPolicy[key("GET", api+"/pool")] = org(viewer)
	PlatformPolicy[key("GET", api+"/images")] = org(viewer)
	PlatformPolicy[key("POST", api+"/images")] = org(member)
	PlatformPolicy[key("GET", api+"/admin/images")] = userOnly()
	PlatformPolicy[key("POST", api+"/admin/images/{id}/approve")] = userOnly()
	PlatformPolicy[key("POST", api+"/admin/images/{id}/reject")] = userOnly()
	PlatformPolicy[key("GET", api+"/admin/quotas/{id}")] = userOnly()
	PlatformPolicy[key("PUT", api+"/admin/quotas/{id}")] = userOnly()
	PlatformPolicy[key("GET", api+"/admin/pool/assignments")] = public()
	PlatformPolicy[key("POST", api+"/admin/pool/assignments/{org}/token")] = public()
}
