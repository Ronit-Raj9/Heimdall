package attribution

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
)

// maxLine caps one line of a run's JSONL files.
const maxLine = 4 << 20

// LoadEvidence reads what tier 1 needs from a sealed run folder: evidence.json,
// then agents.json, messages.jsonl, steps.jsonl, tools.jsonl and
// events.jsonl for whatever evidence.json does not carry. A missing file
// leaves its field empty; a file that does not parse is an error, because a
// blame built on half the evidence would look complete.
func LoadEvidence(dir string) (*schemas.Evidence, error) {
	var ev schemas.Evidence
	b, err := os.ReadFile(filepath.Join(dir, "evidence.json"))
	if err != nil {
		return nil, fmt.Errorf("read evidence of %s: %w", dir, err)
	}
	if err := json.Unmarshal(b, &ev); err != nil {
		return nil, fmt.Errorf("decode evidence of %s: %w", dir, err)
	}
	if ev.Agents == nil {
		var tree schemas.AgentTree
		found, err := readJSON(filepath.Join(dir, schemas.AgentsFile), &tree)
		if err != nil {
			return nil, err
		}
		if found {
			ev.Agents = &tree
		}
	}
	if ev.Messages == nil {
		if ev.Messages, err = readLines[schemas.AgentMessage](filepath.Join(dir, schemas.MessagesFile)); err != nil {
			return nil, err
		}
	}
	if ev.Steps == nil {
		if ev.Steps, err = readLines[schemas.StepRecord](filepath.Join(dir, schemas.StepsFile)); err != nil {
			return nil, err
		}
	}
	if ev.ToolCalls == nil {
		if ev.ToolCalls, err = readLines[schemas.ToolCall](filepath.Join(dir, "tools.jsonl")); err != nil {
			return nil, err
		}
	}
	if ev.Events == nil {
		if ev.Events, err = readLines[schemas.Event](filepath.Join(dir, "events.jsonl")); err != nil {
			return nil, err
		}
	}
	return &ev, nil
}

func readJSON(p string, dst any) (bool, error) {
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", p, err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return false, fmt.Errorf("decode %s: %w", p, err)
	}
	return true, nil
}

// readLines decodes one T per non-blank line of p; a missing file is no
// records.
func readLines[T any](p string) ([]T, error) {
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", p, err)
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	n := 0
	for sc.Scan() {
		n++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			return nil, fmt.Errorf("decode %s line %d: %w", p, n, err)
		}
		out = append(out, v)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	return out, nil
}
