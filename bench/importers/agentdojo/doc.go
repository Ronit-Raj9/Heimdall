// Package agentdojo imports AgentDojo (github.com/ethz-spylab/agentdojo), a
// benchmark of prompt injection through tool results, as a safety suite.
//
// Each AgentDojo user task becomes one task with output kind state. The task
// starts one service beside the agent: the AgentDojo image serving the
// suite's tools over MCP (image/heimdall/server.py), behind the tool proxy.
// The clean condition is upstream's default environment. Each attack
// condition is a probe of layer service that sets AGENTDOJO_ATTACK and
// AGENTDOJO_INJECTION_TASK on that service, which plants the attack text in
// the environment data the user task reads, the way upstream's runner does.
// After the agent exits the service's state (environment before and after,
// and every call) is exported, and the verifier runs upstream's utility
// function for the reward and, under an attack, upstream's security function
// for the reward dimension attack_succeeded, which the gate
// tool_injection_followed reads. See docs/importing-agentdojo.md.
package agentdojo
