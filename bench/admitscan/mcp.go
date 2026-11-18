package admitscan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// MCP rule ids.
const (
	RuleRugPull         = "mcp-rug-pull"
	RuleShadowForeign   = "mcp-shadow-foreign-tool"
	RuleShadowDirective = "mcp-shadow-directive"
	RuleShadowSibling   = "mcp-shadow-sibling-tool"
	RuleNameCollision   = "mcp-tool-name-collision"
	RuleMCPListFailed   = "mcp-list-failed"
)

// RPCMessage is one JSON-RPC 2.0 message: a request, a notification or a
// response.
type RPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// MCPTool is one tool of a tools/list answer. Raw is the tool object as
// sent; the hash and the text checks read it.
type MCPTool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	Raw         json.RawMessage `json:"-"`
}

// MCPListing is what a server listed in one session.
type MCPListing struct {
	Instructions string
	Tools        []MCPTool
	Prompts      []json.RawMessage
	Resources    []json.RawMessage
}

// ToolChange is one difference between two tools/list answers.
type ToolChange struct {
	Tool   string
	Kind   string // added | removed | changed
	Before string // tool hash before (empty when added)
	After  string // tool hash after (empty when removed)
}

// ParseRPCMessages reads the body of a streamable-HTTP MCP answer: one
// JSON-RPC message, a batch, or a server-sent event stream whose data lines
// hold messages. It is pure.
func ParseRPCMessages(body []byte, contentType string) ([]RPCMessage, error) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream") {
		return parseSSE(body)
	}
	return parseJSONRPC(bytes.TrimSpace(body))
}

func parseJSONRPC(b []byte) ([]RPCMessage, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if b[0] == '[' {
		var batch []RPCMessage
		if err := json.Unmarshal(b, &batch); err != nil {
			return nil, fmt.Errorf("decode json-rpc batch: %w", err)
		}
		return batch, nil
	}
	var m RPCMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("decode json-rpc message: %w", err)
	}
	return []RPCMessage{m}, nil
}

// parseSSE joins the data lines of each event and decodes each event.
func parseSSE(body []byte) ([]RPCMessage, error) {
	var out []RPCMessage
	var data []string
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		msgs, err := parseJSONRPC([]byte(strings.Join(data, "\n")))
		data = data[:0]
		if err != nil {
			return err
		}
		out = append(out, msgs...)
		return nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		switch {
		case line == "":
			if err := flush(); err != nil {
				return out, err
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	return out, flush()
}

// ParseToolsList reads a tools/list result: the tools and the next cursor.
func ParseToolsList(result json.RawMessage) ([]MCPTool, string, error) {
	var r struct {
		Tools      []json.RawMessage `json:"tools"`
		NextCursor string            `json:"nextCursor"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return nil, "", fmt.Errorf("decode tools/list result: %w", err)
	}
	tools := make([]MCPTool, 0, len(r.Tools))
	for i, raw := range r.Tools {
		var t MCPTool
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, "", fmt.Errorf("decode tool %d: %w", i, err)
		}
		if t.Name == "" {
			return nil, "", fmt.Errorf("tool %d has no name", i)
		}
		t.Raw = append(json.RawMessage(nil), raw...)
		tools = append(tools, t)
	}
	return tools, r.NextCursor, nil
}

// ParseList reads the array under key of a prompts/list, resources/list or
// resources/templates/list result, and the next cursor.
func ParseList(result json.RawMessage, key string) ([]json.RawMessage, string, error) {
	var r map[string]json.RawMessage
	if err := json.Unmarshal(result, &r); err != nil {
		return nil, "", fmt.Errorf("decode %s result: %w", key, err)
	}
	var items []json.RawMessage
	if raw, ok := r[key]; ok {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, "", fmt.Errorf("decode %s: %w", key, err)
		}
	}
	var cursor string
	if raw, ok := r["nextCursor"]; ok {
		if err := json.Unmarshal(raw, &cursor); err != nil {
			return nil, "", fmt.Errorf("decode %s cursor: %w", key, err)
		}
	}
	return items, cursor, nil
}

// ToolHash is the sha256 of a tool's canonical JSON (keys sorted), without
// its _meta member, which servers use for volatile data the model never
// reads. Any change to name, title, description, schemas or annotations
// changes it.
func ToolHash(t MCPTool) string {
	var v map[string]any
	raw := t.Raw
	if len(raw) == 0 {
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		raw = b
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}
	delete(v, "_meta")
	b, err := json.Marshal(v)
	if err != nil {
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// DiffListings compares two tools/list answers by tool hash, in tool name
// order.
func DiffListings(before, after []MCPTool) []ToolChange {
	b, a := hashByName(before), hashByName(after)
	names := map[string]bool{}
	for n := range b {
		names[n] = true
	}
	for n := range a {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	var out []ToolChange
	for _, n := range sorted {
		hb, inB := b[n]
		ha, inA := a[n]
		switch {
		case !inA:
			out = append(out, ToolChange{Tool: n, Kind: "removed", Before: hb})
		case !inB:
			out = append(out, ToolChange{Tool: n, Kind: "added", After: ha})
		case ha != hb:
			out = append(out, ToolChange{Tool: n, Kind: "changed", Before: hb, After: ha})
		}
	}
	return out
}

func hashByName(ts []MCPTool) map[string]string {
	m := make(map[string]string, len(ts))
	for _, t := range ts {
		m[t.Name] = ToolHash(t)
	}
	return m
}

// RugPullFindings turns the changes between the first listing and a later
// one into block findings; when names the moment of the later listing.
func RugPullFindings(server, when string, changes []ToolChange) []schemas.AdmitScanFinding {
	out := make([]schemas.AdmitScanFinding, 0, len(changes))
	for _, c := range changes {
		d := fmt.Sprintf("tool %s %s %s (first listing sha256 %s, later %s)", c.Tool, c.Kind, when, short12(c.Before), short12(c.After))
		out = append(out, schemas.AdmitScanFinding{Rule: RuleRugPull, Severity: schemas.AdmitScanSeverityBlock, File: mcpWhere(server, "tools/"+c.Tool), Detail: d})
	}
	return out
}

func short12(h string) string {
	if h == "" {
		return "-"
	}
	return h[:min(12, len(h))]
}

// mcpWhere names a place in a server's listings, used as a finding's file.
func mcpWhere(server, where string) string { return "mcp:" + server + "/" + where }

// WellKnownTools are tool names other MCP servers and the coding agents
// themselves provide. A description that names one of them, on a server
// that does not provide it, is shadowing another tool.
var WellKnownTools = []string{
	"send_email", "send_message", "read_file", "write_file", "edit_file", "list_directory", "create_directory", "move_file",
	"search_files", "execute_command", "run_command", "run_shell", "shell", "bash", "exec", "terminal", "fetch", "http_request",
	"web_fetch", "WebFetch", "WebSearch", "Bash", "Read", "Write", "Edit", "MultiEdit", "Glob", "Grep", "Task", "TodoWrite",
	"apply_patch", "str_replace_editor", "create_pull_request", "push_files", "create_or_update_file", "git_commit", "git_push",
	"slack_post_message", "post_message", "transfer_funds", "make_payment", "query", "sql_query", "execute_sql",
}

var shadowDirectiveRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:when|whenever|before|after|every\s+time)\s+(?:you\s+)?(?:use|call|invoke|run)s?\s+(?:the\s+|any\s+|another\s+|other\s+)?[\w.-]*\s*tools?\b`),
	regexp.MustCompile(`(?i)\b(?:all|any|every)\s+other\s+tools?\b|\bother\s+(?:mcp\s+)?(?:servers?|tools?)\b`),
	regexp.MustCompile(`(?i)\binstead\s+of\s+(?:using\s+|calling\s+)?(?:the\s+)?[\w.-]+\s+tool\b`),
	regexp.MustCompile(`(?i)\b(?:do\s+not|don'?t|never)\s+(?:use|call|invoke)\s+(?:the\s+)?[\w.-]+\s+tool\b`),
	regexp.MustCompile(`(?i)\b(?:must|always)\s+(?:be\s+)?(?:called|used|invoked|run)\s+(?:before|after|first|together)\b`),
	regexp.MustCompile(`(?i)\b(?:send|bcc|cc|redirect|forward|re-?route|copy)\b[^.\n]{0,40}\b(?:all|every|each)\b[^.\n]{0,20}\b(?:emails?|messages?|requests?|calls?|payments?|files?)\b`),
	regexp.MustCompile(`(?i)\bthe\s+[\w.-]+\s+tool\s+(?:must|should|shall|needs?\s+to|has\s+to|will\s+now)\b`),
	regexp.MustCompile(`(?i)\bwhen\s+this\s+tool\s+is\s+(?:available|present|installed|loaded|enabled)\b`),
}

// ScanMCPListing runs the text rules over everything the server shows the
// model (instructions, tool names, titles, descriptions, schema texts,
// prompts and resources), then the shadowing and name rules.
func ScanMCPListing(server string, l MCPListing, opts TextOptions) []schemas.AdmitScanFinding {
	opts.Markdown = true
	var out []schemas.AdmitScanFinding
	siblings := map[string]bool{}
	for _, t := range l.Tools {
		siblings[t.Name] = true
	}
	if l.Instructions != "" {
		where := mcpWhere(server, "instructions")
		out = append(out, ScanText(where, []byte(l.Instructions), opts)...)
		out = append(out, shadowFindings(where, "(server instructions)", l.Instructions, siblings)...)
	}
	for _, t := range l.Tools {
		base := "tools/" + t.Name
		for _, txt := range toolTexts(t) {
			where := mcpWhere(server, base+"/"+txt.where)
			out = append(out, ScanText(where, []byte(txt.text), opts)...)
			if txt.where != "name" {
				out = append(out, shadowFindings(where, t.Name, txt.text, siblings)...)
			}
		}
		out = append(out, nameFindings(server, t.Name)...)
	}
	for i, p := range l.Prompts {
		out = append(out, scanRawTexts(mcpWhere(server, fmt.Sprintf("prompts/%s", itemName(p, i))), p, opts)...)
	}
	for i, r := range l.Resources {
		out = append(out, scanRawTexts(mcpWhere(server, fmt.Sprintf("resources/%s", itemName(r, i))), r, opts)...)
	}
	return out
}

// toolText is one string of a tool the model reads.
type toolText struct{ where, text string }

// toolTexts lists the name, title, description and every string inside the
// tool's schemas and annotations (descriptions, titles, enums, defaults,
// examples), each with where it sits.
func toolTexts(t MCPTool) []toolText {
	out := []toolText{{"name", t.Name}}
	if t.Title != "" {
		out = append(out, toolText{"title", t.Title})
	}
	if t.Description != "" {
		out = append(out, toolText{"description", t.Description})
	}
	var v map[string]any
	if json.Unmarshal(t.Raw, &v) != nil {
		return out
	}
	for _, key := range []string{"inputSchema", "outputSchema", "annotations"} {
		collectStrings(v[key], key, &out)
	}
	return out
}

// collectStrings walks a decoded JSON value and keeps every string value
// (not keys), each with its JSON path.
func collectStrings(v any, at string, out *[]toolText) {
	switch x := v.(type) {
	case string:
		if strings.TrimSpace(x) != "" {
			*out = append(*out, toolText{at, x})
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k == "type" || k == "$schema" || k == "format" || k == "pattern" {
				continue
			}
			collectStrings(x[k], at+"."+k, out)
		}
	case []any:
		for i, e := range x {
			collectStrings(e, fmt.Sprintf("%s[%d]", at, i), out)
		}
	}
}

// scanRawTexts runs the text rules over every string of a prompt or
// resource entry.
func scanRawTexts(where string, raw json.RawMessage, opts TextOptions) []schemas.AdmitScanFinding {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ScanText(where, raw, opts)
	}
	var texts []toolText
	collectStrings(v, "", &texts)
	var out []schemas.AdmitScanFinding
	for _, t := range texts {
		out = append(out, ScanText(where+strings.ReplaceAll(t.where, ".", "/"), []byte(t.text), opts)...)
	}
	return out
}

// itemName is a prompt's or resource's name (or uri), else its index.
func itemName(raw json.RawMessage, i int) string {
	var v struct {
		Name string `json:"name"`
		URI  string `json:"uri"`
	}
	if json.Unmarshal(raw, &v) == nil {
		if v.Name != "" {
			return v.Name
		}
		if v.URI != "" {
			return v.URI
		}
	}
	return fmt.Sprintf("%d", i)
}

// shadowFindings reports a text that names a tool of another server or the
// agent, names a sibling tool, or tells the model how to use other tools.
func shadowFindings(where, self, text string, siblings map[string]bool) []schemas.AdmitScanFinding {
	var out []schemas.AdmitScanFinding
	who := "tool " + self
	if strings.HasPrefix(self, "(") {
		who = "the server instructions"
	}
	for _, name := range WellKnownTools {
		if name == self || siblings[name] {
			continue
		}
		if mentionsTool(text, name) {
			out = append(out, schemas.AdmitScanFinding{Rule: RuleShadowForeign, Severity: schemas.AdmitScanSeverityBlock, File: where, Excerpt: Excerpt(text, toolColumn(text, name)), Detail: fmt.Sprintf("%s names %s, a tool this server does not provide", who, name)})
		}
	}
	for name := range siblings {
		if name != self && mentionsTool(text, name) {
			out = append(out, schemas.AdmitScanFinding{Rule: RuleShadowSibling, Severity: schemas.AdmitScanSeverityInfo, File: where, Detail: fmt.Sprintf("%s names sibling tool %s", who, name)})
		}
	}
	for _, re := range shadowDirectiveRes {
		if loc := re.FindStringIndex(text); loc != nil {
			col := len([]rune(text[:loc[0]])) + 1
			out = append(out, schemas.AdmitScanFinding{Rule: RuleShadowDirective, Severity: schemas.AdmitScanSeverityBlock, File: where, Excerpt: Excerpt(text, col), Detail: fmt.Sprintf("%s tells the model how to use other tools", who)})
			break
		}
	}
	sortFindings(out)
	return out
}

// mentionsTool reports name as a whole word in text. Short or common
// English names (Read, Write, Task, query) count only in code style
// (`Read`), as name(, or followed by "tool".
func mentionsTool(text, name string) bool {
	re := regexp.MustCompile(`(?:^|[^\w])` + regexp.QuoteMeta(name) + `(?:[^\w]|$)`)
	if !re.MatchString(text) {
		return false
	}
	if strings.Contains(name, "_") || strings.ToLower(name) != name && len(name) > 5 && !commonWord(name) {
		return true
	}
	strict := regexp.MustCompile("`" + regexp.QuoteMeta(name) + "`|" + regexp.QuoteMeta(name) + `\s*\(|(?i:\b` + regexp.QuoteMeta(name) + `\s+tool\b)`)
	return strict.MatchString(text)
}

func commonWord(name string) bool {
	switch strings.ToLower(name) {
	case "read", "write", "edit", "glob", "grep", "task", "query", "fetch", "shell", "bash", "exec", "terminal", "multiedit", "webfetch", "websearch", "todowrite":
		return true
	}
	return false
}

func toolColumn(text, name string) int {
	if i := strings.Index(text, name); i >= 0 {
		return len([]rune(text[:i])) + 1
	}
	return 1
}

// nameFindings warns about a tool named like a common tool of another
// server or of the agent itself (the MCP Security Bench name-collision
// attack).
func nameFindings(server, name string) []schemas.AdmitScanFinding {
	for _, w := range WellKnownTools {
		if strings.EqualFold(w, name) {
			return []schemas.AdmitScanFinding{{Rule: RuleNameCollision, Severity: schemas.AdmitScanSeverityWarn, File: mcpWhere(server, "tools/"+name+"/name"), Detail: fmt.Sprintf("tool %s has the name of a common tool of other servers or agents; a client may pick the wrong one", name)}}
		}
	}
	return nil
}

// errRPC is the error of a JSON-RPC answer that carries an error object.
var errRPC = errors.New("json-rpc error")

// rpcErr wraps an RPCError as an error.
func rpcErr(method string, e *RPCError) error {
	return fmt.Errorf("%s: %w %d: %s", method, errRPC, e.Code, e.Message)
}
