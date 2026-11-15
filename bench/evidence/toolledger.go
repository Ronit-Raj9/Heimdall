package evidence

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ToolLedger is the name of the tool proxy ledger source.
const ToolLedger = "tool_ledger"

// toolsFile is the run folder file the environment kernel copies the tool
// proxy's ledger to when it exports state.
const toolsFile = "tools.jsonl"

// toolLedgerMax caps how much of the ledger is read. Rows cap their
// arguments and results at 64 KiB each, so this holds thousands of calls.
const toolLedgerMax = 256 << 20

// errNoToolLedger is a run with proxied services whose ledger is not in the
// run folder: the proxy died, or the copy failed.
var errNoToolLedger = errors.New("tool ledger missing from the run folder")

var _ schemas.EvidenceSource = (*toolLedgerSource)(nil)

// toolLedgerSource reads the tool calls the per-run tool proxy recorded. The
// proxy runs in its own container, out of the agent's reach, so the ledger
// is a server-side record of every call to a proxied service.
type toolLedgerSource struct {
	logger schemas.Logger
}

// GetName implements schemas.Part.
func (s *toolLedgerSource) GetName() string { return ToolLedger }

// GetVersion implements schemas.Part.
func (s *toolLedgerSource) GetVersion() string { return version }

// Collect implements schemas.EvidenceSource. It acts only when the task has
// a proxied service. A ledger that is present fills ev.ToolCalls, with an
// empty (not nil) slice when no call was made. A missing ledger is a collect
// failure and leaves ev.ToolCalls nil, so the tool metrics read unknown, not
// zero. Malformed lines are skipped with a warning: a proxy killed mid-write
// leaves a torn last line and the rest is still good evidence.
func (s *toolLedgerSource) Collect(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) error {
	if len(ProxiedServices(rc)) == 0 {
		return nil
	}
	id := runID(rc)
	if rc.Dir == "" {
		return fmt.Errorf("read tool ledger for run %s: no run folder", id)
	}
	f, err := os.Open(filepath.Join(rc.Dir, toolsFile))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read tool ledger for run %s: %w", id, errNoToolLedger)
	}
	if err != nil {
		return fmt.Errorf("read tool ledger for run %s: %w", id, err)
	}
	defer f.Close()
	calls, bad, err := ParseToolLedger(io.LimitReader(f, toolLedgerMax))
	if err != nil {
		return fmt.Errorf("read tool ledger for run %s: %w", id, err)
	}
	if bad > 0 {
		pickLogger(s.logger, rc).Warn("tool ledger has malformed lines", "run_id", id, "lines", bad)
	}
	ev.ToolCalls = calls
	return nil
}

// ParseToolLedger parses tools.jsonl: one schemas.ToolCall per line. It
// returns the rows in file order (never nil) and the number of non-blank
// lines that did not parse or carry no sequence number. Only a read error
// fails.
func ParseToolLedger(r io.Reader) ([]schemas.ToolCall, int, error) {
	calls := []schemas.ToolCall{}
	bad := 0
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var c schemas.ToolCall
		if err := json.Unmarshal(line, &c); err != nil || c.Seq < 1 {
			bad++
			continue
		}
		calls = append(calls, c)
	}
	if err := sc.Err(); err != nil {
		return calls, bad, fmt.Errorf("scan tool ledger: %w", err)
	}
	return calls, bad, nil
}

// ProxiedServices returns the ids of the run's services the tool proxy
// fronts: the suite's then the task's, in file order. A service is proxied
// when it says so, and by default when its role is tool or mcp; a hidden
// service never is, because the agent cannot reach it.
func ProxiedServices(rc *schemas.RunContext) []string {
	if rc == nil {
		return nil
	}
	var specs []schemas.ServiceSpec
	if rc.Suite != nil {
		specs = append(specs, rc.Suite.Services...)
	}
	if rc.Task != nil {
		specs = append(specs, rc.Task.Services...)
	}
	var ids []string
	for _, s := range specs {
		proxied := s.Role == schemas.ServiceRoleTool || s.Role == schemas.ServiceRoleMCP
		if s.Proxy != nil {
			proxied = *s.Proxy
		}
		if proxied && !s.Hidden {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

// newToolLedgerSource returns the tool_ledger source.
func newToolLedgerSource(deps Deps) *toolLedgerSource {
	return &toolLedgerSource{logger: deps.Logger}
}
