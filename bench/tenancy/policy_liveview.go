package tenancy

// Recordings, live screen and human takeover (lane Z03, handlers/runlive.go,
// handlers/runrecordings.go, handlers/workerlive.go).
//
// Recordings are sealed run evidence and read like screen replay frames
// (viewer); a recording file download is audited like other run files. A
// live screen can show a customer's staging data, so only the run's org
// sees it (every org GET is a viewer read by contract), never a public
// reader. Taking control of a debug run is
// member, like starting one. A worker posts its run's frames with its
// runner token, like a heartbeat.
func init() {
	for k, r := range map[string]Rule{
		key("GET", api+"/runs/{id}/recordings"):     org(viewer),
		key("GET", api+"/runs/{id}/recordings/{n}"): org(viewer),
		key("GET", api+"/runs/{id}/live"):           org(viewer),
		key("GET", api+"/runs/{id}/live/frame"):     org(viewer),
		key("POST", api+"/runs/{id}/takeover"):      org(member),
	} {
		OrgPolicy[k] = r
	}
	OrgPolicy[key("POST", api+"/workers/leases/{lease_id}/live-frame")] = runner(false)
	SensitiveReads[key("GET", api+"/runs/{id}/recordings/{n}")] = true
}
