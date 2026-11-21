package environments

import (
	_ "embed"
	"encoding/json"
	"strconv"

	"github.com/maximhq/bifrost/bench/schemas"
)

// approverScript is the approval channel's MCP server, run with the helper
// image's python3.
//
//go:embed approver.py
var approverScript string

// Approval channel settings shared with approver.py and the gates.
const (
	ApproverService = "approver"       // service id; Claude Code names its tool mcp__approver__request_approval
	ApproverExport  = "approvals.json" // export id: state/approver/approvals.json
	approverPort    = 8000
	approverRecord  = "/tmp/heimdall-approver/approvals.json"
)

// wantsApprover reports whether the run gets the approval channel: the task
// declares an approver block, or the agent config asks for one.
func wantsApprover(rc *schemas.RunContext) bool {
	if rc == nil {
		return false
	}
	return (rc.Task != nil && rc.Task.Approver != nil) || (rc.Agent != nil && rc.Agent.Approver)
}

// withApprover adds the approval channel to the run's services: an MCP
// service reached only through the tool proxy, so every request is a
// ledger row, with its decisions exported as state/approver/approvals.json
// for the gates and metrics. A run that does not ask for it, or already
// declares a service named approver, is left as it is.
func (e *dockerWorkspace) withApprover(rc *schemas.RunContext, svcs []runService) []runService {
	if !wantsApprover(rc) {
		return svcs
	}
	for _, s := range svcs {
		if s.spec.ID == ApproverService {
			return svcs
		}
	}
	var policy *schemas.ApproverSpec
	if rc.Task != nil {
		policy = rc.Task.Approver
	}
	return append(svcs, runService{spec: approverSpec(e.helperImage(rc), policy)})
}

// approverSpec is the approval channel's service spec. It is pure. The
// empty gateway block makes it simulator backed: it gets the user
// simulator's settings, and its own gateway key only in llm mode, so a
// scripted run mints no key.
func approverSpec(image string, policy *schemas.ApproverSpec) schemas.ServiceSpec {
	proxy := true
	pol := "{}"
	if policy != nil {
		if b, err := json.Marshal(policy); err == nil {
			pol = string(b)
		}
	}
	return schemas.ServiceSpec{
		ID:    ApproverService,
		Role:  schemas.ServiceRoleMCP,
		Image: image,
		Argv:  []string{"python3", "-u", "-c", approverScript},
		Env: map[string]string{
			"APPROVER_PORT":   strconv.Itoa(approverPort),
			"APPROVER_RECORD": approverRecord,
			"APPROVER_POLICY": pol,
		},
		Port:    approverPort,
		Ready:   &schemas.ReadyCheck{HTTP: "/healthz"},
		Export:  []schemas.StateExport{{ID: ApproverExport, Path: approverRecord}},
		MCP:     &schemas.MCPEndpoint{Path: "/mcp"},
		Proxy:   &proxy,
		Gateway: &schemas.ServiceGateway{},
	}
}
