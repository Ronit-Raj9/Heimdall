package adapters

import "github.com/maximhq/bifrost/bench/schemas"

// Builtin lists every agent adapter shipped with the bench. Removing a line
// removes the adapter.
func Builtin(cfg CommandConfig) []schemas.AgentAdapter {
	return []schemas.AgentAdapter{
		newCommandAdapter(cfg),
		newScriptedAdapter(cfg),
		newRemoteHTTPAdapter(cfg), // Remote area: agents that are deployed services (remotehttp.go)
		newA2AAdapter(cfg),        // B1: A2A agents through Bifrost's agent gateway (a2a.go)
	}
}

// BuiltinParsers lists every agent output parser shipped with the bench.
func BuiltinParsers() []schemas.Parser {
	return []schemas.Parser{
		newProtocolParser(),
		newClaudeCodeParser(),
		newCodexParser(),
		newOpenCodeParser(),
		newGenericJSONParser(ParserCustomPython),
		newGenericJSONParser(ParserGeminiCLI),
		newGenericJSONParser(ParserMiniSWE),
		newGenericJSONLParser(), // Agent events area: native agent events on stdout (genericjsonl.go)
	}
}
