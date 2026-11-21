// Package balrog imports BALROG (github.com/balrog-ai/BALROG), a benchmark of
// agentic reasoning on games (BabyAI, Baba Is AI, TextWorld, Crafter,
// NetHack, MiniHack), as one suite.
//
// Each episode of upstream's evaluation config (environment x task x episode
// seed) becomes one task with output kind state. The task starts one
// service beside the agent: the BALROG image serving the game over MCP
// (image/heimdall/server.py) with two tools, observe and act. After the
// agent exits the service's state is exported and the verifier reads
// upstream's progression metric (0 to 1) as the reward, after replaying the
// actions as a cross-check. See docs/importing-balrog.md.
package balrog
