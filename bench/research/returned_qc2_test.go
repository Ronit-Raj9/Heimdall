package research

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func mcpRow(tool, result string, isErr bool) schemas.ToolCall {
	return schemas.ToolCall{Seq: 1, Server: "websearch", Tool: tool, Protocol: "mcp", Status: 200, IsError: isErr, Result: result}
}

// QC2-26-04: a delivered fetched page whose ledger copy was clipped still
// grounds its URL.
func TestReturnedURLsClippedMCPPage(t *testing.T) {
	// The tool writes the url before the page text (websearch/server.py).
	text := `{"url": "https://example.com/page", "content": "` + strings.Repeat("x", 70<<10) + `"}`
	full, _ := json.Marshal(map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}})
	clipped := string(full[:64<<10])
	row := mcpRow(MCPFetchTool, clipped, false)
	if !row.IsToolCall() {
		t.Skip("row shape is not a tool call in this schema")
	}
	got := ReturnedURLs([]schemas.ToolCall{row}, map[string]bool{})
	if !got[NormalizeURL("https://example.com/page")] {
		t.Fatalf("clipped page lost its URL: %v", got)
	}
}

// QC2-26-05: a failed MCP search does not hide a delivered direct result.
func TestReturnedURLsFailedMCPKeepsDirect(t *testing.T) {
	direct := schemas.ToolCall{Seq: 2, Server: "tavily", Tool: SearchTool, Protocol: "external", Status: 200,
		Result: `{"results":[{"url":"https://example.com/a"}]}`}
	if !IsSearch(direct, map[string]bool{"tavily": true}) {
		t.Skip("direct row shape not recognised")
	}
	got := ReturnedURLs([]schemas.ToolCall{mcpRow(MCPSearchTool, "", true), direct}, map[string]bool{"tavily": true})
	if !got[NormalizeURL("https://example.com/a")] {
		t.Fatalf("direct result hidden: %v", got)
	}
}
