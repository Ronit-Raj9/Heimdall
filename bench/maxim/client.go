package maxim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/export"
	"github.com/maximhq/bifrost/bench/traceexport"
)

const (
	clientTimeout = 30 * time.Second
	maxAnswer     = 1 << 20
	otelPath      = "/v1/otel"
	reposPath     = "/v1/log-repositories"
)

// ErrUnauthorized is a key Maxim refused.
var ErrUnauthorized = errors.New("maxim refused the api key")

// Client talks to Maxim's API. A zero Client uses the public cloud.
type Client struct {
	HTTP    *http.Client
	APIBase string // default DefaultAPIBase; the OTLP endpoint is APIBase + /v1/otel
}

// Repository is the part of a log repository the test reads.
type Repository struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	WorkspaceID string `json:"workspaceId"`
}

// Test checks a key, workspace and repository: the repository must be
// listed in the workspace for that key. It returns the repository.
func (c Client) Test(ctx context.Context, apiKey, workspaceID, repoID string) (*Repository, error) {
	q := url.Values{"workspaceId": {workspaceID}, "id": {repoID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+reposPath+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("maxim test: %w", err)
	}
	req.Header.Set("x-maxim-api-key", apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("maxim test: %s", redactKey(err.Error(), apiKey))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return nil, fmt.Errorf("maxim test: read answer: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, ErrUnauthorized
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("maxim has no log repository %s in workspace %s", repoID, workspaceID)
	case resp.StatusCode/100 != 2:
		return nil, fmt.Errorf("maxim answered %d: %s", resp.StatusCode, redactKey(trimBody(body), apiKey))
	}
	var ans struct {
		Data []Repository `json:"data"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		return nil, fmt.Errorf("maxim test: answer is not a repository list: %w", err)
	}
	for _, r := range ans.Data {
		if r.ID == repoID {
			return &r, nil
		}
	}
	return nil, fmt.Errorf("maxim has no log repository %s in workspace %s", repoID, workspaceID)
}

// PushTrace sends one run's trace to the repository through the OTLP Maxim
// preset: its attribute mapping (maxim.metadata with the run facts as tags,
// the experiment as the session), its encoding and its headers, built from
// the key and repository instead of environment variables.
func (c Client) PushTrace(ctx context.Context, apiKey, repoID string, td *traceexport.TracesData) error {
	p, ok := export.Presets()["maxim"]
	if !ok {
		return errors.New("maxim preset missing")
	}
	vals := map[string]string{"MAXIM_API_KEY": apiKey, "MAXIM_LOG_REPO_ID": repoID}
	headers, err := p.PresetHeaders(func(k string) (string, bool) { v, ok := vals[k]; return v, ok })
	if err != nil {
		return err
	}
	p.Apply(td)
	body, ctype, err := p.Encode(td)
	if err != nil {
		return fmt.Errorf("encode trace: %w", err)
	}
	if err := export.PostTrace(ctx, c.http(), c.base()+otelPath, headers, body, ctype); err != nil {
		return errors.New(redactKey(err.Error(), apiKey))
	}
	return nil
}

func (c Client) base() string {
	if c.APIBase == "" {
		return DefaultAPIBase
	}
	return strings.TrimRight(c.APIBase, "/")
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: clientTimeout}
}

// redactKey removes the key from text that may echo it.
func redactKey(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "[key]")
}
