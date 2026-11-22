package tenancy

// Policy document to compliance benchmark (lane Z23): uploads, clause
// edits and reviews write benchmark files; the document and its drafts
// hold reference solutions, so the handlers refuse viewers on those reads
// (the table keeps every GET at viewer, the rule of the contract). The
// report shows run outcomes, as the run pages do.
func init() {
	OrgPolicy[key("GET", api+"/suites/{id}/policy-docs")] = org(viewer)
	OrgPolicy[key("POST", api+"/suites/{id}/policy-docs")] = org(member)
	OrgPolicy[key("GET", api+"/suites/{id}/policy-docs/{doc}")] = org(viewer)
	OrgPolicy[key("DELETE", api+"/suites/{id}/policy-docs/{doc}")] = org(member)
	OrgPolicy[key("PUT", api+"/suites/{id}/policy-docs/{doc}/clauses")] = org(member)
	OrgPolicy[key("GET", api+"/suites/{id}/policy-docs/{doc}/drafts/{task}")] = org(viewer)
	OrgPolicy[key("POST", api+"/suites/{id}/policy-docs/{doc}/review")] = org(member)
	OrgPolicy[key("GET", api+"/suites/{id}/policy-docs/{doc}/report")] = org(viewer)
}
