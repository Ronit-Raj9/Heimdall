package tenancy

// Web parity lane L18 (live scoring and inbox). The webhook setup read is
// for viewers; the hosted push itself stays denied, with a reason that says
// what to do instead (handlers/liveinbox_webhook.go).
func init() {
	OrgPolicy[key("GET", api+"/inbox/webhook")] = org(viewer)
	OrgPolicy[key("POST", api+"/inbox/ingest")] = deny("the inbox webhook is not served on the hosted server yet: push from a local Heimdall, or pull with bifrost-bench inbox pull and upload the project")
}
