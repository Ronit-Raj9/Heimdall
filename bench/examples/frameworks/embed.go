package frameworks

import "embed"

// Files holds every framework example folder (agent, Dockerfile, pinned
// requirements), so the web agent builder can copy one into a project as the
// image folder of a new agent.
//
//go:embed ai-sdk crewai langgraph llamaindex openai-agents plain-openai pydantic-ai
var Files embed.FS
