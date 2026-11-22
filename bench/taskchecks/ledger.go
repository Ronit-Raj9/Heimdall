package taskchecks

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
)

// maxLedgerLine is the longest tools.jsonl line read; the proxy caps
// arguments and results well below it.
const maxLedgerLine = 16 << 20

// ReadLedger reads the tool proxy's ledger (tools.jsonl). A missing file is
// a run without tool calls: nil and no error.
func ReadLedger(path string) ([]schemas.ToolCall, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxLedgerLine)
	var out []schemas.ToolCall
	line := 0
	for sc.Scan() {
		line++
		b := sc.Bytes()
		if len(b) == 0 {
			continue
		}
		var row schemas.ToolCall
		if err := json.Unmarshal(b, &row); err != nil {
			return nil, fmt.Errorf("tools.jsonl line %d: %v", line, err)
		}
		out = append(out, row)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("tools.jsonl: %v", err)
	}
	return out, nil
}

// TaskFile is the path of a task's checks.yaml (in its verifier folder),
// "" when it has none.
func TaskFile(t *schemas.Task) string {
	if t == nil || t.Dir == "" {
		return ""
	}
	tests := t.Verifier.Tests
	if tests == "" {
		tests = "verifier"
	}
	p := filepath.Join(t.Dir, filepath.FromSlash(tests), FileName)
	if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
		return p
	}
	return ""
}
