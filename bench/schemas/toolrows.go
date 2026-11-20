package schemas

import "fmt"

// Kinds of tool proxy ledger rows (ToolCall.Kind). The proxy writes a row
// for every request it relays, so nothing an agent and a tool server
// exchange through it is invisible; only rows with an empty Kind are tool
// calls, and only those count as tool calls in metrics
// (docs/tool-proxy.md).
const (
	// ToolRowMCPMethod is a client request other than tools/call: initialize,
	// tools/list, resources/*, prompts/*, ping, completion/complete,
	// logging/setLevel and any other method the client sends with an id.
	ToolRowMCPMethod = "mcp_method"
	// ToolRowNotification is a JSON-RPC notification, either way: from the
	// client (notifications/initialized, notifications/cancelled) or from the
	// server (notifications/progress, notifications/message,
	// notifications/tools/list_changed).
	ToolRowNotification = "mcp_notification"
	// ToolRowServerRequest is a request the server sends the client:
	// sampling/createMessage, elicitation/create, roots/list, ping.
	ToolRowServerRequest = "mcp_server_request"
	// ToolRowResponse is a JSON-RPC response that answers no request the
	// proxy tracked: the client's answer to a server request, or a server
	// answer on a standalone stream.
	ToolRowResponse = "mcp_response"
	// ToolRowTransport is a request that carries no JSON-RPC message: an MCP
	// GET that opens the server's event stream, a DELETE that ends a session.
	ToolRowTransport = "transport"
	// ToolRowOpaque is traffic the proxy relayed without parsing it: a body
	// that is not JSON or not JSON-RPC, over the parse cap, of a content type
	// or encoding the proxy does not read, sent to a path that is not the MCP
	// endpoint, or one oversized event of a stream. Its hashes and byte counts
	// still cover every byte.
	ToolRowOpaque = "opaque"
)

// ToolRowUnparsedTool is the Tool of an opaque row.
const ToolRowUnparsedTool = "unparsed"

// IsToolCall reports whether a ledger row is a tool call: an MCP tools/call,
// a plain HTTP tool request or an external API call. Rows of another kind,
// and the tools/list rows written before kinds existed, are not.
func (c ToolCall) IsToolCall() bool {
	return c.Kind == "" && !(c.Protocol == "mcp" && c.Tool == "tools/list")
}

// ToolLedgerStatusFile is the tool proxy's ledger status, kept next to its
// ledger and copied into the run folder beside tools.jsonl (3-D-08).
const ToolLedgerStatusFile = "tools.status.json"

// ToolLedgerStatus is the content of ToolLedgerStatusFile.
type ToolLedgerStatus struct {
	Rows     int    `json:"rows"`             // the highest Seq numbered, written or not
	InFlight int    `json:"inflight"`         // calls answered or in progress whose row is not yet written
	Failed   int    `json:"failed,omitempty"` // rows that could not be written
	Error    string `json:"error,omitempty"`  // the first write error
}

// Incomplete says why a ledger with this status is not the whole record of
// the run's calls, or "" when it is.
func (st ToolLedgerStatus) Incomplete() string {
	switch {
	case st.Failed > 0 || st.Error != "":
		return fmt.Sprintf("%d rows failed to write: %s", st.Failed, st.Error)
	case st.InFlight > 0:
		return fmt.Sprintf("%d calls were still in flight when the ledger was copied", st.InFlight)
	}
	return ""
}
