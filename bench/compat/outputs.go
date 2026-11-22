package compat

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Output rules (lane X10, J5). An agent that is not beside the workspace (a
// remote_http endpoint, an A2A agent, an OpenAI-compatible endpoint) can
// only produce some output kinds, and a state task needs a tool it can
// reach. The planner refuses such a task per run (engine/scenarioremote.go,
// engine/a2a.go, engine/openaiagent.go); TaskOK applies the same rules up
// front so the compatibility API and evaluations grey a benchmark out with
// the reason instead of failing after launch.

// TaskOK reports whether agent can produce task t's output, and why not in
// one plain sentence. Agents beside the workspace (command, scripted) run
// every output kind. It is pure.
func TaskOK(agent *schemas.AgentConfig, s *schemas.Suite, t *schemas.Task) (bool, string) {
	if agent == nil || t == nil {
		return true, ""
	}
	kind := utils.OutputKind(s, t)
	switch {
	case utils.IsOpenAIAgent(agent):
		if ok, _ := utils.OpenAIOutputAllowed(agent.OpenAI, kind); !ok {
			if kind == schemas.OutputState {
				return false, "it checks the state of the task's tools, and this endpoint has tool calls turned off (openai.tools: off)"
			}
			return false, outsideReason(kind, "an OpenAI-compatible endpoint")
		}
		if kind == schemas.OutputState && !hasMCP(s, t) {
			return false, "it checks the state of the task's tools, and its tasks have no MCP tools an OpenAI-compatible endpoint could call"
		}
		return true, ""
	case utils.IsA2A(agent):
		return remoteTaskOK(utils.A2ARemoteView(agent.A2A), s, t, kind, "an A2A agent", "a2a")
	case agent.Adapter == "remote_http" || agent.Remote != nil:
		return remoteTaskOK(agent.Remote, s, t, kind, "your endpoint", "remote")
	case isVoicePlatform(agent): // voiceplatform.go
		return voicePlatformTaskOK(agent, t)
	}
	return true, ""
}

// ChecksTasks reports whether TaskOK can refuse a task for agent: only an
// agent outside the workspace (an OpenAI-compatible endpoint, an A2A agent,
// a remote_http endpoint) has output rules. Callers that read only a
// benchmark's head use it to know when the tasks must be loaded.
func ChecksTasks(agent *schemas.AgentConfig) bool {
	if agent == nil {
		return false
	}
	return utils.IsOpenAIAgent(agent) || utils.IsA2A(agent) || agent.Adapter == "remote_http" || agent.Remote != nil || isVoicePlatform(agent)
}

func remoteTaskOK(r *schemas.RemoteAgent, s *schemas.Suite, t *schemas.Task, kind, who, block string) (bool, string) {
	if ok, _ := utils.RemoteOutputAllowed(r, kind); ok {
		if kind == schemas.OutputState && !publishable(s, t) {
			return false, "it checks the state of the task's tools, and its tasks have no tool " + who + " could reach"
		}
		return true, ""
	}
	switch kind {
	case schemas.OutputState:
		return false, fmt.Sprintf("it checks the state of the task's tools, and %s does not get Heimdall's tools: turn on tool access (%s.tools: expose)", who, block)
	case schemas.OutputArtifact:
		return false, fmt.Sprintf("it grades files the agent produces, and %s does not return files in its answer: turn on file answers (%s.artifacts: inline)", who, block)
	}
	return false, outsideReason(kind, who)
}

func outsideReason(kind, who string) string {
	if kind == schemas.OutputDiff {
		return "it grades changes to a code workspace, and " + who + " runs outside that workspace"
	}
	return "it grades a " + kind + " output, and " + who + " runs outside the workspace"
}

// publishable mirrors engine.hasPublishable: a proxied, visible service or
// an external API that the remote tool gate would publish.
func publishable(s *schemas.Suite, t *schemas.Task) bool {
	if len(utils.ExternalAPIs(s, t)) > 0 {
		return true
	}
	for _, sv := range utils.TaskServices(s, t) {
		if !sv.Hidden && utils.ServiceProxied(sv) {
			return true
		}
	}
	return false
}

// hasMCP mirrors engine.hasMCPService.
func hasMCP(s *schemas.Suite, t *schemas.Task) bool {
	for _, sv := range utils.TaskServices(s, t) {
		if sv.Role == schemas.ServiceRoleMCP && !sv.Hidden {
			return true
		}
	}
	return false
}

// RunnableTasks splits ids (task ids of s) into the ones agent can run and
// the first refusal reason among the others ("" when all run).
func RunnableTasks(agent *schemas.AgentConfig, s *schemas.Suite, ids []string) (runnable []string, reason string) {
	for _, id := range ids {
		var t *schemas.Task
		if s != nil {
			t = s.TaskByID(id)
		}
		ok, why := TaskOK(agent, s, t)
		if ok {
			runnable = append(runnable, id)
		} else if reason == "" {
			reason = why
		}
	}
	return runnable, reason
}

// Full is Check plus the output rules over every task of s: an agent that
// cannot run any of the benchmark's tasks is not ok, with the reason. When
// only some tasks are blocked it is ok, and note says how many it skips.
// The compatibility API and evaluations use Full; the planner keeps Check
// for capabilities and refuses blocked tasks per run (checkScenario).
func Full(agent *schemas.AgentConfig, s *schemas.Suite) (ok bool, missing []schemas.Capability, reason, note string) {
	ok, missing, reason = Check(agent, s)
	if !ok || len(s.Tasks) == 0 {
		return ok, missing, reason, ""
	}
	ids := make([]string, 0, len(s.Tasks))
	for _, t := range s.Tasks {
		ids = append(ids, t.ID)
	}
	runnable, why := RunnableTasks(agent, s, ids)
	switch {
	case why == "":
		return true, nil, "", ""
	case len(runnable) == 0:
		return false, nil, fmt.Sprintf("Agent %s cannot run benchmark %s: %s.", agent.ID, s.ID, why), ""
	}
	return true, nil, "", fmt.Sprintf("%d of %d tasks are skipped: %s.", len(ids)-len(runnable), len(ids), why)
}
