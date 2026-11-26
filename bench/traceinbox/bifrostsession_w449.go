package traceinbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/traceimport"
)

// ErrNoBifrostSession says the gateway logs hold no row of the session.
var ErrNoBifrostSession = errors.New("no gateway log row holds this session")

// FeedbackSourceName is the inbox source user feedback is recorded under.
const FeedbackSourceName = "feedback"

// FirstBifrost is the first inbox source of kind bifrost, if any.
func (c *Config) FirstBifrost() (*Source, bool) {
	if c == nil {
		return nil, false
	}
	for i := range c.Sources {
		if c.Sources[i].Kind == KindBifrost {
			return &c.Sources[i], true
		}
	}
	return nil, false
}

// BifrostSession asks a Bifrost source's log API for one session: it scans
// the list rows from since to now (at most bifrostMaxRows), takes the newest
// row whose session_id is sessionID, and builds the session from that row's
// detail, the way a pull does.
func (ib *Inbox) BifrostSession(ctx context.Context, src *Source, sessionID string, since time.Time) (*traceimport.Session, error) {
	if src == nil || src.Kind != KindBifrost {
		return nil, errors.New("not a bifrost source")
	}
	if src.URL == "" {
		return nil, fmt.Errorf("bifrost source %s: no gateway url", src.Name)
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("no session id")
	}
	hdr := http.Header{}
	if src.TokenEnv != "" {
		tok := ib.getenv(src.TokenEnv)
		if tok == "" {
			return nil, fmt.Errorf("bifrost source %s: set %s", src.Name, src.TokenEnv)
		}
		hdr.Set(bifrostTokenHdr, tok)
	}
	c := newAPIClient(ib.HTTP, ib.Sleep)
	base := strings.TrimRight(src.URL, "/")
	q := url.Values{
		"start_time": {since.UTC().Format(time.RFC3339Nano)}, "end_time": {ib.now().Format(time.RFC3339Nano)},
		"limit": {strconv.Itoa(bifrostPage)}, "sort_by": {"timestamp"}, "order": {"asc"},
	}
	var newest *bifrostRow
	scanned := 0
	for offset := 0; scanned < bifrostMaxRows; offset += bifrostPage {
		q.Set("offset", strconv.Itoa(offset))
		var page struct {
			Logs []bifrostRow `json:"logs"`
		}
		if err := c.doJSON(ctx, http.MethodGet, base+"/api/logs?"+q.Encode(), hdr, nil, &page); err != nil {
			return nil, fmt.Errorf("bifrost logs: %w", err)
		}
		for i := range page.Logs {
			scanned++
			r := page.Logs[i]
			if r.SessionID != nil && *r.SessionID == sessionID && (newest == nil || !r.Timestamp.Before(newest.Timestamp)) {
				newest = &r
			}
		}
		if len(page.Logs) < bifrostPage {
			break
		}
	}
	if newest == nil {
		return nil, ErrNoBifrostSession
	}
	f := &bifrostFetcher{c: c, getenv: ib.getenv}
	detail, err := f.detail(ctx, base, hdr, newest.ID)
	if err != nil {
		return nil, err
	}
	return bifrostSession(sessionID, detail)
}

// IngestSession takes one already parsed session into the inbox under src,
// the same path a push takes (redaction, signature, dedupe). passed keeps it
// as a golden candidate.
func (ib *Inbox) IngestSession(src *Source, s *traceimport.Session, traceID string, reasons []string, passed bool) (*IngestResult, error) {
	if src == nil {
		return nil, errors.New("no source")
	}
	fl := Flagged{Session: s, TraceID: firstNonEmpty(traceID, s.ID), At: ib.now(), Kinds: []string{FlagFeedback}, Reasons: reasons}
	if passed {
		fl.Golden, fl.Kinds = true, []string{FlagGolden}
	}
	out, err := ib.take(src, fl)
	if err != nil {
		return nil, err
	}
	res := &IngestResult{}
	if !out.Skipped {
		res.ItemID, res.Created = out.ItemID, out.Created
	}
	_ = ib.Store.Log(LogEvent{At: ib.now(), Event: "ingest", Item: res.ItemID, Source: src.Name})
	return res, nil
}
