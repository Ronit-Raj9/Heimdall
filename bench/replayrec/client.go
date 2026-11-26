package replayrec

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Limits of one job's reads from the log API (the inbox's hosted rules).
const (
	MaxRequests  = 2000
	MaxReadBytes = 256 << 20
	maxListRows  = 5000
	listPage     = 200
	MaxSessions  = 200
	tokenHeader  = "X-Bifrost-Setup-Token"
)

// Client reads the Bifrost log API.
type Client struct {
	BaseURL string
	Token   string // X-Bifrost-Setup-Token value, from the org secret the source names
	HTTP    *http.Client

	mu       sync.Mutex
	requests int
	read     int64
}

// SessionRef is one session found in the logs.
type SessionRef struct {
	SessionID string    `json:"session_id"`
	Rows      int       `json:"rows"`
	First     time.Time `json:"first"`
	Last      time.Time `json:"last"`
	Failed    bool      `json:"failed"` // any row with status error
	Model     string    `json:"model"`
}

// Query selects sessions.
type Query struct {
	Since, Until time.Time
	VirtualKeys  []string
	Max          int     // sessions; at most 200
	SampleRate   float64 // 0 < r <= 1; a session is kept when its hash bucket is below r
}

type listRow struct {
	ID            string    `json:"id"`
	Timestamp     time.Time `json:"timestamp"`
	Status        string    `json:"status"`
	Model         string    `json:"model"`
	Provider      string    `json:"provider"`
	FallbackIndex int       `json:"fallback_index"`
	SessionID     *string   `json:"session_id"`
}

// Sampled says whether a session id falls in the sample: a stable hash
// bucket of the id below rate.
func Sampled(sessionID string, rate float64) bool {
	if rate >= 1 {
		return true
	}
	if rate <= 0 {
		return false
	}
	sum := sha256.Sum256([]byte(sessionID))
	bucket := float64(binary.BigEndian.Uint64(sum[:8])) / float64(^uint64(0))
	return bucket < rate
}

// Sessions lists sessions in the window (list rows only, at most 5000 rows
// scanned, like the inbox), sampled by a stable hash of the session id.
// The cap Max is applied after sampling; it is at most MaxSessions unless
// a caller in a test asks for more.
func (c *Client) Sessions(ctx context.Context, q Query) ([]SessionRef, error) {
	rate := q.SampleRate
	if rate == 0 {
		rate = 1
	}
	max := q.Max
	if max <= 0 {
		max = MaxSessions
	}
	byID := map[string]*SessionRef{}
	var order []string
	err := c.scan(ctx, q.Since, q.Until, q.VirtualKeys, func(r listRow) {
		id := sessionOf(r)
		if !Sampled(id, rate) {
			return
		}
		s, ok := byID[id]
		if !ok {
			s = &SessionRef{SessionID: id, First: r.Timestamp, Model: r.Model}
			byID[id] = s
			order = append(order, id)
		}
		s.Rows++
		if r.Timestamp.Before(s.First) {
			s.First = r.Timestamp
		}
		if r.Timestamp.After(s.Last) {
			s.Last = r.Timestamp
		}
		if r.Status == "error" {
			s.Failed = true
		}
	})
	if err != nil {
		return nil, err
	}
	out := make([]SessionRef, 0, len(order))
	for _, id := range order {
		if len(out) >= max {
			break
		}
		out = append(out, *byID[id])
	}
	return out, nil
}

// Session fetches the detail rows of one session (at most MaxCalls) and builds the recording.
func (c *Client) Session(ctx context.Context, source, sessionID string, since, until time.Time) (*Recording, error) {
	var ids []string
	err := c.scan(ctx, since, until, nil, func(r listRow) {
		if sessionOf(r) == sessionID && r.FallbackIndex == 0 && len(ids) < MaxCalls {
			ids = append(ids, r.ID)
		}
	})
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("session %s has no log rows in the window", sessionID)
	}
	rows := make([]Row, 0, len(ids))
	for _, id := range ids {
		var row Row
		if err := c.get(ctx, "/api/logs/"+url.PathEscape(id), nil, &row); err != nil {
			return nil, err
		}
		if row.SessionID == nil || *row.SessionID == "" {
			s := sessionID
			row.SessionID = &s
		}
		rows = append(rows, row)
	}
	return Build(source, rows)
}

func sessionOf(r listRow) string {
	if r.SessionID != nil && *r.SessionID != "" {
		return *r.SessionID
	}
	return r.ID
}

func (c *Client) scan(ctx context.Context, since, until time.Time, vks []string, each func(listRow)) error {
	q := url.Values{
		"start_time": {since.UTC().Format(time.RFC3339Nano)}, "end_time": {until.UTC().Format(time.RFC3339Nano)},
		"limit": {strconv.Itoa(listPage)}, "sort_by": {"timestamp"}, "order": {"asc"},
	}
	if len(vks) > 0 {
		q.Set("virtual_key_ids", strings.Join(vks, ","))
	}
	scanned := 0
	for offset := 0; scanned < maxListRows; offset += listPage {
		q.Set("offset", strconv.Itoa(offset))
		var page struct {
			Logs []listRow `json:"logs"`
		}
		if err := c.get(ctx, "/api/logs", q, &page); err != nil {
			return err
		}
		for _, r := range page.Logs {
			if scanned >= maxListRows {
				break
			}
			scanned++
			each(r)
		}
		if len(page.Logs) < listPage {
			break
		}
	}
	return nil
}

var errNoRedirect = errors.New("the log server answered with a redirect, which is not followed")

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	c.mu.Lock()
	if c.requests >= MaxRequests {
		c.mu.Unlock()
		return fmt.Errorf("stopped after %d requests to the log server, the limit of one replay", MaxRequests)
	}
	c.requests++
	c.mu.Unlock()
	u := strings.TrimRight(c.BaseURL, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set(tokenHeader, c.Token)
	}
	hc := http.DefaultClient
	if c.HTTP != nil {
		hc = c.HTTP
	}
	nc := *hc
	nc.CheckRedirect = func(*http.Request, []*http.Request) error { return errNoRedirect }
	resp, err := nc.Do(req)
	if err != nil {
		return fmt.Errorf("log server: %w", err)
	}
	defer resp.Body.Close()
	c.mu.Lock()
	left := int64(MaxReadBytes) - c.read
	c.mu.Unlock()
	if left <= 0 {
		return fmt.Errorf("stopped after reading %d MiB from the log server, the limit of one replay", MaxReadBytes>>20)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, left+1))
	c.mu.Lock()
	c.read += int64(len(body))
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("log server: %w", err)
	}
	if int64(len(body)) > left {
		return fmt.Errorf("stopped after reading %d MiB from the log server, the limit of one replay", MaxReadBytes>>20)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("log server answered %d for %s", resp.StatusCode, path)
	}
	return json.Unmarshal(body, out)
}
