package handlers

import "github.com/valyala/fasthttp"

// API docs of lane Z15 (connecttraces.go): connect an agent from its
// traces, and check a .heimdall/agent.yaml.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: ConnectTracesPath, Tag: "connect",
			Summary:  "Connect from traces: the OTLP endpoint and setup lines, how many traces arrived, and the agent they show (name, framework, model, tools seen, serving route) as a draft and an agent.yaml",
			Response: TraceConnectInfo{}},
		RouteDoc{Method: "DELETE", Path: ConnectTracesPath, Tag: "connect", Auth: AuthWrite,
			Summary: "Forget the traces kept for connecting an agent", Response: TracesForgotten{}},
		RouteDoc{Method: "POST", Path: ConnectTracesInferPath, Tag: "connect", Auth: AuthWrite,
			Summary:    "Infer an agent from one OTLP trace export (JSON or protobuf, up to 4 MiB); nothing is kept",
			RawRequest: "application/json", RequestAlso: []string{"application/x-protobuf"}, Response: TraceInferResult{}},
		RouteDoc{Method: "POST", Path: AgentFileCheckPath, Tag: "connect", Auth: AuthWrite,
			Summary: "Check a .heimdall/agent.yaml: errors per field, and the agent request or config it becomes",
			Request: AgentFileCheckRequest{}, Response: AgentFileCheckResult{}},
		RouteDoc{Method: "POST", Path: OTLPLocalIngestPath, Tag: "connect", Auth: AuthWrite,
			Summary:    "OTLP/HTTP traces endpoint (local mode): spans are kept for agent inference, prompts and answers are not",
			RawRequest: "application/x-protobuf", RequestAlso: []string{"application/json"}, Response: OTLPExportResult{}, AlsoRaw: "application/x-protobuf"},
	)
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "POST", Path: OTLPOrgIngestPath, Tag: "connect", Auth: AuthInbox,
			Summary:    "OTLP/HTTP traces endpoint of this org: one of the org's ingest tokens, a 4 MiB cap and a per-org rate limit guard it; spans are kept for agent inference, prompts and answers are not",
			RawRequest: "application/x-protobuf", RequestAlso: []string{"application/json"}, Status: fasthttp.StatusOK, Response: OTLPExportResult{}, AlsoRaw: "application/x-protobuf"},
	)
}
