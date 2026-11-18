package traceinbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/traceimport"
)

// The Langfuse source reads the public API as its OpenAPI reference states
// it (https://cloud.langfuse.com/generated/api/openapi.yml, read 18 Nov
// 2026). The trace read endpoints are gone from that reference (only DELETE
// /api/public/traces remains), so a trace is assembled from its observations:
//
//   - GET /api/public/v3/scores: name, dataType, valueMax, fromTimestamp,
//     toTimestamp, environment, fields=subject, limit (max 100), cursor;
//     answer {data: [{name, value, dataType, timestamp, subject: {kind,
//     id, traceId}}], meta: {cursor}}. Score data may lag about 10 minutes.
//   - GET /api/public/v2/observations: level, traceId, fromStartTime,
//     toStartTime, environment, isRootObservation, filter (JSON conditions,
//     e.g. traceTags any of), fields (core, basic, io, model, ...), limit
//     (max 1000), cursor; answer {data, meta: {cursor}}. A page may be empty
//     while a cursor is present. input and output arrive as raw strings.
//   - Basic auth: public key as user, secret key as password.
//
// No real Langfuse has been called; the tests use a local fake of these
// endpoints.

const (
	langfuseScorePage = 100
	langfuseObsPage   = 1000
	langfuseMaxPages  = 50 // pages of one flag query per pull
	langfuseObsPages  = 5  // pages of one trace's observations
)

type langfuseFetcher struct {
	c      *apiClient
	getenv func(string) string
}

// candidate is one trace a pull will fetch, with why.
type candidate struct {
	id      string
	at      time.Time
	reasons []string
	kinds   []string
	extra   map[string]string
}

// candidates keeps flagged traces in first-flagged order, merging reasons.
// A trace an earlier pull already took is skipped, so the per-pull cap
// counts only new traces and a capped pull makes progress next time.
type candidates struct {
	order []string
	by    map[string]*candidate
	limit int
	full  bool
	seen  map[string]time.Time // the cursor's taken traces, by redacted id
}

func newCandidates(limit int, cur Cursor) *candidates {
	return &candidates{by: map[string]*candidate{}, limit: limit, seen: cur.Seen}
}

// add records a flag; it returns false once the cap is reached.
func (cs *candidates) add(id string, at time.Time, reason, kind string) bool {
	if id == "" {
		return true
	}
	if _, taken := cs.seen[redactedID(id)]; taken {
		return true
	}
	c, ok := cs.by[id]
	if !ok {
		if len(cs.order) >= cs.limit {
			cs.full = true
			return false
		}
		c = &candidate{id: id, at: at}
		cs.by[id] = c
		cs.order = append(cs.order, id)
	}
	if at.After(c.at) {
		c.at = at
	}
	if reason != "" && len(c.reasons) < MaxFlagged && !contains(c.reasons, reason) {
		c.reasons = append(c.reasons, reason)
	}
	if kind != "" && !contains(c.kinds, kind) {
		c.kinds = append(c.kinds, kind)
	}
	return true
}

func (cs *candidates) list() []*candidate {
	out := make([]*candidate, 0, len(cs.order))
	for _, id := range cs.order {
		out = append(out, cs.by[id])
	}
	return out
}

// Fetch implements Fetcher.
func (f *langfuseFetcher) Fetch(ctx context.Context, src *Source, cur Cursor, now time.Time) (*Batch, error) {
	pub, sec := f.getenv(src.PublicKeyEnv), f.getenv(src.SecretKeyEnv)
	if pub == "" || sec == "" {
		return nil, fmt.Errorf("langfuse source %s: set %s and %s", src.Name, src.PublicKeyEnv, src.SecretKeyEnv)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://x", nil)
	req.SetBasicAuth(pub, sec)
	hdr := http.Header{"Authorization": req.Header.Values("Authorization")}
	host := strings.TrimRight(src.Host, "/")
	from := windowStart(src, cur, now)
	cs := newCandidates(src.maxPerPull(), cur)
	b := &Batch{Cursor: cur}
	b.Cursor.Since = now

	for _, sf := range src.Filters.Scores {
		if err := f.scores(ctx, host, hdr, src, sf.Name, sf.Below, sf.Values, FlagScore, from, now, cs, b); err != nil {
			return nil, err
		}
	}
	if fb := src.Filters.Feedback; fb != nil {
		if err := f.scores(ctx, host, hdr, src, fb.Name, fb.Below, fb.Negative, FlagFeedback, from, now, cs, b); err != nil {
			return nil, err
		}
	}
	if src.Filters.LevelError {
		q := url.Values{"level": {"ERROR"}}
		if err := f.observations(ctx, host, hdr, src, q, from, now, cs, FlagLevel, b); err != nil {
			return nil, err
		}
	}
	if len(src.Filters.Tags) > 0 {
		cond, _ := json.Marshal([]map[string]any{{"type": "arrayOptions", "column": "traceTags", "operator": "any of", "value": src.Filters.Tags}})
		q := url.Values{"isRootObservation": {"true"}, "filter": {string(cond)}}
		if err := f.observations(ctx, host, hdr, src, q, from, now, cs, FlagTag, b); err != nil {
			return nil, err
		}
	}
	if b.Truncated = cs.full; b.Truncated {
		b.holdAt(from.Add(src.overlap())) // read this window again; taken traces are skipped
	}
	for _, c := range cs.list() {
		s, err := f.trace(ctx, host, hdr, c.id)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			b.Warnings = append(b.Warnings, fmt.Sprintf("trace %s: %v", c.id, err))
			b.holdAt(c.at) // tried again by the next pull
			continue
		}
		b.Sessions = append(b.Sessions, Flagged{Session: s, TraceID: c.id, At: c.at, Reasons: c.reasons, Kinds: c.kinds})
	}
	return b, nil
}

// scores pages the v3 scores of one name and flags the traces whose score is
// below the threshold or one of the values.
func (f *langfuseFetcher) scores(ctx context.Context, host string, hdr http.Header, src *Source, name string, below *float64, values []string, kind string, from, to time.Time, cs *candidates, b *Batch) error {
	q := url.Values{
		"name": {name}, "fields": {"subject"}, "limit": {strconv.Itoa(langfuseScorePage)},
		"fromTimestamp": {from.UTC().Format(time.RFC3339Nano)}, "toTimestamp": {to.UTC().Format(time.RFC3339Nano)},
	}
	if below != nil && len(values) == 0 {
		q.Set("dataType", "NUMERIC")
		q.Set("valueMax", strconv.FormatFloat(*below, 'f', -1, 64))
	}
	if src.Environment != "" {
		q.Set("environment", src.Environment)
	}
	sessionScores := 0
	for page := 0; page < langfuseMaxPages; page++ {
		var ans struct {
			Data []struct {
				Name      string          `json:"name"`
				Value     json.RawMessage `json:"value"`
				DataType  string          `json:"dataType"`
				Timestamp string          `json:"timestamp"`
				Subject   struct {
					Kind    string `json:"kind"`
					ID      string `json:"id"`
					TraceID string `json:"traceId"`
				} `json:"subject"`
			} `json:"data"`
			Meta struct {
				Cursor string `json:"cursor"`
			} `json:"meta"`
		}
		if err := f.c.doJSON(ctx, http.MethodGet, host+"/api/public/v3/scores?"+q.Encode(), hdr, nil, &ans); err != nil {
			return fmt.Errorf("langfuse scores %s: %w", name, err)
		}
		for _, sc := range ans.Data {
			val := scoreText(sc.Value)
			if !scoreMatches(val, below, values) {
				continue
			}
			trace := ""
			switch sc.Subject.Kind {
			case "trace", "":
				trace = sc.Subject.ID
			case "observation":
				trace = sc.Subject.TraceID
			default:
				sessionScores++
				continue
			}
			reason := fmt.Sprintf("score %s = %s", oneLine(sc.Name, 60), oneLine(val, 40))
			if below != nil {
				reason += fmt.Sprintf(" (below %s)", strconv.FormatFloat(*below, 'f', -1, 64))
			}
			if kind == FlagFeedback {
				reason = "negative user feedback: " + reason
			}
			if !cs.add(trace, parseRFC3339(sc.Timestamp), reason, kind+":"+sc.Name) {
				return nil
			}
		}
		if ans.Meta.Cursor == "" {
			break
		}
		q.Set("cursor", ans.Meta.Cursor)
		if page == langfuseMaxPages-1 {
			cs.full = true
		}
	}
	if sessionScores > 0 {
		b.Warnings = append(b.Warnings, fmt.Sprintf("%d %s scores are on sessions or experiments, not traces; they are not pulled", sessionScores, name))
	}
	return nil
}

// observations pages v2 observations with the given filter and flags their
// traces.
func (f *langfuseFetcher) observations(ctx context.Context, host string, hdr http.Header, src *Source, q url.Values, from, to time.Time, cs *candidates, kind string, b *Batch) error {
	q.Set("fields", "core,basic")
	q.Set("limit", strconv.Itoa(langfuseObsPage))
	q.Set("fromStartTime", from.UTC().Format(time.RFC3339Nano))
	q.Set("toStartTime", to.UTC().Format(time.RFC3339Nano))
	if src.Environment != "" {
		q.Set("environment", src.Environment)
	}
	for page := 0; page < langfuseMaxPages; page++ {
		var ans struct {
			Data []map[string]any `json:"data"`
			Meta struct {
				Cursor string `json:"cursor"`
			} `json:"meta"`
		}
		if err := f.c.doJSON(ctx, http.MethodGet, host+"/api/public/v2/observations?"+q.Encode(), hdr, nil, &ans); err != nil {
			return fmt.Errorf("langfuse observations: %w", err)
		}
		for _, o := range ans.Data {
			trace, _ := o["traceId"].(string)
			name, _ := o["name"].(string)
			reason := "trace tagged " + strings.Join(src.Filters.Tags, " or ")
			if kind == FlagLevel {
				msg, _ := o["statusMessage"].(string)
				reason = fmt.Sprintf("observation %q level ERROR", oneLine(name, 60))
				if msg != "" {
					reason += ": " + oneLine(msg, 160)
				}
			}
			at, _ := o["startTime"].(string)
			if !cs.add(trace, parseRFC3339(at), reason, kind) {
				return nil
			}
		}
		if ans.Meta.Cursor == "" {
			return nil
		}
		q.Set("cursor", ans.Meta.Cursor)
	}
	cs.full = true
	return nil
}

// trace reads one trace's observations with their input and output and
// builds the session through traceimport's Langfuse reader.
func (f *langfuseFetcher) trace(ctx context.Context, host string, hdr http.Header, traceID string) (*traceimport.Session, error) {
	q := url.Values{"traceId": {traceID}, "fields": {"core,basic,io,model"}, "limit": {strconv.Itoa(langfuseObsPage)}}
	var rows []any
	for page := 0; page < langfuseObsPages; page++ {
		var ans struct {
			Data []map[string]any `json:"data"`
			Meta struct {
				Cursor string `json:"cursor"`
			} `json:"meta"`
		}
		if err := f.c.doJSON(ctx, http.MethodGet, host+"/api/public/v2/observations?"+q.Encode(), hdr, nil, &ans); err != nil {
			return nil, err
		}
		for _, o := range ans.Data {
			for _, k := range []string{"input", "output"} {
				o[k] = decodeJSONString(o[k])
			}
			rows = append(rows, o)
		}
		if ans.Meta.Cursor == "" {
			break
		}
		q.Set("cursor", ans.Meta.Cursor)
	}
	if len(rows) == 0 {
		return nil, errors.New("no observations (the trace may still be ingesting; it is tried again on the next pull)")
	}
	data, err := json.Marshal(map[string]any{"data": rows})
	if err != nil {
		return nil, err
	}
	tr, err := traceimport.Parse(data, traceimport.FormatLangfuse)
	if err != nil {
		return nil, err
	}
	return tr.Pick(traceID)
}

// decodeJSONString turns a raw string that holds a JSON object or array into
// that value; other values pass.
func decodeJSONString(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "{") && !strings.HasPrefix(t, "[") && !strings.HasPrefix(t, `"`) {
		return v
	}
	var out any
	dec := json.NewDecoder(strings.NewReader(t))
	dec.UseNumber()
	if dec.Decode(&out) != nil {
		return v
	}
	return out
}

// scoreText renders a score value (number, boolean or string) as text.
func scoreText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

// scoreMatches reports whether a score value is flagged: below the
// threshold (strictly, the API bound is inclusive), or one of the values.
func scoreMatches(val string, below *float64, values []string) bool {
	for _, v := range values {
		if strings.EqualFold(strings.TrimSpace(v), strings.TrimSpace(val)) {
			return true
		}
	}
	if below != nil {
		if n, err := strconv.ParseFloat(val, 64); err == nil {
			return n < *below
		}
	}
	return false
}

func parseRFC3339(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
