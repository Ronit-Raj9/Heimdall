package marble

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// record is one MultiAgentBench task as the benchmark files hold it
// (multiagentbench/<scenario>/<scenario>_main.jsonl, one JSON object per
// line). Fields the importer does not use are kept only in raw, whose hash
// is the task's provenance.
type record struct {
	Scenario       string     `json:"scenario"`
	TaskID         int        `json:"task_id"`
	CoordinateMode string     `json:"coordinate_mode"`
	Relationships  [][]string `json:"relationships"`
	Environment    struct {
		Type          string          `json:"type"`
		Name          string          `json:"name"`
		MaxIterations json.RawMessage `json:"max_iterations"`
		InitSQL       string          `json:"init_sql"`
		Anomalies     []anomaly       `json:"anomalies"`
	} `json:"environment"`
	Task struct {
		Content      string   `json:"content"`
		OutputFormat string   `json:"output_format"`
		Labels       []string `json:"labels"`
		RootCauses   []string `json:"root_causes"`
		LabelsPred   int      `json:"number_of_labels_pred"`
	} `json:"task"`
	Agents []upAgent `json:"agents"`

	line int    // 1-based line in file
	file string // base name of the file it came from
	raw  []byte // the line as read
}

// upAgent is one agent of a task.
type upAgent struct {
	Type    string `json:"type"`
	AgentID string `json:"agent_id"`
	Profile string `json:"profile"`
	Role    string `json:"role"`
}

// anomaly is one database anomaly the upstream environment triggers before
// the agents start (marble/environments/db_env.py).
type anomaly struct {
	Anomaly string `json:"anomaly"`
	Threads int    `json:"threads"`
	NColumn int    `json:"ncolumn"`
	NRow    int    `json:"nrow"`
	ColSize int    `json:"colsize"`
}

// sha256 is the hash of the record line as read, for provenance.
func (r record) sha256() string {
	sum := sha256.Sum256(bytes.TrimSpace(r.raw))
	return hex.EncodeToString(sum[:])
}

// upstreamID is the task's id in MultiAgentBench's own terms.
func (r record) upstreamID() string { return r.Scenario + "/" + strconv.Itoa(r.TaskID) }

// benchID is the bench task id: <scenario>-<task_id>.
func (r record) benchID() string { return r.Scenario + "-" + strconv.Itoa(r.TaskID) }

// scenarioFile is where a clone keeps one scenario's records.
func scenarioFile(root, scenario string) string {
	return filepath.Join(root, "multiagentbench", scenario, scenario+"_main.jsonl")
}

// source is what -from names: a clone of the MARBLE repository (its
// multiagentbench/ folder is read) or a single JSONL file.
type source struct {
	root     string // the clone; empty for a single file
	file     string // the single file; empty for a clone
	revision string
}

// openSource checks from and reads the upstream revision: -set revision
// wins, else the clone's git HEAD. A single file without -set revision has
// an unknown revision, which the notes say.
func openSource(from, rev string) (*source, error) {
	st, err := os.Stat(from)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", from, err)
	}
	s := &source{revision: rev}
	if !st.IsDir() {
		s.file = from
		return s, nil
	}
	if _, err := os.Stat(filepath.Join(from, "multiagentbench")); err != nil {
		return nil, fmt.Errorf("%s has no multiagentbench/ folder: give -from a clone of %s or one of its <scenario>_main.jsonl files", from, upstreamRepo)
	}
	s.root = from
	if s.revision == "" {
		if s.revision, err = gitHead(from); err != nil {
			return nil, fmt.Errorf("read upstream revision (pass -set %s=<commit> when -from is not a git clone): %w", setRevision, err)
		}
	}
	return s, nil
}

// records reads the records of one scenario: from the clone's file for it,
// or from the single file (records of other scenarios are left out). A
// clone without the file returns nil and no error.
func (s *source) records(ctx context.Context, scenario string) ([]record, error) {
	file := s.file
	if file == "" {
		file = scenarioFile(s.root, scenario)
		if _, err := os.Stat(file); errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
	}
	all, err := readRecords(ctx, file)
	if err != nil {
		return nil, err
	}
	var out []record
	for _, r := range all {
		if r.Scenario == scenario {
			out = append(out, r)
		}
	}
	return out, nil
}

// maxLine bounds one record line; the largest upstream record is about
// 60 KB.
const maxLine = 16 << 20

// readRecords reads every non-empty line of a JSONL file as a record. A
// line that does not parse is an error naming the line: the file is not
// the upstream format.
func readRecords(ctx context.Context, file string) ([]record, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", file, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	var out []record
	n := 0
	for sc.Scan() {
		n++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r record
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("parse %s line %d: %w", filepath.Base(file), n, err)
		}
		r.line, r.file, r.raw = n, filepath.Base(file), append([]byte(nil), line...)
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	return out, nil
}

var hexRev = regexp.MustCompile(`^[0-9a-f]{40}$`)

// gitHead reads the commit HEAD points at from root/.git without running
// git: a detached HEAD, a loose ref, or packed-refs for a fresh clone.
func gitHead(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, ".git", "HEAD"))
	if err != nil {
		return "", err
	}
	h := strings.TrimSpace(string(b))
	if hexRev.MatchString(h) {
		return h, nil
	}
	ref, ok := strings.CutPrefix(h, "ref: ")
	if !ok || strings.Contains(ref, "..") {
		return "", fmt.Errorf("unreadable .git/HEAD %q", h)
	}
	if b, err := os.ReadFile(filepath.Join(root, ".git", filepath.FromSlash(ref))); err == nil {
		if r := strings.TrimSpace(string(b)); hexRev.MatchString(r) {
			return r, nil
		}
	}
	packed, err := os.ReadFile(filepath.Join(root, ".git", "packed-refs"))
	if err != nil {
		return "", fmt.Errorf("ref %s not found", ref)
	}
	for _, line := range strings.Split(string(packed), "\n") {
		if sha, name, ok := strings.Cut(strings.TrimSpace(line), " "); ok && name == ref && hexRev.MatchString(sha) {
			return sha, nil
		}
	}
	return "", fmt.Errorf("ref %s not found", ref)
}
