package bifrost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

const (
	logPageSize = 200
	// emptyLedgerSettle is how long a run with no ledger entries is polled
	// before zero rows count as complete. Bifrost writes rows in batches
	// (logs_store.writer.batch_interval, 5 s by default, 500 ms in the bench
	// config), and an early poll sees zero rows and zero "processing" rows.
	emptyLedgerSettle = 6 * time.Second
	// unknownWindow bounds the list query for a key this process did not mint.
	// A bounded window under 24 h makes Bifrost count raw rows instead of
	// reading its lagging hourly view.
	unknownWindow = 23 * time.Hour
	mintSkew      = time.Minute
)

// FetchRequests implements schemas.Gateway. It polls the key's rows every
// PollEvery until every ledger entry of kind message and count_tokens is
// covered by exactly one row (joinLedger), every row that reached the proxy
// is covered, no row is processing, and two polls in a row return the same
// rows; or until Timeout. A direct provider has no proxy and no ledger: its
// set is complete when no row is processing and two polls match, after the
// settle time that covers Bifrost's batched log writer. Each detail row is fetched once (rows still
// processing are fetched again). An incomplete set is returned with
// Complete=false and a Reason, never dropped. Only a cancelled context or a
// key without an id is an error.
func (g *Gateway) FetchRequests(ctx context.Context, key *schemas.RunKey, expect schemas.FetchExpectation) (*schemas.RequestLog, error) {
	if key == nil || key.ID == "" {
		return nil, errors.New("fetch requests: key has no virtual key id")
	}
	timeout, every := expect.Timeout, expect.PollEvery
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if every <= 0 {
		every = DefaultPollEvery
	}
	since := g.windowStart(key.ID)
	start := time.Now()
	cache := map[string]*parsedRow{}
	prev, lastErr := "\x00none", "" // no poll yet: the first signature never counts as repeated
	var last *schemas.RequestLog
	direct := g.keyMode(key) == ModeDirect
	expected := 0
	if !direct {
		expected = len(joinLedger(nil, expect.LedgerEntries, key.KeySlot).Entries)
	}
	for {
		snap, err := g.snapshot(ctx, key, since, cache, expect.LedgerEntries, direct)
		switch {
		case err != nil && ctx.Err() != nil:
			return last, fmt.Errorf("fetch requests for key %s: %w", key.ID, ctx.Err())
		case err != nil:
			lastErr = err.Error()
			g.logger.Warn("bifrost log poll failed", "virtual_key_id", key.ID, "error", lastErr)
		default:
			lastErr, last = "", snap.log
			settled := expected > 0 || time.Since(start) >= g.settle
			if snap.sig == prev && settled && snap.final() {
				return g.finish(ctx, snap.log), nil
			}
			prev = snap.sig
		}
		if time.Since(start)+every > timeout {
			return g.finish(ctx, timedOut(last, timeout, lastErr)), nil
		}
		select {
		case <-ctx.Done():
			return last, fmt.Errorf("fetch requests for key %s: %w", key.ID, ctx.Err())
		case <-time.After(every):
		}
	}
}

// listRow is the part of a list row needed to decide whether to fetch it.
type listRow struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// snapshot is one poll: the log as it stands and what the stop rule needs.
type snapshot struct {
	log        *schemas.RequestLog
	sig        string // sorted row ids with status; equal signatures mean nothing changed
	processing int
	join       joinResult
}

// final reports whether more polls cannot change the outcome: the set is
// complete, or every ledger entry is covered and nothing is processing but
// some row does not join. The ledger is fixed, so waiting would not help.
func (s snapshot) final() bool {
	return s.processing == 0 && len(s.join.Unmatched) == 0
}

// snapshot lists every row of the key, fetches detail rows not yet cached
// (and again while processing or when the status changed), and joins them to
// the ledger.
func (g *Gateway) snapshot(ctx context.Context, key *schemas.RunKey, since time.Time, cache map[string]*parsedRow, ledger []schemas.LedgerEntry, direct bool) (*snapshot, error) {
	rows, err := g.listRows(ctx, key.ID, since)
	if err != nil {
		return nil, err
	}
	parsed := make([]*parsedRow, 0, len(rows))
	ids := make([]string, 0, len(rows))
	sig := make([]string, 0, len(rows))
	processing := 0
	for _, lr := range rows {
		p, ok := cache[lr.ID]
		if !ok || p.Req.Status == statusProcessing || lr.Status != p.Req.Status {
			if p, err = g.fetchDetail(ctx, lr.ID); err != nil {
				return nil, err
			}
			cache[lr.ID] = p
		}
		if p.Req.Status == statusProcessing {
			processing++
		}
		parsed = append(parsed, p)
		ids = append(ids, p.Req.ID)
		sig = append(sig, p.Req.ID+"="+p.Req.Status)
	}
	sort.Strings(sig)
	var join joinResult
	if !direct {
		join = joinLedger(parsed, ledger, key.KeySlot)
	}
	log := &schemas.RequestLog{Requests: make([]schemas.GatewayRequest, 0, len(parsed)), RowIDs: ids}
	for _, p := range parsed {
		log.Requests = append(log.Requests, p.Req)
	}
	log.Complete = join.complete() && processing == 0
	switch {
	case processing > 0:
		log.Reason = fmt.Sprintf("%d rows still processing", processing)
	case !join.complete():
		log.Reason = join.reason()
	}
	return &snapshot{log: log, sig: strings.Join(sig, ","), processing: processing, join: join}, nil
}

// listRows pages GET /api/logs for the key, oldest first, until a short page.
func (g *Gateway) listRows(ctx context.Context, vkID string, since time.Time) ([]listRow, error) {
	var all []listRow
	for offset := 0; ; offset += g.pageSize {
		q := url.Values{
			"virtual_key_ids": {vkID},
			"start_time":      {since.UTC().Format(time.RFC3339)},
			"limit":           {strconv.Itoa(g.pageSize)},
			"offset":          {strconv.Itoa(offset)},
			"sort_by":         {"timestamp"},
			"order":           {"asc"},
		}
		var page struct {
			Logs []listRow `json:"logs"`
		}
		if err := g.call(ctx, http.MethodGet, "/api/logs?"+q.Encode(), nil, &page); err != nil {
			return nil, fmt.Errorf("list rows for key %s: %w", vkID, err)
		}
		all = append(all, page.Logs...)
		if len(page.Logs) < g.pageSize {
			return all, nil
		}
	}
}

func (g *Gateway) fetchDetail(ctx context.Context, id string) (*parsedRow, error) {
	raw, err := g.callRaw(ctx, http.MethodGet, "/api/logs/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, fmt.Errorf("fetch detail row %s: %w", id, err)
	}
	return parseDetail(raw)
}

// DroppedLogs reads Bifrost's global count of log rows it dropped since the
// process started (GET /api/logs/dropped, dropped_requests). The count covers
// every key, so it never decides one run's completeness; the verdict reads its
// rise over the whole experiment. dropped_during_maintenance is a subset of
// dropped_requests and is not added.
func (g *Gateway) DroppedLogs(ctx context.Context) (int64, error) {
	var out struct {
		DroppedRequests *int64 `json:"dropped_requests"`
	}
	if err := g.call(ctx, http.MethodGet, "/api/logs/dropped", nil, &out); err != nil {
		return 0, fmt.Errorf("read dropped log counter: %w", err)
	}
	if out.DroppedRequests == nil {
		return 0, errors.New("read dropped log counter: answer has no dropped_requests")
	}
	return *out.DroppedRequests, nil
}

// finish attaches Bifrost's global drop counter to the run's log at collect
// time. A counter that cannot be read stays nil, which the verdict reads as
// unknown, never as 0.
func (g *Gateway) finish(ctx context.Context, log *schemas.RequestLog) *schemas.RequestLog {
	n, err := g.DroppedLogs(ctx)
	if err != nil {
		g.logger.Warn("bifrost dropped counter unavailable", "error", err.Error())
		return log
	}
	log.Dropped = &n
	if n > 0 {
		g.logger.Warn("bifrost reports dropped log rows", "dropped_requests", n)
	}
	return log
}

// keyMode is the mode of the key's provider: by its slot, else by its
// provider name. Unknown keys are treated as proxied, the stricter check.
func (g *Gateway) keyMode(key *schemas.RunKey) string {
	if p, ok := g.slotProvider(key.KeySlot); ok {
		return p.Mode
	}
	if p, ok := g.provider(key.Provider); ok {
		return p.Mode
	}
	return ModeProxied
}

// windowStart is the list query's start_time: shortly before the key was
// minted, or a bounded window for a key from an earlier process.
func (g *Gateway) windowStart(vkID string) time.Time {
	g.mu.Lock()
	rec, ok := g.minted[vkID]
	g.mu.Unlock()
	if ok {
		return rec.at.Add(-mintSkew)
	}
	return time.Now().Add(-unknownWindow)
}

// timedOut marks the last snapshot incomplete after the timeout.
func timedOut(last *schemas.RequestLog, timeout time.Duration, lastErr string) *schemas.RequestLog {
	if last == nil {
		last = &schemas.RequestLog{Requests: []schemas.GatewayRequest{}, RowIDs: []string{}}
	}
	if last.Complete {
		last.Complete = false
		last.Reason = "row set still changing"
	}
	reason := fmt.Sprintf("timeout after %s", timeout)
	if last.Reason != "" {
		reason += ": " + last.Reason
	}
	if lastErr != "" {
		reason += "; last poll failed: " + lastErr
	}
	last.Reason = reason
	return last
}
