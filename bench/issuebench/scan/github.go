package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultAPIURL is GitHub's public API.
const DefaultAPIURL = "https://api.github.com"

const (
	maxGraphQLBody = 16 << 20
	// MaxDiffBytes caps one pull request's diff; a bigger diff is skipped.
	MaxDiffBytes = 2 << 20
)

// ErrDiffTooBig is a diff over MaxDiffBytes.
var ErrDiffTooBig = errors.New("diff too big")

// APIError is a non-2xx answer or a GraphQL error from GitHub.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return "GitHub rejected the token (revoked, expired or wrong); reconnect GitHub in Settings > GitHub or check the token"
	case http.StatusNotFound:
		return "repository not found on GitHub, or the token has no access to it"
	}
	if e.Msg != "" {
		return fmt.Sprintf("GitHub answered %d: %s", e.Status, e.Msg)
	}
	return fmt.Sprintf("GitHub answered %d", e.Status)
}

// Client talks to the GitHub GraphQL and REST APIs with one token. The
// token is a secret: it is only ever sent to APIURL.
type Client struct {
	APIURL string // "" = DefaultAPIURL; GitHub Enterprise: https://host/api/v3
	Token  string
	HTTP   *http.Client
}

func (c *Client) base() string {
	if c.APIURL == "" {
		return DefaultAPIURL
	}
	return strings.TrimRight(c.APIURL, "/")
}

// graphqlURL is APIURL + /graphql; GitHub Enterprise serves it at
// /api/graphql next to /api/v3.
func (c *Client) graphqlURL() string {
	b := c.base()
	if strings.HasSuffix(b, "/api/v3") {
		return strings.TrimSuffix(b, "/v3") + "/graphql"
	}
	return b + "/graphql"
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (c *Client) send(req *http.Request, limit int64) ([]byte, error) {
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &m)
		msg := m.Message
		if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
			msg = "rate limit reached; try again after " + resp.Header.Get("X-RateLimit-Reset")
		}
		return nil, &APIError{Status: resp.StatusCode, Msg: clip(msg, 300)}
	}
	if int64(len(body)) > limit {
		return nil, ErrDiffTooBig
	}
	return body, nil
}

// GraphQL runs one query and decodes data into out.
func (c *Client) GraphQL(ctx context.Context, query string, vars map[string]any, out any) error {
	b, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphqlURL(), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	body, err := c.send(req, maxGraphQLBody)
	if errors.Is(err, ErrDiffTooBig) {
		return errors.New("GitHub answer too big")
	}
	if err != nil {
		return err
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("decode GitHub GraphQL answer: %w", err)
	}
	if len(env.Errors) > 0 {
		st := http.StatusBadGateway
		if env.Errors[0].Type == "NOT_FOUND" {
			st = http.StatusNotFound
		}
		return &APIError{Status: st, Msg: clip(env.Errors[0].Message, 300)}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

// Diff returns the unified diff from base to head (REST compare).
func (c *Client) Diff(ctx context.Context, repo, base, head string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/repos/"+repo+"/compare/"+base+"..."+head, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.diff")
	body, err := c.send(req, MaxDiffBytes)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// FirstParent returns the first parent of a commit (REST), "" for a root.
func (c *Client) FirstParent(ctx context.Context, repo, sha string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/repos/"+repo+"/commits/"+sha, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	// The answer carries the commit's files and patches; allow a big one.
	body, err := c.send(req, maxGraphQLBody)
	if err != nil {
		return "", err
	}
	var cm struct {
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
	}
	if err := json.Unmarshal(body, &cm); err != nil {
		return "", err
	}
	if len(cm.Parents) == 0 {
		return "", nil
	}
	return cm.Parents[0].SHA, nil
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
