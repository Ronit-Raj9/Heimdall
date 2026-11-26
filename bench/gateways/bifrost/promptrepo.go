package bifrost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/maximhq/bifrost/bench/schemas"
)

// W4 feature 2 (audit/W4_DESIGN_3_BIFROST.md section 5): the run's copy of a
// Prompt Repository version. Bifrost's prompts plugin reads x-bf-prompt-id
// and x-bf-prompt-version on each request and puts the version's messages
// before the request's own. The copy is named hm-prompt-<run id> so cleanup
// can find it, and it is reachable only by its id.
const (
	pathPrompts      = "/api/prompt-repo/prompts"
	runPromptPrefix  = "hm-prompt-"
	runPromptMessage = "Heimdall run "
)

// RunPromptName is the name of run runID's prompt copy.
func RunPromptName(runID string) string { return runPromptPrefix + runID }

// CreateRunPrompt creates the run's prompt copy with one version holding the
// arm's messages and model params, and returns its id and version number. A
// failed version call deletes the new prompt.
func (g *Gateway) CreateRunPrompt(ctx context.Context, runID string, p schemas.BifrostPrompt) (string, int, error) {
	var created struct {
		Prompt struct {
			ID any `json:"id"`
		} `json:"prompt"`
	}
	if err := g.call(ctx, http.MethodPost, pathPrompts, map[string]any{"name": RunPromptName(runID)}, &created); err != nil {
		return "", 0, err
	}
	id := promptID(created.Prompt.ID)
	if id == "" {
		return "", 0, errors.New("bifrost POST " + pathPrompts + ": answer has no prompt id")
	}
	body := map[string]any{
		"commit_message": runPromptMessage + runID,
		"messages":       p.Messages,
		"model_params":   p.ModelParams,
		"provider":       "",
		"model":          "",
	}
	var ver struct {
		Version struct {
			VersionNumber int `json:"version_number"`
		} `json:"version"`
	}
	if err := g.call(ctx, http.MethodPost, pathPrompts+"/"+url.PathEscape(id)+"/versions", body, &ver); err != nil {
		if derr := g.deletePrompt(ctx, id); derr != nil {
			return "", 0, errors.Join(err, derr)
		}
		return "", 0, err
	}
	return id, ver.Version.VersionNumber, nil
}

// RemoveRunPrompt deletes every prompt named hm-prompt-<runID>; one already
// gone is not an error, so it is safe to call twice.
func (g *Gateway) RemoveRunPrompt(ctx context.Context, runID string) error {
	var list struct {
		Prompts []struct {
			ID   any    `json:"id"`
			Name string `json:"name"`
		} `json:"prompts"`
	}
	if err := g.call(ctx, http.MethodGet, pathPrompts, nil, &list); err != nil {
		return err
	}
	var errs []error
	for _, pr := range list.Prompts {
		if pr.Name != RunPromptName(runID) {
			continue
		}
		if err := g.deletePrompt(ctx, promptID(pr.ID)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (g *Gateway) deletePrompt(ctx context.Context, id string) error {
	err := g.call(ctx, http.MethodDelete, pathPrompts+"/"+url.PathEscape(id), nil, nil)
	if isStatus(err, http.StatusNotFound) {
		return nil
	}
	return err
}

// promptID is a prompt id as text; Bifrost may answer a string or a number.
func promptID(v any) string {
	switch id := v.(type) {
	case nil:
		return ""
	case string:
		return id
	case float64:
		return fmt.Sprintf("%.0f", id)
	default:
		return fmt.Sprint(id)
	}
}
