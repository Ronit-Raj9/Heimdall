package main

// MCP onboarding tools (lane Z02, audit/INFRA_I2_AGENT_ONBOARDING.md 5.3):
// connect_agent, verify_agent and first_look, so the IDE's own agent
// (Cursor, Claude Code, VS Code) can connect the agent the user has open.
// The local stdio server reads the workspace; Heimdall never edits code.
// This file's init runs after mcpserve.go's (file name order), which builds
// the tool table.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func init() {
	mcpToolTable["connect_agent"] = mcpTool{
		description: "Connect an AI agent to Heimdall. Pass path (a folder of the workspace: detects LangGraph, ADK, CrewAI, OpenAI Agents SDK, Pydantic AI, smolagents, DSPy or a plain function) " +
			"or target (an http(s) URL or a curl command). Returns what was found and how it will run. With register true it also creates the agent and writes .heimdall/agent.yaml in path; " +
			"nothing in the user's code is changed. A local agent then needs `heimdall serve .` running in a terminal.",
		schema: obj(map[string]any{
			"path":     str("Folder to detect the agent in (default the workspace root, the current folder)"),
			"target":   str("An agent URL or a curl command instead of a folder"),
			"name":     str("Agent name; default from the folder, host or object"),
			"local":    map[string]any{"type": "boolean", "description": "Check and relay URLs from this machine even when the server could reach them"},
			"register": map[string]any{"type": "boolean", "description": "Create the agent and write .heimdall/agent.yaml (default false: only propose)"},
			"headers": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"},
				"description": "Headers for the agent; use ${ENV_VAR} values, never a literal secret"},
		}),
		run: connectAgentTool,
	}
	mcpToolTable["verify_agent"] = mcpTool{
		description: "Check that an agent works before evaluating it: reachability, auth, request shape, answer, sessions, and one real task when the server can (Z01 agent.verify); " +
			"for an agent served from this machine, whether `heimdall serve` is running. Read only.",
		schema: obj(map[string]any{"agent": str("Agent id")}, "agent"), readOnly: true,
		run: verifyAgentTool,
	}
	mcpToolTable["first_look"] = mcpTool{
		description: "A first look at an agent: 5 tasks by 1 trial on the fastest benchmark it fits. Without confirm it returns the plan and the cost and time estimate only. " +
			"With confirm true it starts the evaluation (real runs on the organization's own provider keys) and returns its id and report card link; read it later with get_report. " +
			"Too small for a verdict; never a release gate.",
		schema: obj(map[string]any{
			"agent":     str("Agent id"),
			"model":     str("Model; default the agent's own"),
			"benchmark": str("Benchmark id instead of the fastest one"),
			"confirm":   map[string]any{"type": "boolean", "description": "Start it (spends on the org's keys). Ask the user first."},
		}, "agent"),
		run: firstLookTool,
	}
}

func (s *mcpServer) onboard() *onboardAPI {
	a := newOnboardAPI(s.api.base, s.api.token)
	if s.api.client != nil {
		a.client = s.api.client
	}
	return a
}

func connectAgentTool(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
	var args struct {
		Path     string            `json:"path"`
		Target   string            `json:"target"`
		Name     string            `json:"name"`
		Local    bool              `json:"local"`
		Register bool              `json:"register"`
		Headers  map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if args.Path != "" && args.Target != "" {
		return nil, errors.New("pass path or target, not both")
	}
	dir := args.Path
	if dir == "" {
		dir = "."
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("path %q is not a folder", dir)
	}
	for k, v := range args.Headers {
		if !strings.Contains(v, "${") && looksSecret(v) {
			return nil, fmt.Errorf("header %s holds a literal secret; pass ${ENV_VAR} and have the user export it, so no secret passes through the model", k)
		}
	}
	api := s.onboard()
	prop, err := proposeConnect(ctx, api, args.Target, dir, args.Headers, args.Local)
	if err != nil {
		return nil, err
	}
	if args.Name != "" {
		prop.rename(agentNameFrom(args.Name))
	}
	out := map[string]any{"proposal": prop}
	if prop.Config == "" {
		out["registered"] = false
		out["next"] = prop.NextSteps
		return out, nil
	}
	if !args.Register {
		out["registered"] = false
		out["next"] = "call connect_agent again with register true to create agent " + prop.Agent + " and write .heimdall/agent.yaml"
		return out, nil
	}
	res, err := applyConnect(ctx, api, prop, dir, s.api.base)
	if err != nil {
		return nil, err
	}
	out["registered"] = true
	out["result"] = res
	next := []string{}
	if prop.Serve != nil {
		abs, _ := filepath.Abs(dir)
		next = append(next, "the user runs `heimdall serve .` in "+abs+" and keeps it running (outbound only)")
		for _, env := range prop.SecretEnv {
			next = append(next, "the user exports "+env+" in that terminal first")
		}
	}
	next = append(next, "call verify_agent with agent "+prop.Agent, "then first_look with agent "+prop.Agent)
	out["next"] = next
	return out, nil
}

// looksSecret flags header values that look like a credential.
func looksSecret(v string) bool {
	v = strings.TrimSpace(v)
	low := strings.ToLower(v)
	for _, p := range []string{"bearer ", "basic ", "token "} {
		if strings.HasPrefix(low, p) && len(v) > len(p)+8 {
			return true
		}
	}
	return len(v) >= 24 && !strings.ContainsAny(v, " /")
}

func verifyAgentTool(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
	var args struct {
		Agent string `json:"agent"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || args.Agent == "" {
		return nil, errors.New("agent is required")
	}
	api := s.onboard()
	pull := isPullAgent(ctx, api, args.Agent)
	v, err := api.verify(ctx, args.Agent, pull)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"verify": v}
	if !v.OK && pull {
		out["next"] = "the user starts `heimdall serve .` in the agent's folder, then call verify_agent again"
	}
	return out, nil
}

// isPullAgent reads the agent's config: adapter pull.
func isPullAgent(ctx context.Context, api *onboardAPI, id string) bool {
	var doc map[string]any
	if _, err := api.call(ctx, "GET", fmt.Sprintf(agentConfigPath, id), nil, &doc); err != nil {
		return false
	}
	if y, _ := doc["yaml"].(string); strings.Contains(y, "adapter: pull") {
		return true
	}
	if f, ok := doc["fields"].(map[string]any); ok && f["adapter"] == "pull" {
		return true
	}
	return false
}

func firstLookTool(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
	var args struct {
		Agent     string `json:"agent"`
		Model     string `json:"model"`
		Benchmark string `json:"benchmark"`
		Confirm   bool   `json:"confirm"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || args.Agent == "" {
		return nil, errors.New("agent is required")
	}
	api := s.onboard()
	plan, err := api.planFirstLook(ctx, args.Agent, args.Model, args.Benchmark)
	if err != nil {
		return nil, err
	}
	if !args.Confirm {
		return map[string]any{"plan": plan, "started": false,
			"next": "show the user the estimate; if they agree, call first_look again with confirm true"}, nil
	}
	ev, err := api.startFirstLook(ctx, plan, true)
	if err != nil {
		return nil, err
	}
	id, _ := ev["id"].(string)
	return map[string]any{"plan": plan, "started": true, "evaluation": ev, "report_link": evaluationLink(s.api.base, id),
		"next": "call get_report with evaluation_id " + id + " once its status is done"}, nil
}
