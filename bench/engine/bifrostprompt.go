package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/maximhq/bifrost/bench/schemas"
)

// W4 feature 2 (audit/W4_DESIGN_3_BIFROST.md section 5): an arm whose
// gateway.bifrost.prompt holds a Prompt Repository version gets its own copy
// of that version on the run's gateway, named hm-prompt-<run id>. The agent
// sends the copy's id and version as request headers and Bifrost's prompts
// plugin puts the messages before the agent's own on every model request.

// bifrostPromptGateway is implemented by gateways that can hold a run's copy
// of a prompt version (gateways/bifrost/promptrepo.go).
type bifrostPromptGateway interface {
	CreateRunPrompt(ctx context.Context, runID string, p schemas.BifrostPrompt) (promptID string, version int, err error)
	RemoveRunPrompt(ctx context.Context, runID string) error
}

// bifrostPrompt is the arm's prompt version, or nil.
func bifrostPrompt(cfg *schemas.AgentConfig) *schemas.BifrostPrompt {
	if cfg == nil || cfg.Gateway.Bifrost == nil {
		return nil
	}
	return cfg.Gateway.Bifrost.Prompt
}

// serveBifrostPrompt creates the run's prompt copy after the key was minted
// and puts its id and version into rc.GatewayHeaders; a failure fails the
// prepare stage, so the agent never runs without its prompt.
func (p *Pipeline) serveBifrostPrompt(ctx context.Context, st *runState) error {
	pr := bifrostPrompt(st.rc.Agent)
	if pr == nil {
		return nil
	}
	gw, ok := schemas.GatewayAs[bifrostPromptGateway](p.reg.Gateway)
	if !ok {
		return errors.New("gateway.bifrost.prompt: the gateway cannot hold a copy of a prompt version")
	}
	if st.rc.Run == nil {
		return errors.New("gateway.bifrost.prompt: the run has no id")
	}
	id, version, err := gw.CreateRunPrompt(ctx, st.rc.Run.RunID, *pr)
	if err != nil {
		return fmt.Errorf("create the run's copy of prompt %s v%d: %w", pr.Name, pr.Version, err)
	}
	if st.rc.GatewayHeaders == nil {
		st.rc.GatewayHeaders = map[string]string{}
	}
	st.rc.GatewayHeaders["x-bf-prompt-id"] = id
	st.rc.GatewayHeaders["x-bf-prompt-version"] = strconv.Itoa(version)
	return nil
}

// removeBifrostPrompt deletes the copy at cleanup; safe to call twice.
func (p *Pipeline) removeBifrostPrompt(ctx context.Context, st *runState) error {
	if st == nil || st.rc == nil || bifrostPrompt(st.rc.Agent) == nil || st.rc.Run == nil {
		return nil
	}
	gw, ok := schemas.GatewayAs[bifrostPromptGateway](p.reg.Gateway)
	if !ok {
		return nil
	}
	if err := gw.RemoveRunPrompt(ctx, st.rc.Run.RunID); err != nil {
		return fmt.Errorf("remove the run's prompt copy: %w", err)
	}
	return nil
}
