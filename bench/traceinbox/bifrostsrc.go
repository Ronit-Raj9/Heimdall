package traceinbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/traceimport"
)

// The Bifrost source reads the gateway's log API as this repository's
// gateway serves it (transports/bifrost-http/handlers/logging.go and
// framework/logstore/tables.go, read 18 Nov 2026):
//
//   - GET /api/logs: status (comma list: processing, success, error),
//     virtual_key_ids, start_time, end_time (RFC 3339), limit (at most 1000),
//     offset, sort_by=timestamp, order=asc; answer {logs: [...]} with id,
//     timestamp, status, provider, model, number_of_retries, fallback_index,
//     session_id, virtual_key_id per row.
//   - GET /api/logs/{id}: one row with input_history (chat messages),
//     output_message, error_details {type, status_code, error: {message}}.
//   - A governance budget refusal carries error type budget_exceeded and
//     status 402 (plugins/governance).
//   - Management auth: the bench's setup token header, as the bench gateway
//     client sends it (gateways/bifrost).
//
// It reads chat-completion rows; rows of the Responses API keep their
// input in another shape and come through with the request text only when
// it is in chat form. No live gateway has been called by the tests; they
// use fixture rows behind a local fake.

const (
	bifrostPage      = 200
	bifrostMaxRows   = 5000 // list rows scanned per pull
	bifrostTokenHdr  = "X-Bifrost-Setup-Token"
	budgetErrorType  = "budget_exceeded"
	budgetStatusCode = 402
)

type bifrostFetcher struct {
	c      *apiClient
	getenv func(string) string
}

// bifrostRow is a list or detail row, the fields the source reads.
type bifrostRow struct {
	ID             string            `json:"id"`
	Timestamp      time.Time         `json:"timestamp"`
	Status         string            `json:"status"`
	Provider       string            `json:"provider"`
	Model          string            `json:"model"`
	Retries        int               `json:"number_of_retries"`
	FallbackIndex  int               `json:"fallback_index"`
	SessionID      *string           `json:"session_id"`
	VirtualKeyName *string           `json:"virtual_key_name"`
	InputHistory   []json.RawMessage `json:"input_history"`
	OutputMessage  json.RawMessage   `json:"output_message"`
	ContentSummary string            `json:"content_summary"`
	ErrorDetails   *bifrostError     `json:"error_details"`
}

type bifrostError struct {
	Type       *string `json:"type"`
	StatusCode *int    `json:"status_code"`
	Error      *struct {
		Message string  `json:"message"`
		Type    *string `json:"type"`
	} `json:"error"`
}

func (e *bifrostError) typ() string {
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

func (e *bifrostError) status() int {
	if e == nil || e.StatusCode == nil {
		return 0
	}
	return *e.StatusCode
}

func (e *bifrostError) message() string {
	if e == nil || e.Error == nil {
		return ""
	}
	return e.Error.Message
}

// Fetch implements Fetcher.
func (f *bifrostFetcher) Fetch(ctx context.Context, src *Source, cur Cursor, now time.Time) (*Batch, error) {
	if src.URL == "" {
		return nil, fmt.Errorf("bifrost source %s: no gateway url (set url, or gateway.url in the bench config)", src.Name)
	}
	hdr := http.Header{}
	if src.TokenEnv != "" {
		tok := f.getenv(src.TokenEnv)
		if tok == "" {
			return nil, fmt.Errorf("bifrost source %s: set %s", src.Name, src.TokenEnv)
		}
		hdr.Set(bifrostTokenHdr, tok)
	}
	base := strings.TrimRight(src.URL, "/")
	from := windowStart(src, cur, now)
	fl := src.Filters
	onlyErrors := (fl.Errors || fl.Budget) && fl.MinRetries <= 0 && !fl.Fallbacks
	q := url.Values{
		"start_time": {from.UTC().Format(time.RFC3339Nano)}, "end_time": {now.UTC().Format(time.RFC3339Nano)},
		"limit": {strconv.Itoa(bifrostPage)}, "sort_by": {"timestamp"}, "order": {"asc"},
	}
	if onlyErrors {
		q.Set("status", "error")
	}
	if len(src.VirtualKeys) > 0 {
		q.Set("virtual_key_ids", strings.Join(src.VirtualKeys, ","))
	}
	b := &Batch{Cursor: cur}
	b.Cursor.Since = now
	type group struct {
		key     string
		rows    []bifrostRow
		reasons []string
		kinds   []string
		at      time.Time
	}
	groups := map[string]*group{}
	var order []string
	scanned := 0
	for offset := 0; ; offset += bifrostPage {
		q.Set("offset", strconv.Itoa(offset))
		var page struct {
			Logs []bifrostRow `json:"logs"`
		}
		if err := f.c.doJSON(ctx, http.MethodGet, base+"/api/logs?"+q.Encode(), hdr, nil, &page); err != nil {
			return nil, fmt.Errorf("bifrost logs: %w", err)
		}
		for _, r := range page.Logs {
			scanned++
			reasons, kinds := bifrostFlags(r, fl, false)
			if len(kinds) == 0 && !(fl.Budget && r.Status == "error") {
				continue
			}
			key, trace := r.ID, r.ID
			if r.SessionID != nil && *r.SessionID != "" {
				key, trace = "session:"+*r.SessionID, *r.SessionID
			}
			if _, taken := cur.Seen[redactedID(trace)]; taken {
				continue
			}
			g, ok := groups[key]
			if !ok {
				if len(order) >= src.maxPerPull() {
					if !b.Truncated {
						b.holdAt(r.Timestamp.Add(src.overlap())) // the next pull starts at the first row not taken
					}
					b.Truncated = true
					continue
				}
				g = &group{key: key}
				groups[key] = g
				order = append(order, key)
			}
			g.rows = append(g.rows, r)
			for _, rs := range reasons {
				if len(g.reasons) < MaxFlagged && !slices.Contains(g.reasons, rs) {
					g.reasons = append(g.reasons, rs)
				}
			}
			for _, k := range kinds {
				if !slices.Contains(g.kinds, k) {
					g.kinds = append(g.kinds, k)
				}
			}
			if r.Timestamp.After(g.at) {
				g.at = r.Timestamp
			}
		}
		if len(page.Logs) < bifrostPage {
			break
		}
		if scanned >= bifrostMaxRows {
			if last := page.Logs[len(page.Logs)-1]; !b.Truncated {
				b.holdAt(last.Timestamp.Add(src.overlap()))
			}
			b.Truncated = true
			b.Warnings = append(b.Warnings, fmt.Sprintf("scanned %d log rows, the per-pull cap; pull again for the rest", scanned))
			break
		}
	}
	for _, key := range order {
		g := groups[key]
		last := g.rows[len(g.rows)-1]
		detail, err := f.detail(ctx, base, hdr, last.ID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			b.Warnings = append(b.Warnings, fmt.Sprintf("row %s: %v", last.ID, err))
			continue
		}
		// The detail row carries the error's status, message and type: its
		// reasons replace the list row's bare "failed" for that row, and a
		// budget refusal is only recognized here.
		reasons, kinds := bifrostFlags(*detail, fl, true)
		bare := fmt.Sprintf("request %s to %s/%s failed", last.ID, last.Provider, last.Model)
		g.reasons = slices.DeleteFunc(g.reasons, func(r string) bool { return r == bare })
		for i, k := range kinds {
			if !slices.Contains(g.kinds, k) {
				g.kinds = append(g.kinds, k)
			}
			if !slices.Contains(g.reasons, reasons[i]) {
				g.reasons = append([]string{reasons[i]}, g.reasons...)
			}
		}
		if len(g.reasons) > MaxFlagged {
			g.reasons = g.reasons[:MaxFlagged]
		}
		if !fl.Errors && fl.MinRetries <= 0 && !fl.Fallbacks && !slices.Contains(g.kinds, FlagBudget) {
			continue // only budget hits were asked for, and this error was not one
		}
		if len(g.kinds) == 0 {
			continue
		}
		traceID := last.ID
		if strings.HasPrefix(key, "session:") {
			traceID = strings.TrimPrefix(key, "session:")
		}
		s, err := bifrostSession(traceID, detail)
		if err != nil {
			b.Warnings = append(b.Warnings, fmt.Sprintf("row %s: %v", last.ID, err))
			continue
		}
		b.Sessions = append(b.Sessions, Flagged{Session: s, TraceID: traceID, At: g.at, Reasons: g.reasons, Kinds: g.kinds,
			Extra: map[string]string{"row_id": last.ID}})
	}
	return b, nil
}

// bifrostFlags says why one row is flagged. withDetail reads the error
// type, which only the detail row is sure to carry.
func bifrostFlags(r bifrostRow, fl Filters, withDetail bool) ([]string, []string) {
	var reasons, kinds []string
	if fl.Errors && r.Status == "error" {
		msg := oneLine(r.ErrorDetails.message(), 160)
		reason := fmt.Sprintf("request %s to %s/%s failed", r.ID, r.Provider, r.Model)
		if st := r.ErrorDetails.status(); st != 0 {
			reason += fmt.Sprintf(" (status %d)", st)
		}
		if msg != "" {
			reason += ": " + msg
		}
		reasons, kinds = append(reasons, reason), append(kinds, FlagError)
	}
	if fl.MinRetries > 0 && r.Retries >= fl.MinRetries {
		reasons = append(reasons, fmt.Sprintf("request %s retried %d times", r.ID, r.Retries))
		kinds = append(kinds, FlagRetries)
	}
	if fl.Fallbacks && r.FallbackIndex > 0 {
		reasons = append(reasons, fmt.Sprintf("request %s served by fallback %d (%s/%s)", r.ID, r.FallbackIndex, r.Provider, r.Model))
		kinds = append(kinds, FlagFallback)
	}
	if fl.Budget && withDetail && r.Status == "error" &&
		(r.ErrorDetails.typ() == budgetErrorType || r.ErrorDetails.status() == budgetStatusCode) {
		reasons = append(reasons, "budget exceeded: "+oneLine(r.ErrorDetails.message(), 160))
		kinds = append(kinds, FlagBudget)
	}
	return reasons, kinds
}

func (f *bifrostFetcher) detail(ctx context.Context, base string, hdr http.Header, id string) (*bifrostRow, error) {
	var row bifrostRow
	if err := f.c.doJSON(ctx, http.MethodGet, base+"/api/logs/"+url.PathEscape(id), hdr, nil, &row); err != nil {
		return nil, err
	}
	return &row, nil
}

// bifrostSession builds a session from the newest flagged row of a session:
// its input history and output message hold the conversation so far.
func bifrostSession(traceID string, r *bifrostRow) (*traceimport.Session, error) {
	msgs := make([]json.RawMessage, 0, len(r.InputHistory)+1)
	msgs = append(msgs, r.InputHistory...)
	if len(r.OutputMessage) > 0 && string(r.OutputMessage) != "null" {
		msgs = append(msgs, r.OutputMessage)
	}
	if len(msgs) == 0 && r.ContentSummary != "" {
		b, _ := json.Marshal(map[string]string{"role": "user", "content": r.ContentSummary})
		msgs = append(msgs, b)
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("the row holds no messages (content logging may be off for it)")
	}
	conv := map[string]any{"id": traceID, "model": r.Provider + "/" + r.Model, "messages": msgs}
	if r.Status == "error" {
		conv["status"] = "error"
		conv["error"] = firstNonEmpty(r.ErrorDetails.message(), "request failed")
	}
	data, err := json.Marshal(conv)
	if err != nil {
		return nil, err
	}
	tr, err := traceimport.Parse(data, traceimport.FormatChat)
	if err != nil {
		return nil, err
	}
	return tr.Sessions[0], nil
}
