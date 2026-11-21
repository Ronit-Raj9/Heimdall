package livescore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/traceimport"
)

// The source reads Bifrost's log API as v2.5.1 serves it. Verified from the
// v2.5.1 source (transports/bifrost-http/handlers/logging.go, getLogs and
// parseMetadataFilters; framework/logstore/tables.go, Log; plugins/logging
// main.go, captureLoggingHeaders; core/schemas/useragents.go,
// HarnessSessionHeaders), read 18 Nov 2026, and against a throwaway v2.5.1
// stack the same day (docs/live-scoring.md, "Verified Bifrost behaviour"):
//
//   - GET /api/logs filters on virtual_key_ids, team_ids, customer_ids,
//     models, status, session_id, start_time and end_time (RFC 3339), and
//     metadata_<key>=<value> for any metadata key; limit, offset,
//     sort_by=timestamp, order=asc.
//   - A request header x-bf-lh-<name> is logged as metadata key <name> with
//     no gateway setting; client.logging_headers adds other headers under
//     their own names.
//   - session_id comes from x-bf-session-id, else a harness session header
//     (x-claude-code-session-id, x-session-affinity, x-session-id,
//     session-id, ...).
//   - List rows carry the numbers (latency in milliseconds, cost,
//     token_usage, number_of_retries, fallback_index), session_id, metadata
//     and input_history; GET /api/logs/{id} adds output_message,
//     error_details and stop_reason. content_hidden is true when content
//     logging was off for the row, and then no transcript is served.
//
// Every request is a GET, enforced by readOnlyTransport.

const (
	statusError      = "error"
	statusProcessing = "processing"
	budgetErrorType  = "budget_exceeded"
	tokenHeader      = "X-Bifrost-Setup-Token"
	pageSize         = 500
	maxBodyBytes     = 64 << 20
)

// ErrWriteRefused is what readOnlyTransport answers for any method but GET
// and HEAD. Live scoring never writes to the gateway.
var ErrWriteRefused = errors.New("live scoring is read-only: refused a request that is not a GET")

// readOnlyTransport refuses every request that is not GET or HEAD before it
// leaves the process, so no code path of this package can create, change or
// delete a key, rule or setting on the gateway.
type readOnlyTransport struct{ next http.RoundTripper }

// RoundTrip implements http.RoundTripper.
func (t readOnlyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, fmt.Errorf("%w (%s %s)", ErrWriteRefused, r.Method, r.URL.Path)
	}
	next := t.next
	if next == nil {
		next = http.DefaultTransport
	}
	return next.RoundTrip(r)
}

// ReadOnlyClient wraps a client so it can only read. A nil base gets a
// client with a 30 second timeout.
func ReadOnlyClient(base *http.Client) *http.Client {
	c := &http.Client{Timeout: 30 * time.Second}
	if base != nil {
		cp := *base
		c = &cp
	}
	c.Transport = readOnlyTransport{next: c.Transport}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

// Row is a Bifrost log row: the list fields, and the transcript fields of a
// detail row. Only numbers, flags and ids survive measuring; the transcript
// is dropped with the row.
type Row struct {
	ID            string            `json:"id"`
	Timestamp     time.Time         `json:"timestamp"`
	Status        string            `json:"status"`
	Object        string            `json:"object"`
	Provider      string            `json:"provider"`
	Model         string            `json:"model"`
	Retries       int               `json:"number_of_retries"`
	FallbackIndex int               `json:"fallback_index"`
	Latency       *float64          `json:"latency"`
	Cost          *float64          `json:"cost"`
	StopReason    *string           `json:"stop_reason"`
	SessionID     *string           `json:"session_id"`
	VirtualKeyID  *string           `json:"virtual_key_id"`
	TeamID        *string           `json:"team_id"`
	CustomerID    *string           `json:"customer_id"`
	UserID        *string           `json:"user_id"`
	Metadata      map[string]any    `json:"metadata"`
	TokenUsage    *rowTokens        `json:"token_usage"`
	ErrorDetails  *rowError         `json:"error_details"`
	InputHistory  []json.RawMessage `json:"input_history"`
	OutputMessage json.RawMessage   `json:"output_message"`
	// A Responses API row keeps its conversation here instead
	// (traceimport.ResponsesToChat).
	ResponsesInputHistory []json.RawMessage `json:"responses_input_history"`
	ResponsesOutput       []json.RawMessage `json:"responses_output"`
	ContentHidden         bool              `json:"content_hidden"`
	ContentSummary        string            `json:"content_summary"`
	detail                bool
}

type rowTokens struct {
	PromptTokens     *int64 `json:"prompt_tokens"`
	CompletionTokens *int64 `json:"completion_tokens"`
	TotalTokens      *int64 `json:"total_tokens"`
}

type rowError struct {
	Type       *string `json:"type"`
	StatusCode *int    `json:"status_code"`
	Error      *struct {
		Message string  `json:"message"`
		Type    *string `json:"type"`
	} `json:"error"`
}

func (e *rowError) typ() string {
	if e == nil {
		return ""
	}
	if e.Type != nil && *e.Type != "" {
		return *e.Type
	}
	if e.Error != nil && e.Error.Type != nil {
		return *e.Error.Type
	}
	return ""
}

func (e *rowError) status() int {
	if e == nil || e.StatusCode == nil {
		return 0
	}
	return *e.StatusCode
}

func (e *rowError) message() string {
	if e == nil || e.Error == nil {
		return ""
	}
	return e.Error.Message
}

// tokens is prompt plus completion tokens, or -1 when unknown.
func (r *Row) tokens() int64 {
	u := r.TokenUsage
	if u == nil {
		return -1
	}
	if u.TotalTokens != nil && *u.TotalTokens > 0 {
		return *u.TotalTokens
	}
	if u.PromptTokens == nil && u.CompletionTokens == nil {
		return -1
	}
	var n int64
	if u.PromptTokens != nil {
		n += *u.PromptTokens
	}
	if u.CompletionTokens != nil {
		n += *u.CompletionTokens
	}
	return n
}

// refusalField reports an OpenAI-style refusal on the row's output message.
func (r *Row) refusalField() bool {
	if len(r.OutputMessage) == 0 {
		return false
	}
	var m struct {
		Refusal *string `json:"refusal"`
	}
	if json.Unmarshal(r.OutputMessage, &m) != nil || m.Refusal == nil {
		return false
	}
	return strings.TrimSpace(*m.Refusal) != ""
}

// metadataValue is a metadata key's value as text.
func (r *Row) metadataValue(key string) (string, bool) {
	if r.Metadata == nil {
		return "", false
	}
	for k, v := range r.Metadata {
		if strings.EqualFold(k, key) {
			switch x := v.(type) {
			case string:
				return x, true
			default:
				b, _ := json.Marshal(x)
				return string(b), true
			}
		}
	}
	return "", false
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// client reads one gateway.
type client struct {
	base  string
	token string
	http  *http.Client
}

func newClient(base, token string, hc *http.Client) *client {
	return &client{base: strings.TrimRight(base, "/"), token: token, http: ReadOnlyClient(hc)}
}

// get fetches one JSON document.
func (c *client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set(tokenHeader, c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("GET %s: %w", path, ue.Err)
		}
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("GET %s: read: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 200 {
			msg = msg[:200] + "..."
		}
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("GET %s: status %d: the gateway refused the read credential (source.token_env)", path, resp.StatusCode)
		}
		return fmt.Errorf("GET %s: status %d: %s", path, resp.StatusCode, msg)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("GET %s: decode: %w", path, err)
	}
	return nil
}

// query is the server-side filter of a version: Bifrost narrows the rows,
// and matches checks every row again, so a gateway that ignored a filter
// cannot leak another version's rows into this one.
func (v *Version) query(from, to time.Time) url.Values {
	q := url.Values{
		"start_time": {from.UTC().Format(time.RFC3339Nano)},
		"end_time":   {to.UTC().Format(time.RFC3339Nano)},
		"sort_by":    {"timestamp"}, "order": {"asc"}, "limit": {strconv.Itoa(pageSize)},
	}
	if len(v.VirtualKeys) > 0 {
		q.Set("virtual_key_ids", strings.Join(v.VirtualKeys, ","))
	}
	if len(v.Teams) > 0 {
		q.Set("team_ids", strings.Join(v.Teams, ","))
	}
	if len(v.Customers) > 0 {
		q.Set("customer_ids", strings.Join(v.Customers, ","))
	}
	if len(v.Models) > 0 {
		var ms []string
		for _, m := range v.Models {
			if _, after, ok := strings.Cut(m, "/"); ok {
				m = after
			}
			if !slices.Contains(ms, m) {
				ms = append(ms, m)
			}
		}
		q.Set("models", strings.Join(ms, ","))
	}
	if t := v.Tag; t != nil {
		q.Set("metadata_"+t.MetadataKey(), t.Value)
	}
	return q
}

// matches checks a row against the selector on the client side. A row
// without the metadata field cannot be checked for a tag: the list row of a
// gateway that does not serve metadata is trusted to the server filter and
// counted (unchecked).
func (v *Version) matches(r *Row) (ok, unchecked bool) {
	if len(v.VirtualKeys) > 0 && !slices.Contains(v.VirtualKeys, deref(r.VirtualKeyID)) {
		return false, false
	}
	if len(v.Teams) > 0 && !slices.Contains(v.Teams, deref(r.TeamID)) {
		return false, false
	}
	if len(v.Customers) > 0 && !slices.Contains(v.Customers, deref(r.CustomerID)) {
		return false, false
	}
	if len(v.Models) > 0 {
		hit := false
		for _, m := range v.Models {
			if m == r.Model || m == r.Provider+"/"+r.Model {
				hit = true
				break
			}
		}
		if !hit {
			return false, false
		}
	}
	if t := v.Tag; t != nil {
		if r.Metadata == nil {
			return true, true
		}
		val, has := r.metadataValue(t.MetadataKey())
		if !has || val != t.Value {
			return false, false
		}
	}
	return true, false
}

// listRows pages GET /api/logs for one version over [from, to], at most
// MaxRowsPerPull rows. truncated is set when the cap stopped it. Rows the
// version's selector rejects, and rows that skip reports (already scored or skipped by sampling) are dropped while
// paging and do not count toward the cap: a backlog denser than the cap
// would otherwise refill the capped page with the same old rows on every
// pull and never reach the rest.
func (c *client) listRows(ctx context.Context, v *Version, from, to time.Time, skip func(id string) bool) (rows []Row, truncated bool, err error) {
	q := v.query(from, to)
	for offset := 0; ; offset += pageSize {
		q.Set("offset", strconv.Itoa(offset))
		var page struct {
			Logs []Row `json:"logs"`
		}
		if err := c.get(ctx, "/api/logs?"+q.Encode(), &page); err != nil {
			return rows, false, err
		}
		for _, r := range page.Logs {
			if skip != nil && skip(r.ID) {
				continue
			}
			// Rows the version's selector rejects (another provider under
			// the same model name) are never scored either: counting them
			// toward the cap would refill the capped page with them forever.
			if ok, _ := v.matches(&r); !ok {
				continue
			}
			rows = append(rows, r)
		}
		if len(page.Logs) < pageSize {
			return rows, false, nil
		}
		if len(rows) >= MaxRowsPerPull {
			return rows, true, nil
		}
	}
}

// detail fetches one detail row.
func (c *client) detail(ctx context.Context, id string) (*Row, error) {
	var r Row
	if err := c.get(ctx, "/api/logs/"+url.PathEscape(id), &r); err != nil {
		return nil, err
	}
	r.detail = true
	return &r, nil
}

// transcriptOf builds the session's conversation from its last row that
// holds one: its input history and output message carry the conversation
// so far, as for the failure inbox's Bifrost source. It returns nil when no
// row holds messages.
func transcriptOf(id string, rows []Row) *traceimport.Session {
	for i := len(rows) - 1; i >= 0; i-- {
		r := &rows[i]
		if r.ContentHidden {
			continue
		}
		msgs := make([]json.RawMessage, 0, len(r.InputHistory)+1)
		msgs = append(msgs, r.InputHistory...)
		if len(r.OutputMessage) > 0 && string(r.OutputMessage) != "null" {
			msgs = append(msgs, r.OutputMessage)
		}
		if len(msgs) == 0 {
			msgs = traceimport.ResponsesToChat(r.ResponsesInputHistory, r.ResponsesOutput)
		}
		if len(msgs) == 0 {
			continue
		}
		conv := map[string]any{"id": id, "model": r.Provider + "/" + r.Model, "messages": msgs}
		if r.Status == statusError {
			conv["status"] = "error"
			conv["error"] = firstNonEmpty(r.ErrorDetails.message(), "request failed")
		}
		data, err := json.Marshal(conv)
		if err != nil {
			return nil
		}
		tr, err := traceimport.Parse(data, traceimport.FormatChat)
		if err != nil || len(tr.Sessions) == 0 {
			return nil
		}
		return tr.Sessions[0]
	}
	return nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
