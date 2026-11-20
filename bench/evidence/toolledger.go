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
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
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

// Collect implements schemas.EvidenceSource. It acts only when the run has
// a tool ledger (ToolLedgerExpected). A ledger that is present fills ev.ToolCalls, with an
// empty (not nil) slice when no call was made. A missing ledger is a collect
// failure and leaves ev.ToolCalls nil, so the tool metrics read unknown, not
// zero. A ledger that misses rows (malformed lines, a Seq gap, a status file
// reporting failed writes or calls in flight, or a ledger over the read cap)
// is a collect failure: a prefix of the calls is not the observed call set,
// so every tool metric and gate reads unknown (1-C-04, 3-D-08).
func (s *toolLedgerSource) Collect(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) error {
	if !ToolLedgerExpected(rc) {
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
	why := ledgerIncomplete(rc.Dir, calls, bad)
	if n, _ := io.Copy(io.Discard, io.LimitReader(f, 1)); n > 0 {
		why = fmt.Sprintf("tool ledger exceeds the %d byte read cap", toolLedgerMax)
	}
	if why != "" {
		return fmt.Errorf("read tool ledger for run %s: %w: %s", id, errToolLedgerIncomplete, why)
	}
	ev.ToolCalls = calls
	return nil
}

// errToolLedgerIncomplete is a ledger that is present but misses rows.
var errToolLedgerIncomplete = errors.New("tool ledger incomplete")

// ledgerIncomplete says why the ledger is not the whole record of the run's
// calls, or "" when it is: unparsable lines, a gap in Seq (the proxy numbers
// a row even when writing it fails), or a status file (written by the proxy,
// copied by the exporter) that reports failed writes, calls still in flight,
// or more rows than the ledger holds. A run folder without a status file
// comes from a proxy that kept none and is judged on the rows alone.
func ledgerIncomplete(dir string, calls []schemas.ToolCall, bad int) string {
	if bad > 0 {
		return fmt.Sprintf("%d malformed lines", bad)
	}
	for i, c := range calls {
		if c.Seq != i+1 {
			return fmt.Sprintf("row %d has seq %d: rows are missing", i+1, c.Seq)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, schemas.ToolLedgerStatusFile))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		return "status: " + err.Error()
	}
	var st schemas.ToolLedgerStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return "status unreadable: " + err.Error()
	}
	if why := st.Incomplete(); why != "" {
		return why
	}
	if st.Rows > len(calls) {
		return fmt.Sprintf("the proxy numbered %d rows, the ledger holds %d", st.Rows, len(calls))
	}
	return ""
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
	if len(utils.TeamOf(rc)) > 0 && !slices.Contains(ids, schemas.MailboxServer) { // Multi-agent area: a team run's mailbox is proxied (docs/multi-agent.md)
		ids = append(ids, schemas.MailboxServer)
	}
	return ids
}

// ToolLedgerExpected reports whether the run's tool proxy keeps a ledger:
// it fronts a proxied service, a team mailbox or an external API. It is the
// same rule the environment uses to export tools.jsonl, so an external-only
// run (no proxied service) still has its calls collected.
func ToolLedgerExpected(rc *schemas.RunContext) bool {
	if rc == nil {
		return false
	}
	return len(ProxiedServices(rc)) > 0 || len(utils.ExternalAPIs(rc.Suite, rc.Task)) > 0 // External APIs area
}

// newToolLedgerSource returns the tool_ledger source.
func newToolLedgerSource(deps Deps) *toolLedgerSource {
	return &toolLedgerSource{logger: deps.Logger}
}
