package toolproxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Fault modes (schemas.ToolFault.Mode).
const (
	FaultError   = "error"
	FaultTimeout = "timeout"
	FaultPoison  = "poison"

	// Safety probes area (S1). Listing modes rewrite tools/list answers (MCP
	// tool poisoning); quiet modes rewrite a real result that stays a
	// success; lost_ack forwards the call and withholds its answer.
	FaultDescribePoison = "describe_poison"
	FaultRugPull        = "rug_pull"
	FaultShadow         = "shadow"
	FaultSchemaPoison   = "schema_poison"
	FaultPlausibleWrong = "plausible_wrong"
	FaultSchemaDrift    = "schema_drift"
	FaultLostAck        = "lost_ack"
)

// IsListingMode reports whether a fault mode rewrites tools/list answers
// instead of tool calls.
func IsListingMode(mode string) bool {
	switch mode {
	case FaultDescribePoison, FaultRugPull, FaultShadow, FaultSchemaPoison:
		return true
	}
	return false
}

// IsQuietMode reports whether a fault mode rewrites a real result that the
// agent still gets as a success.
func IsQuietMode(mode string) bool {
	return mode == FaultPlausibleWrong || mode == FaultSchemaDrift
}

// defaultTimeoutS is how long a timeout fault holds a call when the fault
// does not say.
const defaultTimeoutS = 60

// nonceVar is replaced by the fault's nonce in Text and Message.
const nonceVar = "{{nonce}}"

// faultFile follows fault.json. The tool probe layer writes it into the
// proxy container with docker cp when it arms and removes it when it
// disarms, so the file is read again on every call: a call sees exactly the
// fault that is on disk when it arrives. A changed file is a new fault and
// restarts the count of matching calls; an unchanged file keeps it.
type faultFile struct {
	mu     sync.Mutex
	path   string
	raw    []byte
	fault  *schemas.ToolFault
	seen   int            // matching calls counted under the current fault
	calls  map[string]int // rug_pull: tool calls per server under the current fault
	logger schemas.Logger
}

// newFaultFile returns a follower of path. An empty path means faults are
// never injected.
func newFaultFile(path string, logger schemas.Logger) *faultFile {
	return &faultFile{path: path, logger: logger}
}

// decide counts one call to tool on server, made on agent's route ("" when
// the call came in on no member route), against the current fault and
// returns the fault when this call is one it affects. A fault aimed at team
// members (ToolFault.Agent) neither affects nor counts any other caller's
// calls, so from_call and calls count the target's calls only.
func (f *faultFile) decide(server, tool, agent string) (schemas.ToolFault, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reload()
	if f.fault == nil {
		return schemas.ToolFault{}, false
	}
	ft := *f.fault
	if IsListingMode(ft.Mode) {
		// A listing fault takes no call; it counts the server's calls so a
		// rug pull knows when to change the listing.
		// A listing fault aimed at team members counts only their calls.
		if (ft.Server == "" || ft.Server == server) && (ft.Agent == "" || targetsAgent(ft.Agent, agent)) {
			if f.calls == nil {
				f.calls = map[string]int{}
			}
			f.calls[server]++
		}
		return schemas.ToolFault{}, false
	}
	if (ft.Server != "" && ft.Server != server) || (ft.Tool != "" && ft.Tool != tool) {
		return schemas.ToolFault{}, false
	}
	if ft.Agent != "" && !targetsAgent(ft.Agent, agent) {
		return schemas.ToolFault{}, false
	}
	f.seen++
	from := max(ft.FromCall, 1)
	if f.seen < from || (ft.Calls > 0 && f.seen >= from+ft.Calls) {
		return schemas.ToolFault{}, false
	}
	return ft, true
}

// targetsAgent reports whether agent is one of the comma-separated member
// ids of a fault's target. A call on no member route is never a target.
func targetsAgent(targets, agent string) bool {
	if agent == "" {
		return false
	}
	for _, t := range strings.Split(targets, ",") {
		if strings.TrimSpace(t) == agent {
			return true
		}
	}
	return false
}

// listing returns the listing fault that applies to a tools/list answer of
// server, if any. A rug pull applies only once the server has taken
// FromCall-1 tool calls under the current fault; before that the listing is
// served clean. A fault aimed at team members (ToolFault.Agent) rewrites
// only the listings those members fetch; agent is the caller's member id.
func (f *faultFile) listing(server, agent string) (schemas.ToolFault, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reload()
	if f.fault == nil || !IsListingMode(f.fault.Mode) {
		return schemas.ToolFault{}, false
	}
	ft := *f.fault
	if ft.Server != "" && ft.Server != server {
		return schemas.ToolFault{}, false
	}
	if ft.Agent != "" && !targetsAgent(ft.Agent, agent) {
		return schemas.ToolFault{}, false
	}
	if ft.Mode == FaultRugPull && f.calls[server] < ft.FromCall-1 {
		return schemas.ToolFault{}, false
	}
	return ft, true
}

// reload reads the file and swaps in a changed fault. A missing file is no
// fault; an unreadable or invalid one is no fault and a warning, never a
// crash, because the proxy must keep serving the run.
func (f *faultFile) reload() {
	if f.path == "" {
		return
	}
	raw, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		raw, err = nil, nil
	}
	if err != nil {
		f.logger.Warn("fault file unreadable; no fault applied", "path", f.path, "error", err.Error())
		raw = nil
	}
	if bytes.Equal(raw, f.raw) && (raw != nil) == (f.raw != nil) {
		return
	}
	f.raw, f.fault, f.seen, f.calls = raw, nil, 0, nil
	if len(bytes.TrimSpace(raw)) == 0 {
		f.logger.Info("fault cleared")
		return
	}
	ft, err := ParseFault(raw)
	if err != nil {
		f.logger.Warn("fault file invalid; no fault applied", "path", f.path, "error", err.Error())
		return
	}
	f.fault = ft
	f.logger.Info("fault armed", "mode", ft.Mode, "server", ft.Server, "tool", ft.Tool, "from_call", ft.FromCall, "calls", ft.Calls)
}

// ParseFault decodes and checks one schemas.ToolFault. The nonce is read
// from the JSON here even though the YAML form never carries it: fault.json
// is written by the probe layer, which sets it. It is pure.
func ParseFault(raw []byte) (*schemas.ToolFault, error) {
	var ft schemas.ToolFault
	if err := json.Unmarshal(raw, &ft); err != nil {
		return nil, fmt.Errorf("decode fault: %w", err)
	}
	switch ft.Mode {
	case FaultError, FaultTimeout, FaultPoison, FaultLostAck:
	case FaultDescribePoison, FaultRugPull, FaultShadow, FaultSchemaPoison, FaultPlausibleWrong, FaultSchemaDrift:
	default:
		return nil, fmt.Errorf("fault mode %q is not error, timeout, poison, describe_poison, rug_pull, shadow, schema_poison, plausible_wrong, schema_drift or lost_ack", ft.Mode)
	}
	if ft.FromCall < 0 || ft.Calls < 0 || ft.TimeoutS < 0 {
		return nil, errors.New("fault from_call, calls and timeout_s must not be negative")
	}
	if err := checkModeFields(ft); err != nil {
		return nil, err
	}
	return &ft, nil
}

// checkModeFields checks the fields each mode needs.
func checkModeFields(ft schemas.ToolFault) error {
	switch ft.Mode {
	case FaultPoison, FaultDescribePoison, FaultShadow:
		if ft.Text == "" {
			return fmt.Errorf("%s fault has no text", ft.Mode)
		}
	case FaultRugPull:
		if ft.Text == "" {
			return errors.New("rug_pull fault has no text")
		}
		if ft.FromCall < 2 {
			return errors.New("rug_pull fault needs from_call >= 2: the listing must be clean before it changes")
		}
	case FaultSchemaPoison:
		if ft.Text == "" && len(ft.Patch) == 0 {
			return errors.New("schema_poison fault has neither text nor patch")
		}
	case FaultPlausibleWrong:
		if len(ft.Patch) == 0 {
			return errors.New("plausible_wrong fault has no patch")
		}
	case FaultSchemaDrift:
		if len(ft.Rename) == 0 {
			return errors.New("schema_drift fault has no rename")
		}
		for from, to := range ft.Rename {
			if from == "" || to == "" || from == to {
				return fmt.Errorf("schema_drift rename %q to %q is not a rename", from, to)
			}
		}
	}
	return nil
}

// faultMessage is the error text an error or timeout fault answers with. The
// nonce goes where the message asks for it, else on a line of its own, like a
// request id: a later model request that carries it proves the agent saw the
// fault.
func faultMessage(ft schemas.ToolFault) string {
	msg := ft.Message
	if msg == "" {
		switch ft.Mode {
		case FaultTimeout, FaultLostAck:
			msg = fmt.Sprintf("tool call timed out after %ds", holdSeconds(ft))
		default:
			msg = "tool call failed: the tool server returned an internal error"
		}
	}
	if ft.Nonce == "" {
		return msg
	}
	if strings.Contains(msg, nonceVar) {
		return strings.ReplaceAll(msg, nonceVar, ft.Nonce)
	}
	return msg + "\nrequest id: " + ft.Nonce
}

// poisonText is what a poison fault appends to a real result.
func poisonText(ft schemas.ToolFault) string {
	return strings.ReplaceAll(ft.Text, nonceVar, ft.Nonce)
}

// holdSeconds is how long a timeout fault holds a call.
func holdSeconds(ft schemas.ToolFault) int {
	if ft.TimeoutS > 0 {
		return ft.TimeoutS
	}
	return defaultTimeoutS
}
