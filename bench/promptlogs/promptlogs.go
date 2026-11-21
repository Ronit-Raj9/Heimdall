// Package promptlogs keeps each organization's gateway prompt logs apart
// (lane X03, audit PROD2 N4). A hosted runner tags every virtual key it mints
// for an organization's run with heimdall_org=<org> (schemas.PromptLogOrgMetadataKey);
// Bifrost copies the tag onto every log row of the key. This package is the
// server's side: the per-organization policy (keep prompt text or not, how
// many days to keep rows), and a client of the shared gateway's log API that
// only ever lists or deletes rows carrying one organization's tag.
package promptlogs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// SettingKey is the org settings row that holds the policy as JSON.
const SettingKey = "prompt_logging"

// SettingsStore is the slice of the database the policy needs (db.DB).
type SettingsStore interface {
	GetSetting(ctx context.Context, orgID, key string) (string, bool, error)
	SetSetting(ctx context.Context, orgID, key, value string) error
}

// LoadPolicy returns the organization's policy, or the default when it never
// chose one.
func LoadPolicy(ctx context.Context, st SettingsStore, orgID string) (schemas.PromptLogPolicy, error) {
	def := schemas.DefaultPromptLogPolicy()
	if st == nil || orgID == "" {
		return def, nil
	}
	raw, found, err := st.GetSetting(ctx, orgID, SettingKey)
	if err != nil {
		return def, err
	}
	if !found || strings.TrimSpace(raw) == "" {
		return def, nil
	}
	var p schemas.PromptLogPolicy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return def, fmt.Errorf("prompt logging setting of %s: %w", orgID, err)
	}
	return p.Normalize(), nil
}

// ValidatePolicy reports why p cannot be saved, or "".
func ValidatePolicy(p schemas.PromptLogPolicy) string {
	if p.RetentionDays < 1 || p.RetentionDays > schemas.PromptLogRetentionMax {
		return fmt.Sprintf("retention_days must be between 1 and %d", schemas.PromptLogRetentionMax)
	}
	return ""
}

// SavePolicy stores the organization's policy.
func SavePolicy(ctx context.Context, st SettingsStore, orgID string, p schemas.PromptLogPolicy) error {
	if st == nil {
		return errors.New("prompt logging: no settings store")
	}
	if orgID == "" {
		return errors.New("prompt logging: no organization")
	}
	if why := ValidatePolicy(p); why != "" {
		return errors.New(why)
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return st.SetSetting(ctx, orgID, SettingKey, string(b))
}

// Row is one gateway log row as an organization sees it: metadata only,
// never the prompt or response text.
type Row struct {
	ID               string    `json:"id"`
	Timestamp        time.Time `json:"timestamp"`
	RunID            string    `json:"run_id,omitempty"`
	Provider         string    `json:"provider,omitempty"`
	Model            string    `json:"model,omitempty"`
	Status           string    `json:"status,omitempty"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	CostUSD          float64   `json:"cost_usd"`
	LatencyMS        float64   `json:"latency_ms"`
	ContentStored    bool      `json:"content_stored"`
}

// Page is one page of an organization's rows.
type Page struct {
	Rows  []Row `json:"rows"`
	Total int64 `json:"total"`
}

// Client talks to the shared gateway's log API with its setup token.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
	// PageSize is the list page size Purge walks with; 0 means 200.
	PageSize int
}

// NewClient returns a client, or nil when url is empty (no shared gateway:
// the routes say logs are not kept on this server).
func NewClient(baseURL, token string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	return &Client{BaseURL: baseURL, Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// setupTokenHeader is the header Bifrost reads the setup token from
// (gateways/bifrost).
const setupTokenHeader = "X-Bifrost-Setup-Token"

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set(setupTokenHeader, c.Token)
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("gateway %s %s: %w", method, strings.SplitN(path, "?", 2)[0], err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snip := strings.TrimSpace(string(raw))
		if len(snip) > 200 {
			snip = snip[:200]
		}
		return fmt.Errorf("gateway %s %s: status %d: %s", method, strings.SplitN(path, "?", 2)[0], resp.StatusCode, snip)
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// listRow is the part of a Bifrost list row this package reads.
type listRow struct {
	ID         string         `json:"id"`
	Timestamp  time.Time      `json:"timestamp"`
	Provider   string         `json:"provider"`
	Model      string         `json:"model"`
	Status     string         `json:"status"`
	Latency    *float64       `json:"latency"`
	Cost       *float64       `json:"cost"`
	Metadata   map[string]any `json:"metadata"`
	TokenUsage *struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"token_usage"`
	ContentHidden bool `json:"content_hidden"` // content logging was off for the call
}

// Query bounds a list.
type Query struct {
	Limit  int
	Offset int
	// Before, when set, keeps rows older than it.
	Before time.Time
}

// list asks the gateway for one page of the organization's rows, newest
// first. The org filter is always set: there is no path to an unfiltered
// list.
func (c *Client) list(ctx context.Context, orgID string, q Query, order string) ([]listRow, int64, error) {
	if orgID == "" {
		return nil, 0, errors.New("prompt logs: no organization")
	}
	v := url.Values{
		"metadata_" + schemas.PromptLogOrgMetadataKey: {orgID},
		"limit":   {strconv.Itoa(q.Limit)},
		"offset":  {strconv.Itoa(q.Offset)},
		"sort_by": {"timestamp"},
		"order":   {order},
	}
	if !q.Before.IsZero() {
		v.Set("end_time", q.Before.UTC().Format(time.RFC3339))
	}
	var page struct {
		Logs  []listRow `json:"logs"`
		Stats *struct {
			TotalRequests int64 `json:"total_requests"`
		} `json:"stats"`
		Pagination *struct {
			TotalCount int64 `json:"total_count"`
		} `json:"pagination"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/logs?"+v.Encode(), nil, &page); err != nil {
		return nil, 0, err
	}
	var total int64
	switch {
	case page.Pagination != nil:
		total = page.Pagination.TotalCount
	case page.Stats != nil:
		total = page.Stats.TotalRequests
	}
	// Defence in depth: a gateway that ignored the filter must not leak
	// another organization's rows.
	rows := page.Logs[:0]
	for _, r := range page.Logs {
		if s, _ := r.Metadata[schemas.PromptLogOrgMetadataKey].(string); s == orgID {
			rows = append(rows, r)
		}
	}
	return rows, total, nil
}

// List returns one page of the organization's rows, newest first.
func (c *Client) List(ctx context.Context, orgID string, q Query) (Page, error) {
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	rows, total, err := c.list(ctx, orgID, q, "desc")
	if err != nil {
		return Page{}, err
	}
	out := Page{Rows: make([]Row, 0, len(rows)), Total: total}
	for _, r := range rows {
		row := Row{ID: r.ID, Timestamp: r.Timestamp, Provider: r.Provider, Model: r.Model, Status: r.Status,
			ContentStored: !r.ContentHidden}
		row.RunID, _ = r.Metadata[schemas.PromptLogRunMetadataKey].(string)
		if r.TokenUsage != nil {
			row.PromptTokens, row.CompletionTokens = r.TokenUsage.PromptTokens, r.TokenUsage.CompletionTokens
		}
		if r.Cost != nil {
			row.CostUSD = *r.Cost
		}
		if r.Latency != nil {
			row.LatencyMS = *r.Latency
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// maxPurgePages bounds one Purge call, so a huge backlog is worked off over
// several sweeps instead of one unbounded request.
const maxPurgePages = 500

// Purge deletes the organization's rows older than before (every row when
// before is zero) and returns how many it deleted.
func (c *Client) Purge(ctx context.Context, orgID string, before time.Time) (int, error) {
	size := c.PageSize
	if size <= 0 {
		size = 200
	}
	deleted := 0
	for i := 0; i < maxPurgePages; i++ {
		rows, _, err := c.list(ctx, orgID, Query{Limit: size, Before: before}, "asc")
		if err != nil {
			return deleted, err
		}
		if len(rows) == 0 {
			return deleted, nil
		}
		ids := make([]string, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		if err := c.do(ctx, http.MethodDelete, "/api/logs", map[string]any{"ids": ids}, nil); err != nil {
			return deleted, err
		}
		deleted += len(ids)
	}
	return deleted, nil
}

// Sweep applies every listed organization's retention: rows older than its
// retention_days are deleted. It returns the rows deleted and the first
// error; one organization's failure does not stop the others.
func Sweep(ctx context.Context, st SettingsStore, c *Client, orgIDs []string, now time.Time) (int, error) {
	if c == nil {
		return 0, nil
	}
	var firstErr error
	total := 0
	for _, org := range orgIDs {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		p, err := LoadPolicy(ctx, st, org)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		n, err := c.Purge(ctx, org, now.Add(-time.Duration(p.RetentionDays)*24*time.Hour))
		total += n
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("org %s: %w", org, err)
		}
	}
	return total, firstErr
}
