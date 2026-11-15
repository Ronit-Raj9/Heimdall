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
)

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
	seen   int // matching calls counted under the current fault
	logger schemas.Logger
}

// newFaultFile returns a follower of path. An empty path means faults are
// never injected.
func newFaultFile(path string, logger schemas.Logger) *faultFile {
	return &faultFile{path: path, logger: logger}
}

// decide counts one call to tool on server against the current fault and
// returns the fault when this call is one it affects.
func (f *faultFile) decide(server, tool string) (schemas.ToolFault, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reload()
	if f.fault == nil {
		return schemas.ToolFault{}, false
	}
	ft := *f.fault
	if (ft.Server != "" && ft.Server != server) || (ft.Tool != "" && ft.Tool != tool) {
		return schemas.ToolFault{}, false
	}
	f.seen++
	from := max(ft.FromCall, 1)
	if f.seen < from || (ft.Calls > 0 && f.seen >= from+ft.Calls) {
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
	f.raw, f.fault, f.seen = raw, nil, 0
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
	case FaultError, FaultTimeout, FaultPoison:
	default:
		return nil, fmt.Errorf("fault mode %q is not error, timeout or poison", ft.Mode)
	}
	if ft.FromCall < 0 || ft.Calls < 0 || ft.TimeoutS < 0 {
		return nil, errors.New("fault from_call, calls and timeout_s must not be negative")
	}
	if ft.Mode == FaultPoison && ft.Text == "" {
		return nil, errors.New("poison fault has no text")
	}
	return &ft, nil
}

// faultMessage is the error text an error or timeout fault answers with. The
// nonce goes where the message asks for it, else on a line of its own, like a
// request id: a later model request that carries it proves the agent saw the
// fault.
func faultMessage(ft schemas.ToolFault) string {
	msg := ft.Message
	if msg == "" {
		switch ft.Mode {
		case FaultTimeout:
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
