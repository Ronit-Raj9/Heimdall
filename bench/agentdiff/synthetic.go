package agentdiff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/maximhq/bifrost/bench/schemas"
)

// adapterScripted is the adapter type of the scripted control agents.
const adapterScripted = "scripted"

// scriptedOutputTokens is the fixed output usage of every scripted-model
// reply (faultproxy/scripted.go).
const scriptedOutputTokens = 32

// scriptedResponseID matches the ids the fault proxy's scripted model mints
// (utils.NewID after "msg_", "resp_" or "chatcmpl-").
var scriptedResponseID = regexp.MustCompile(`^(?:(?:msg|resp)_|chatcmpl-)[0-9a-z]{24}$`)

// syntheticArm decides whether an experiment arm's runs are synthetic, the
// way the leaderboard does (leaderboard/synthetic.go): the experiment is a
// declared smoke run, the config is a scripted control agent, or a sealed
// run's proxy ledger shows the scripted model. The rule is restated here
// because the leaderboard keeps it unexported and this package depends on
// no other feature package.
func syntheticArm(src Source, a *armRecord, cands []candidate) (bool, error) {
	if a.m.Experiment.Smoke || a.arm.Config.Adapter == adapterScripted || a.arm.Config.Scripted != nil {
		return true, nil
	}
	for _, c := range cands {
		if !c.sealed {
			continue
		}
		data, found, err := readRunFile(src, c.run.RunID, fileLedger)
		if err != nil {
			return false, err
		}
		if !found {
			continue
		}
		scripted, err := scriptedLedger(data)
		if err != nil {
			return false, fmt.Errorf("run %s: %w", c.run.RunID, err)
		}
		if scripted {
			return true, nil
		}
	}
	return false, nil
}

// scriptedLedger reports whether a proxy ledger shows the scripted model: by
// the upstream label when any line carries one, else by the scripted
// model's fingerprint (a passed message answered 200 with exactly 32 output
// tokens and a scripted response id).
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
		fingerprint = fingerprint || (e.Kind == "message" && e.Action == "pass" && e.Status == 200 &&
			e.Tokens != nil && e.Tokens.Out == scriptedOutputTokens && scriptedResponseID.MatchString(e.ResponseID))
	}
	if labelled {
		return scripted, nil
	}
	return fingerprint, nil
}
