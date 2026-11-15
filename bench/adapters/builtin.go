package adapters

import "github.com/maximhq/bifrost/bench/schemas"

// Builtin lists every agent adapter shipped with the bench. Removing a line
// removes the adapter.
func Builtin(cfg CommandConfig) []schemas.AgentAdapter {
	return []schemas.AgentAdapter{
		newCommandAdapter(cfg),
		newScriptedAdapter(cfg),
		newRemoteHTTPAdapter(cfg), // Remote area: agents that are deployed services (remotehttp.go)
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
	}
}
