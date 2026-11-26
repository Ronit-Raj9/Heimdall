package tenancy

// Lane W4-47 (shadow replay): viewers read the settings and the replays,
// admins turn the opt-in on or off, members send a replayed session to the
// failure inbox. Replays are started with POST /jobs kind replay.run.
func init() {
	OrgPolicy[key("GET", api+"/replays/settings")] = org(viewer)
	OrgPolicy[key("PUT", api+"/replays/settings")] = org(admin)
	OrgPolicy[key("GET", api+"/replays")] = org(viewer)
	OrgPolicy[key("GET", api+"/replays/{id}")] = org(viewer)
	OrgPolicy[key("POST", api+"/replays/{id}/sessions/{recording}/to-inbox")] = org(member)
}
