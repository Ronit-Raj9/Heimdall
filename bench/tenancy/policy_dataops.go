package tenancy

// Data lifecycle (lane X16, audits N10, N11, N17, N18).
//
// Deleting stored data is admin: a comparison with its runs, traces and
// gateway logs, or an evaluation with its comparisons. Retention is read by
// any member and set by an admin. Deleting the whole organization is owner
// only (the handler also wants a signed-in user and the typed slug). The
// audit log export and its integrity check are admin, like the log itself.
// Retrying a webhook delivery is member, like testing a webhook.
func init() {
	for k, r := range map[string]Rule{
		key("DELETE", api+"/experiments/{id}"):                 org(admin),
		key("DELETE", api+"/evaluations/{id}"):                 org(admin),
		key("GET", api+"/data/retention"):                      org(viewer),
		key("PUT", api+"/data/retention"):                      org(admin),
		key("POST", api+"/integrations/deliveries/{id}/retry"): org(member),
	} {
		OrgPolicy[k] = r
	}
	for k, r := range map[string]Rule{
		key("POST", api+"/orgs/{org}/delete"):      pathOrg(owner),
		key("GET", api+"/orgs/{org}/audit/export"): pathOrg(admin),
		key("GET", api+"/orgs/{org}/audit/verify"): pathOrg(admin),
	} {
		PlatformPolicy[k] = r
	}
}

// SensitiveReads are the GET routes whose successful reads leave an audit
// row (Rule.AuditRead): traces, evidence, run files and logs, exports and
// downloads of an org's data, and the audit log export itself. It is
// consulted at lookup, so it applies whichever file set the route's rule.
var SensitiveReads = map[string]bool{
	key("GET", api+"/runs/{id}/trace"):                       true,
	key("GET", api+"/runs/{id}/otlp"):                        true,
	key("GET", api+"/runs/{id}/diff"):                        true,
	key("GET", api+"/runs/{id}/files/{name}"):                true,
	key("GET", api+"/runs/{id}/evidence/{name}"):             true,
	key("GET", api+"/runs/{id}/artifacts/{artifact}/{name}"): true,
	key("GET", api+"/runs/{id}/services/{service}/log"):      true,
	key("GET", api+"/runs/{id}/voice/audio/{track}"):         true,
	key("GET", api+"/releases/{id}/evidence"):                true,
	key("GET", api+"/experiments/{id}/export"):               true,
	key("GET", api+"/integrations/exports/{name}"):           true,
	key("GET", api+"/project/export.zip"):                    true,
	key("GET", api+"/orgs/{org}/audit/export"):               true,
}
