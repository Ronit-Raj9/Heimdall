package handlers

import (
	"fmt"
	"strings"
)

// RenderedAgentFile is one file a template writes, relative to the
// project's agents folder.
type RenderedAgentFile struct {
	Rel  string
	Data []byte
}

// RenderAgentRequest renders a POST /agents/from-template request without
// a server, for the CLI's local push of a .heimdall/agent.yaml (lane Z15).
// It checks what the route checks; the first file is <id>.yaml.
func RenderAgentRequest(req AgentFromTemplateRequest) ([]RenderedAgentFile, error) {
	t, ok := findTemplate(req.Template)
	if !ok {
		return nil, fmt.Errorf("unknown template %q", req.Template)
	}
	req.ID = strings.TrimSpace(req.ID)
	if !agentIDRe.MatchString(req.ID) {
		return nil, fmt.Errorf("agent name %q: use lowercase letters, digits, '.', '_' or '-', starting with a letter or digit (max 63)", req.ID)
	}
	files, err := renderAgentTemplate(t, req)
	if err != nil {
		return nil, err
	}
	rel := "agents/" + req.ID + ".yaml"
	if _, err := decodeAgent(files[0].Data, rel); err != nil {
		return nil, fmt.Errorf("the rendered agent does not validate: %w", err)
	}
	out := make([]RenderedAgentFile, 0, len(files))
	for _, f := range files {
		out = append(out, RenderedAgentFile{Rel: f.Rel, Data: f.Data})
	}
	return out, nil
}
