package engine

import (
	"context"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// runnerPreflight is the gateway preflight of a remote worker for one slot
// (Tenancy area). A manifest planned on a hosted server defers the gateway
// preflight to runners (WithGatewayDeferredToRunner): this worker
// preflights its own gateway before every slot and refuses the lease when
// it has none, the preflight fails, or the hygiene check finds a violation,
// exactly as a local plan would refuse. The slot then runs with a copy of the
// manifest whose gateway is this record, so the gates that read the
// gateway's settings (egress) see the gateway the run used; the stored
// manifest and its hash are unchanged. The record is uploaded with the run.
// A manifest that does not defer runs as before.
func (w *Worker) runnerPreflight(ctx context.Context, m *schemas.Manifest) (*schemas.Manifest, *schemas.GatewayManifest, string) {
	if !m.Gateway.Deferred() {
		return m, nil, ""
	}
	if w.reg == nil || w.reg.Gateway == nil {
		return nil, nil, "the manifest defers the gateway preflight to runners and this runner has no gateway"
	}
	gm, err := w.reg.Gateway.Preflight(ctx)
	if err != nil {
		return nil, nil, "gateway preflight: " + err.Error()
	}
	if gm == nil {
		return nil, nil, "gateway preflight returned no record"
	}
	if declaresSemanticCache(m) { // same opt-in as a local plan (audit 8-B-03)
		acceptSemanticCache(gm.Hygiene)
	}
	if h := gm.Hygiene; h != nil && len(h.Violations) > 0 {
		return nil, nil, "gateway hygiene check failed on this runner: " + strings.Join(h.Violations, "; ")
	}
	rec := *gm
	rec.PartVersion = "gateway/" + w.reg.Gateway.GetName() + "@" + w.reg.Gateway.GetVersion()
	cp := *m
	cp.Gateway = &rec
	return &cp, &rec, ""
}

// withoutGatewayParts drops the gateway part from a part version map. A
// manifest planned with the preflight deferred to runners names no gateway
// part (the hosted server has none); each run's runner record names the
// gateway and version it used.
func withoutGatewayParts(parts map[string]string) map[string]string {
	out := make(map[string]string, len(parts))
	for k, v := range parts {
		if !strings.HasPrefix(k, "gateway/") {
			out[k] = v
		}
	}
	return out
}
