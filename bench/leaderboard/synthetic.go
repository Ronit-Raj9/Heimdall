package leaderboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
)

// adapterScripted is the adapter type of the scripted control agents
// (oracle, noop, exploit, refuse, toy).
const adapterScripted = "scripted"

// scriptedOutputTokens is the fixed output usage of every scripted-model
// reply (faultproxy/scripted.go).
const scriptedOutputTokens = 32

// scriptedResponseID matches the ids the fault proxy's scripted model mints:
// "msg_", "resp_" or "chatcmpl-" and 24 lowercase base-36 characters
// (utils.NewID). Real Anthropic message ids mix upper and lower case and real
// OpenAI response ids carry 50 hex characters, so neither matches.
var scriptedResponseID = regexp.MustCompile(`^(?:(?:msg|resp)_|chatcmpl-)[0-9a-z]{24}$`)

// classify decides, per (experiment, config), whether the runs are
// synthetic: the config uses the scripted adapter, or any sealed run of that
// config in that experiment ran against the scripted model, as its proxy
// ledger shows (scriptedLedger). The pair, not the config, is the unit: a dry
// run of a real agent against the scripted model makes that experiment's runs
// synthetic without hiding the same config's live runs elsewhere. Runs of one
// pair share the proxy's upstream, so a run that made no model call follows
// the others.
func classify(ctx context.Context, src Source, recs []*record) (map[string]bool, error) {
	byKey := map[string][]*record{}
	for _, r := range recs {
		byKey[r.key()] = append(byKey[r.key()], r)
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("leaderboard: %w", err)
		}
		group := byKey[k]
		// A declared smoke experiment stays off the live board even with a real agent.
		if e := group[0].exp; e != nil && e.m != nil && e.m.Experiment.NoPaidTraffic() {
			out[k] = true
			continue
		}
		if scriptedAgent(group[0]) {
			out[k] = true
			continue
		}
		for _, r := range group {
			if !r.sealed {
				continue
			}
			if r.run.Practice || r.run.HumanAssisted { // X01: labelled practice in run.json; Z03: a person took control
				out[k] = true
				break
			}
			// The seal is checked before any file of the run is read: a run
			// whose files changed is excluded later as a seal mismatch and
			// its ledger never decides anything (finding 1-D-06).
			reason, err := sealReason(ctx, src, r)
			if err != nil {
				return nil, err
			}
			if reason != "" {
				continue
			}
			data, found, err := readRunFile(src, r.run.RunID, fileLedger)
			if err != nil {
				r.evidenceProblem = fmt.Sprintf("%s: %s", fileLedger, err)
				continue
			}
			if !found {
				continue
			}
			scripted, err := scriptedLedger(data)
			if err != nil {
				// One run's unreadable ledger excludes that run, not the board.
				r.evidenceProblem = err.Error()
				continue
			}
			if scripted {
				out[k] = true
				break
			}
		}
	}
	return out, nil
}

// scriptedAgent reports whether the record's config is a scripted control
// agent, from the manifest arm or, without one, the run's own agent record.
func scriptedAgent(r *record) bool {
	if r.arm != nil {
		return r.arm.Config.Adapter == adapterScripted || r.arm.Config.Scripted != nil
	}
	return r.run.Agent.Adapter == adapterScripted
}

// scriptedLedger reports whether a run's proxy.ledger.jsonl shows the
// scripted model. A proxy that labels its lines (LedgerEntry.Upstream) is
// taken at its word: once any line carries the label, the run is scripted
// exactly when a line says scripted, and no fingerprint is consulted. Only a
// ledger with no labelled line, written by a proxy older than the field,
// falls back to the scripted model's fingerprint (scriptedEntry). It is pure.
func scriptedLedger(data []byte) (bool, error) {
	labelled, scripted, fingerprint := false, false, false
	for i, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e schemas.LedgerEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return false, fmt.Errorf("parse %s line %d: %w", fileLedger, i+1, err)
		}
		if e.Upstream != "" {
			labelled = true
			scripted = scripted || e.Upstream == schemas.LedgerUpstreamScripted
		}
		fingerprint = fingerprint || scriptedEntry(e)
	}
	if labelled {
		return scripted, nil
	}
	return fingerprint, nil
}

// scriptedEntry reports whether one unlabelled ledger entry carries the
// scripted model's fingerprint: a passed message request answered 200 with
// exactly 32 output tokens and a scripted response id.
func scriptedEntry(e schemas.LedgerEntry) bool {
	return e.Kind == "message" && e.Action == "pass" && e.Status == 200 &&
		e.Tokens != nil && e.Tokens.Out == scriptedOutputTokens && scriptedResponseID.MatchString(e.ResponseID)
}

// Run provenance classes (LedgerProvenance, the pair API's PairRun.Provenance).
const (
	ProvenanceScripted    = "scripted"    // the scripted model answered every recorded model call
	ProvenanceModel       = "model"       // a real model answered at least one recorded call, none scripted
	ProvenanceMixed       = "mixed"       // both the scripted and a real model answered calls
	ProvenanceUnobserved  = "unobserved"  // no recorded model call: no ledger, or one without a passed message
	ProvenanceUnavailable = "unavailable" // the ledger could not be read or parsed
)

// LedgerProvenance classifies one run's proxy.ledger.jsonl by the model
// evidence it records. A labelled proxy is taken at its word per line; an
// unlabelled passed message falls back to the scripted fingerprint
// (scriptedEntry), else counts as a real model call. A ledger with no passed
// message call is unobserved: nothing shows a model played. It is pure.
func LedgerProvenance(data []byte) (string, error) {
	real, scripted := false, false
	for i, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e schemas.LedgerEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return ProvenanceUnavailable, fmt.Errorf("parse %s line %d: %w", fileLedger, i+1, err)
		}
		if e.Kind != "message" || e.Action != "pass" {
			continue
		}
		switch {
		case e.Upstream == schemas.LedgerUpstreamScripted:
			scripted = true
		case e.Upstream == schemas.LedgerUpstreamReal && e.Status >= 200 && e.Status < 300:
			real = true
		case e.Upstream == "" && scriptedEntry(e):
			scripted = true
		case e.Upstream == "" && e.Status >= 200 && e.Status < 300:
			real = true
		}
	}
	switch {
	case real && scripted:
		return ProvenanceMixed, nil
	case scripted:
		return ProvenanceScripted, nil
	case real:
		return ProvenanceModel, nil
	}
	return ProvenanceUnobserved, nil
}
