package bifrost

import (
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Prompt logging per organization (lane X03, audit PROD2 N4). One shared
// Bifrost serves every organization on a shared runner, so each virtual key
// minted for an organization's run carries the organization in its metadata.
// Bifrost snapshots a key's metadata onto every log row the key produces
// (plugins/logging mergeVirtualKeyMetadata), after the caller's own headers,
// so an agent cannot relabel its rows. The server then lists and deletes
// exactly one organization's rows with the metadata filter
// metadata_heimdall_org=<org> (promptlogs package). An organization that
// turned prompt text logging off gets keys with disable_content_logging set:
// Bifrost then stores token counts, cost and timing for its calls, never the
// request or response text.

// OrgLogMetadataKey and RunLogMetadataKey are the virtual key metadata keys
// that tag a gateway log row with its organization and run.
const (
	OrgLogMetadataKey = schemas.PromptLogOrgMetadataKey
	RunLogMetadataKey = schemas.PromptLogRunMetadataKey
)

// orgLogs is the gateway's per-organization logging state.
type orgLogs struct {
	mu     sync.Mutex
	expOrg map[string]string // experiment id -> org
	keep   map[string]bool   // org -> keep prompt text in the gateway log
	runOrg map[string]string // run id -> org, recorded at mint time
}

var orgLogsInitMu sync.Mutex

// maxRunOrgs bounds the run id to organization map of a long-lived runner.
const maxRunOrgs = 20000

func (g *Gateway) orgLogState() *orgLogs {
	orgLogsInitMu.Lock()
	defer orgLogsInitMu.Unlock()
	if g.logSt == nil {
		g.logSt = &orgLogs{expOrg: map[string]string{}, keep: map[string]bool{}, runOrg: map[string]string{}}
	}
	return g.logSt
}

// SetExperimentLogPolicy tags every key minted for the experiment's runs with
// the organization, and records whether the organization keeps prompt text in
// the gateway log. It implements engine.OrgLogPolicyGateway.
func (g *Gateway) SetExperimentLogPolicy(expID, orgID string, p schemas.PromptLogPolicy) {
	if expID == "" || orgID == "" {
		return
	}
	st := g.orgLogState()
	st.mu.Lock()
	defer st.mu.Unlock()
	st.expOrg[expID] = orgID
	st.keep[orgID] = p.KeepContent
}

// noteRunOrg remembers the organization of rc's run, so every key minted
// under that run id (the run key, team members, derived service keys) is
// tagged.
func (g *Gateway) noteRunOrg(rc *schemas.RunContext) {
	if rc == nil || rc.Run == nil || rc.Run.ExpID == "" || rc.Run.RunID == "" {
		return
	}
	st := g.orgLogState()
	st.mu.Lock()
	defer st.mu.Unlock()
	if org, ok := st.expOrg[rc.Run.ExpID]; ok {
		if len(st.runOrg) >= maxRunOrgs { // a long-lived runner: old runs' keys are long gone
			clear(st.runOrg)
		}
		st.runOrg[rc.Run.RunID] = org
	}
}

// addLogFields sets the organization metadata, and the content logging
// decision, on a virtual key create body for the key named name. A key of a
// run with no recorded organization (a local install) is left untouched.
func (g *Gateway) addLogFields(body map[string]any, name string) {
	run, ok := staleRun(name)
	if !ok {
		return
	}
	st := g.orgLogState()
	st.mu.Lock()
	org, found := st.runOrg[run]
	keep := st.keep[org]
	st.mu.Unlock()
	if !found {
		return
	}
	body["metadata"] = map[string]string{OrgLogMetadataKey: org, RunLogMetadataKey: run}
	if !keep {
		off := true
		body["disable_content_logging"] = off
	}
}
