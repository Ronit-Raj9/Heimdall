package traceimport

import (
	"fmt"
	"strings"
	"time"
)

// mcpHelper is the MCP client of the reference script, the same streamable
// HTTP JSON-RPC exchange the mcp template's reference uses.
const mcpHelper = `import json, os, sys, urllib.request

SERVICE = %s
servers = json.loads(os.environ.get("BENCH_MCP_SERVERS") or "{}")
URL = (servers.get(SERVICE) or {}).get("url") or f"http://toolproxy:9200/s/{SERVICE}/mcp"
session = {}


def rpc(method, params=None, notify=False):
    msg = {"jsonrpc": "2.0", "method": method, "params": params or {}}
    if not notify:
        msg["id"] = rpc.next_id = getattr(rpc, "next_id", 0) + 1
    headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
    headers.update(session)
    req = urllib.request.Request(URL, data=json.dumps(msg).encode(), headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=30) as resp:
        if resp.headers.get("Mcp-Session-Id"):
            session["Mcp-Session-Id"] = resp.headers["Mcp-Session-Id"]
        body = resp.read().decode()
    if notify:
        return None
    if body.lstrip().startswith("data:") or "\ndata:" in body:
        body = [l[5:] for l in body.splitlines() if l.startswith("data:")][-1]
    answer = json.loads(body)
    if "error" in answer:
        sys.exit(f"{method}: {answer['error']}")
    return answer["result"]


def tool(name, args):
    result = rpc("tools/call", {"name": name, "arguments": args})
    text = "".join(c.get("text", "") for c in result.get("content", []))
    if result.get("isError"):
        sys.exit(f"{name}: {text}")
    return text


rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "reference", "version": "1"}})
rpc("notifications/initialized", notify=True)
`

// httpHelper is the HTTP client of a tool-use reference script; tool() is
// httpMapTool or httpReplayTool.
const httpHelper = `import json, os, sys, urllib.error, urllib.request

URL = os.environ.get("TOOL_URL") or "http://toolproxy:9200/s/" + %s


def call(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(URL.rstrip("/") + path, data=data, method=method,
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        sys.exit(f"{method} {path}: {e.code} {e.read().decode()}")


def tool(name, args):
%s


`

// httpMapTool is tool() for a tool server the trace does not describe: the
// trace names tools, not HTTP routes, so it refuses to run until a person
// maps each tool to the server's route.
const httpMapTool = `    # Map each tool of the trace to the tool server's route, for example:
    #   if name == "get_item": return call("GET", "/items/" + args["sku"])
    sys.exit(f"map tool {name} to a call(method, path, body) of the tool server before admitting")`

// httpReplayTool is tool() for the replay server's plain HTTP routes.
const httpReplayTool = `    return call("POST", "/tools/" + name, args)`

// referenceScript is the reference control skeleton: the observed calls in
// order, each with its recorded result as a comment.
func referenceScript(s *Session, tmpl, service string, replay bool) []byte {
	header := "# EDIT ME: make the calls a correct agent makes in this task, then delete this line.\n" +
		"# Reference control drafted by `bifrost-bench task from-trace`. The calls below\n" +
		"# are the ones the recorded session made, in order (" + ObservedToolsFile + " holds\n" +
		"# their recorded results). They are what happened, not necessarily what is\n" +
		"# right: keep the correct calls, fix or drop the rest. Admission runs this\n" +
		"# script in the agent's place and requires the verifier to pass it.\n"
	return callScript(header, s, tmpl, service, replay, false)
}

// mutantScript is the mutant control of a draft from a failed session: the
// recorded calls exactly as the session made them, carrying on past tool
// errors. Admission requires the verifier to fail it, which proves the
// checks catch the failure the trace recorded.
func mutantScript(s *Session, tmpl, service string) []byte {
	header := "# Mutant control drafted by `bifrost-bench task from-trace`: the calls the\n" +
		"# recorded session made, which the trace marks as failed, replayed as they\n" +
		"# were. Admission requires the verifier to fail it, so the checks catch the\n" +
		"# failure that happened in production. Delete it if the session was right.\n"
	return callScript(header, s, tmpl, service, true, true)
}

// callScript renders a control script that makes the session's tool calls
// in order. keepGoing reports tool errors instead of stopping on them.
func callScript(header string, s *Session, tmpl, service string, replay, keepGoing bool) []byte {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n" + header)
	b.WriteString("exec python3 -I - <<'PY'\n")
	helper := ""
	if tmpl == KindMCP {
		helper = fmt.Sprintf(mcpHelper, quote(service))
	} else {
		body := httpMapTool
		if replay {
			body = httpReplayTool
		}
		helper = fmt.Sprintf(httpHelper, quote(service), body)
	}
	if keepGoing {
		helper = strings.Replace(helper, `        sys.exit(f"{name}: {text}")`, `        print(f"{name}: {text}", file=sys.stderr)`, 1)
		helper = strings.Replace(helper, `        sys.exit(f"{method} {path}: {e.code} {e.read().decode()}")`,
			`        print(f"{method} {path}: {e.code} {e.read().decode()}", file=sys.stderr)`, 1)
	}
	b.WriteString(helper)
	if len(s.ToolCalls) == 0 {
		b.WriteString("# The recorded session made no tool calls.\n")
	}
	for _, c := range s.ToolCalls {
		outcome := "-> " + oneLine(c.Result, 160)
		if c.IsError {
			outcome = "failed: " + oneLine(firstNonEmpty(c.Error, c.Result), 160)
		}
		fmt.Fprintf(&b, "# %d. %s %s\n", c.Seq, oneLine(c.Name, 80), outcome)
		fmt.Fprintf(&b, "tool(%s, json.loads(%s))\n", quote(c.Name), quote(string(c.Arguments)))
	}
	if keepGoing && strings.TrimSpace(s.FinalAnswer) != "" {
		// The recorded agent's final answer is this control's final
		// message, so a check on the answer can fail the recorded failure.
		b.WriteString("# The recorded final answer.\n")
		fmt.Fprintf(&b, "print(%s)\nPY\n", quote(s.FinalAnswer))
		return []byte(b.String())
	}
	b.WriteString("print(\"calls done\")\nPY\n")
	return []byte(b.String())
}

// transcript is from-trace.md: the whole recorded session, indented so no
// content can close a code block.
func transcript(s *Session, src Source) []byte {
	var b strings.Builder
	b.WriteString("# Recorded session behind this draft\n\n")
	b.WriteString("Written by `bifrost-bench task from-trace`. It is the session as the trace\n")
	b.WriteString("recorded it, for the person editing the draft; the agent never sees it.\n\n")
	redacted := "off"
	if src.Redacted != nil {
		redacted = "on, scrubbed " + src.Redacted.String()
	}
	fmt.Fprintf(&b, "- Source: %s file %s, sha256 %s\n", src.Format, src.File, src.SHA256)
	fmt.Fprintf(&b, "- Redaction: %s\n", redacted)
	fmt.Fprintf(&b, "- Session: %s", s.ID)
	if s.Name != "" {
		fmt.Fprintf(&b, " (%s)", oneLine(s.Name, 80))
	}
	b.WriteString("\n")
	if s.Model != "" {
		fmt.Fprintf(&b, "- Model: %s\n", s.Model)
	}
	if !s.Started.IsZero() {
		fmt.Fprintf(&b, "- Time: %s to %s\n", stamp3339(s.Started), stamp3339(s.Ended))
	}
	fmt.Fprintf(&b, "- Outcome: %s\n", outcomeLine(s))
	fmt.Fprintf(&b, "- Turns: %d, tool calls: %d (%d failed)\n", len(s.Turns), len(s.ToolCalls), s.ToolErrors())
	if len(s.Skipped) > 0 || s.Truncated > 0 {
		var parts []string
		for k, v := range s.Skipped {
			parts = append(parts, fmt.Sprintf("%d %s", v, k))
		}
		if s.Truncated > 0 {
			parts = append(parts, fmt.Sprintf("%d fields truncated", s.Truncated))
		}
		fmt.Fprintf(&b, "- Not imported: %s\n", strings.Join(sortedCopy(parts), ", "))
	}
	b.WriteString("\n## Conversation\n")
	for i, t := range s.Turns {
		fmt.Fprintf(&b, "\n### %d. %s", i+1, t.Role)
		if !t.Time.IsZero() {
			fmt.Fprintf(&b, " (%s)", stamp3339(t.Time))
		}
		b.WriteString("\n\n" + indent(t.Text) + "\n")
	}
	if len(s.ToolCalls) > 0 {
		b.WriteString("\n## Tool calls\n")
	}
	for _, c := range s.ToolCalls {
		fmt.Fprintf(&b, "\n### %d. %s", c.Seq, c.Name)
		if c.IsError {
			b.WriteString(" (failed)")
		}
		b.WriteString("\n\nArguments:\n\n" + indent(string(c.Arguments)) + "\n")
		if c.Result != "" {
			b.WriteString("\nResult:\n\n" + indent(c.Result) + "\n")
		}
		if c.Error != "" {
			b.WriteString("\nError:\n\n" + indent(c.Error) + "\n")
		}
	}
	if s.FinalAnswer != "" {
		b.WriteString("\n## Final answer\n\n" + indent(s.FinalAnswer) + "\n")
	}
	return []byte(b.String())
}

// conversationFile is what a memory task's first session reads: the recorded
// conversation before the last user request.
func conversationFile(s *Session) []byte {
	last := -1
	for i, t := range s.Turns {
		if t.Role == "user" {
			last = i
		}
	}
	var b strings.Builder
	b.WriteString("# Earlier conversation\n")
	for _, t := range s.Turns[:max(last, 0)] {
		if t.Role == "system" {
			continue
		}
		fmt.Fprintf(&b, "\n**%s:**\n\n%s\n", t.Role, strings.TrimSpace(t.Text))
	}
	return []byte(b.String())
}

func indent(s string) string {
	if s == "" {
		return "    (empty)"
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}

func stamp3339(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.UTC().Format(time.RFC3339)
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
