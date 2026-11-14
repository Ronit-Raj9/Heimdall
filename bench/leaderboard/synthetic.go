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
// config in that experiment has a proxy ledger line answered by the scripted
// model. The proxy ledger does not record which upstream answered, so the
// scripted model is recognized by its fingerprint: a passed message request
// answered 200 with exactly 32 output tokens and a scripted response id. The
// pair, not the config, is the unit: a dry run of a real agent against the
// scripted model makes that experiment's runs synthetic without hiding the
// same config's live runs elsewhere. Runs of one pair share the proxy's
// upstream, so a run that made no model call follows the others.
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
		if scriptedAgent(group[0]) {
			out[k] = true
			continue
		}
		for _, r := range group {
			if !r.sealed {
				continue
			}
			data, found, err := readRunFile(src, r.run.RunID, fileLedger)
			if err != nil {
				return nil, err
			}
			if !found {
				continue
			}
			scripted, err := scriptedLedger(data)
			if err != nil {
				return nil, fmt.Errorf("run %s: %w", r.run.RunID, err)
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

// scriptedLedger reports whether any line of a proxy.ledger.jsonl was
// answered by the scripted model. It is pure.
func scriptedLedger(data []byte) (bool, error) {
	for i, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e schemas.LedgerEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return false, fmt.Errorf("parse %s line %d: %w", fileLedger, i+1, err)
		}
		if scriptedEntry(e) {
			return true, nil
		}
	}
	return false, nil
}

// scriptedEntry reports whether one ledger entry carries the scripted
// model's fingerprint.
func scriptedEntry(e schemas.LedgerEntry) bool {
	return e.Kind == "message" && e.Action == "pass" && e.Status == 200 &&
		e.Tokens != nil && e.Tokens.Out == scriptedOutputTokens && scriptedResponseID.MatchString(e.ResponseID)
}
