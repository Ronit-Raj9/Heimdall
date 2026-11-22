package tenancy

// Connect from traces (lane Z15, handlers/connecttraces.go): viewers see
// what the org's traces show and the draft agent; members infer from a
// posted export, check an agent file and forget the kept traces. The
// hosted ingest is a platform route: the handler checks one of the org's
// ingest tokens (the inbox tokens) and rate limits per org. The local
// ingest route is refused on the hosted server.
func init() {
	OrgPolicy[key("GET", api+"/connect/traces")] = org(viewer)
	OrgPolicy[key("DELETE", api+"/connect/traces")] = org(member)
	OrgPolicy[key("POST", api+"/connect/traces/infer")] = org(member)
	OrgPolicy[key("POST", api+"/connect/agent-file/check")] = org(member)
	OrgPolicy[key("POST", api+"/otlp/v1/traces")] = deny("the hosted server takes traces per org: POST /api/bench/orgs/{org}/otlp/v1/traces with an ingest token (Agents, Connect from traces)")
	PlatformPolicy[key("GET", api+"/github/repos/{owner}/{repo}/agent-file")] = org(member) // the repository's .heimdall/agent.yaml
	PlatformPolicy[key("POST", api+"/orgs/{org}/otlp/v1/traces")] = public() // the handler checks the org's ingest token, rate limited per org
}
