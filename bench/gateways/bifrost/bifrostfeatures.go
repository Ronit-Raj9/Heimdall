package bifrost

import (
	"encoding/json"

	"github.com/maximhq/bifrost/bench/schemas"
)

// B2 Bifrost features (docs/bifrost-features.md): the per-row fields that let
// an experiment measure Bifrost itself. Bifrost v2.5.1 splits every detail
// row's latency into overhead_latency (time inside the gateway: plugins,
// governance, routing, conversion, queueing) and upstream_latency (waiting
// for the provider, here the fault proxy), both in milliseconds. A row of a
// routed request may carry the complexity tier the classifier gave it, and a
// cache hit carries its hit type in cache_debug.

// featureFields copies the overhead split, the complexity tier and the
// semantic cache fields of one detail row into the bench's row. raw is the
// whole detail row; cache_debug is read from it so that only the hit type and
// the cache entry id are kept. On v2.5.1 a miss row of a request with a cache
// key carries the id of the entry it stored, and a hit row the id of the
// entry it was served from, so a hit can be traced to the run that stored
// it (checked on the hd-b2 stack, 18 Nov 2026).
func featureFields(r *schemas.GatewayRequest, d detailRow, raw []byte) {
	if d.OverheadLatency != nil && *d.OverheadLatency >= 0 {
		v := *d.OverheadLatency
		r.OverheadMS = &v
	}
	if d.UpstreamLatency != nil && *d.UpstreamLatency >= 0 {
		v := *d.UpstreamLatency
		r.UpstreamMS = &v
	}
	if d.ComplexityTier != nil {
		r.ComplexityTier = *d.ComplexityTier
	}
	var c struct {
		CacheDebug *struct {
			HitType string `json:"hit_type"`
			CacheID string `json:"cache_id"`
		} `json:"cache_debug"`
	}
	if json.Unmarshal(raw, &c) == nil && c.CacheDebug != nil {
		r.CacheHitType, r.CacheID = c.CacheDebug.HitType, c.CacheDebug.CacheID
	}
}
