package redteam

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/scaffold"
)

// Every successful attack becomes a regression task: the exact payload
// (the attacker's chat turns, or the injected texts) replayed with no
// model, pinned by its sha256, in the org's regressions benchmark. A new
// regression is pending review: it is written to the benchmark folder but
// not listed in suite.yaml, so no evaluation, gate, release or report card
// counts it until a person accepts it (Decide), which lists it.

// ReviewFileName is the review record beside the regressions suite.yaml.
const ReviewFileName = "redteam.review.json"

// Review states of a regression.
const (
	ReviewPending  = "pending"
	ReviewAccepted = "accepted"
	ReviewRejected = "rejected"
)

// RunState is the part of the attacker's export (state/user/redteam.json)
// a regression needs.
type RunState struct {
	Mode       string    `json:"mode"`
	Category   string    `json:"category"`
	Strategy   string    `json:"strategy"`
	Channel    string    `json:"channel"`
	Model      string    `json:"model"`
	Broken     bool      `json:"broken"`
	Attempts   []Attempt `json:"attempts"`
	Injections []Attempt `json:"injections"`
}

// Attempt is one attack attempt that reached the agent.
type Attempt struct {
	N    int     `json:"n"`
	T    float64 `json:"t"`
	Text string  `json:"text"`
}

// RunStateFile is the run folder path of the attacker export.
const RunStateFile = "state/" + ServiceUser + "/" + StateExport

// ParseRunState decodes an attacker export. It is pure.
func ParseRunState(b []byte) (*RunState, error) {
	var s RunState
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("decode red-team state: %w", err)
	}
	return &s, nil
}

// Payload is the replay of a run's attack: chat turns or injected texts.
func (s *RunState) Payload() Replay {
	var r Replay
	for _, a := range s.Attempts {
		r.Turns = append(r.Turns, clean(a.Text))
	}
	for _, a := range s.Injections {
		r.Injections = append(r.Injections, clean(a.Text))
	}
	return r
}

// clean keeps a payload text byte-stable between Go and Python JSON: valid
// UTF-8, no line or paragraph separator (Go escapes them, Python does not).
func clean(s string) string {
	s = strings.ToValidUTF8(s, " ")
	return strings.NewReplacer(" ", " ", " ", " ").Replace(s)
}

// PayloadSHA256 is the hash redteam.py checks before it replays: sha256 of
// the replay object as compact JSON with sorted keys.
func PayloadSHA256(r Replay) string {
	m := map[string]any{}
	if len(r.Turns) > 0 {
		m["turns"] = r.Turns
	}
	if len(r.Injections) > 0 {
		m["injections"] = r.Injections
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		panic(err) // strings only: cannot fail
	}
	sum := sha256.Sum256(bytes.TrimRight(b.Bytes(), "\n"))
	return hex.EncodeToString(sum[:])
}

// RegressionID is the task id of a regression: reg.<cell>.<payload hash>.
func RegressionID(c Cell, payloadSHA string) string {
	return regressionPrefix + c.Category + "." + c.Strategy + "." + c.Channel + "." + payloadSHA[:10]
}

// Regression is the attack config of a regression task cut from the run
// state s of a task configured as orig: the same world and success rule,
// the payload fixed.
func Regression(orig AttackConfig, s *RunState) (AttackConfig, error) {
	if s == nil || !s.Broken {
		return AttackConfig{}, errors.New("the run's attack did not succeed")
	}
	p := s.Payload()
	n := len(p.Turns)
	if orig.Channel != ChannelChat {
		n = len(p.Injections)
	}
	if n == 0 {
		return AttackConfig{}, errors.New("the run recorded no attack payload")
	}
	c := Cell{Category: orig.Category, Strategy: orig.Strategy, Channel: orig.Channel}
	out := orig
	out.Replay = &p
	out.ReplaySHA256 = PayloadSHA256(p)
	out.ID = RegressionID(c, out.ReplaySHA256)
	out.MaxAttempts = n
	if orig.Channel == ChannelChat {
		out.Script = Script{Opening: p.Turns[0], Seeds: p.Turns}
	} else {
		out.Script = Script{Opening: orig.Script.Opening, Seeds: p.Injections}
	}
	return out, nil
}

// ReviewFile is the content of redteam.review.json.
type ReviewFile struct {
	Suite string       `json:"suite"`
	Items []ReviewItem `json:"items"`
}

// ReviewItem is one regression and its review state.
type ReviewItem struct {
	Task            string     `json:"task"`
	State           string     `json:"state"` // pending | accepted | rejected
	Category        string     `json:"category"`
	Strategy        string     `json:"strategy"`
	Channel         string     `json:"channel"`
	Evaluation      string     `json:"evaluation,omitempty"`
	Run             string     `json:"run"`
	SourceSuite     string     `json:"source_suite"`
	SourceTask      string     `json:"source_task"`
	AttemptsToBreak int        `json:"attempts_to_break,omitempty"`
	AttackerModel   string     `json:"attacker_model,omitempty"`
	PayloadSHA256   string     `json:"payload_sha256"`
	Created         time.Time  `json:"created"`
	DecidedBy       string     `json:"decided_by,omitempty"`
	DecidedAt       *time.Time `json:"decided_at,omitempty"`
}

// LoadReview reads the review record of the suite folder dir; a missing
// file is an empty record.
func LoadReview(dir string) (*ReviewFile, error) {
	b, err := os.ReadFile(filepath.Join(dir, ReviewFileName))
	if errors.Is(err, os.ErrNotExist) {
		return &ReviewFile{Suite: filepath.Base(dir), Items: []ReviewItem{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ReviewFileName, err)
	}
	var f ReviewFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("decode %s: %w", ReviewFileName, err)
	}
	if f.Items == nil {
		f.Items = []ReviewItem{}
	}
	return &f, nil
}

func saveReview(dir string, f *ReviewFile) error {
	b, err := marshal(f)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+ReviewFileName+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", ReviewFileName, err)
	}
	return os.Rename(tmp, filepath.Join(dir, ReviewFileName))
}

// EnsureRegressionsSuite writes the regressions benchmark into dir when it
// has no suite.yaml yet: no tasks, the scripted simulator (replays call no
// model) and the red-team service image source.
func EnsureRegressionsSuite(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "suite.yaml")); err == nil {
		return nil
	}
	rec, err := marshal(PlanRecord{Plan: Plan{Suite: RegressionsSuite}, Generator: "heimdall redteam " + version, Tasks: []Cell{}})
	if err != nil {
		return err
	}
	yml := suiteYAML(RegressionsSuite, "Every attack that once broke your agent, replayed exactly. A new one waits for review before it counts.", nil, Plan{MaxAttempts: MaxMaxAttempts})
	yml = strings.Replace(yml, "  provider: \"\"\n  model: \"\"\n", "", 1)
	files := append([]scaffold.File{
		{Path: "suite.yaml", Data: []byte(yml), Mode: 0o644},
		{Path: PlanFile, Data: rec, Mode: 0o644},
	}, serviceFiles()...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create regressions benchmark: %w", err)
	}
	return scaffold.WriteFiles(dir, files, false)
}

// AddRegression writes cfg as a pending regression task of the regressions
// benchmark in dir and records it for review. It reports false when the
// same payload is already a regression (the id carries its hash).
func AddRegression(dir string, cfg AttackConfig, item ReviewItem) (bool, error) {
	if cfg.Replay == nil || !IsRegressionTask(cfg.ID) {
		return false, errors.New("not a regression attack config")
	}
	if err := EnsureRegressionsSuite(dir); err != nil {
		return false, err
	}
	f, err := LoadReview(dir)
	if err != nil {
		return false, err
	}
	if slices.ContainsFunc(f.Items, func(i ReviewItem) bool { return i.Task == cfg.ID }) {
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks", cfg.ID)); err == nil {
		return false, nil
	}
	files, err := TaskFiles(RegressionsSuite, cfg, taskMeta{
		Title:      "Regression: " + CategoryLabel(cfg.Category) + " by " + StrategyLabel(cfg.Strategy) + " through " + strings.ToLower(ChannelLabel(cfg.Channel)),
		Source:     "Heimdall red-team regression of run " + item.Run + " (" + item.SourceSuite + "/" + item.SourceTask + ")",
		Regression: true,
	})
	if err != nil {
		return false, err
	}
	if err := scaffold.WriteFiles(dir, files, false); err != nil {
		return false, fmt.Errorf("write regression %s: %w", cfg.ID, err)
	}
	item.Task, item.State = cfg.ID, ReviewPending
	item.Category, item.Strategy, item.Channel = cfg.Category, cfg.Strategy, cfg.Channel
	item.PayloadSHA256 = cfg.ReplaySHA256
	f.Items = append(f.Items, item)
	return true, saveReview(dir, f)
}

// Decide records a person's decision on a regression. Accepting lists the
// task in suite.yaml, so readiness checks and evaluations pick it up;
// rejecting keeps it unlisted. A decided regression can be decided again.
func Decide(dir, task, decision, by string, at time.Time) (ReviewItem, error) {
	if decision != ReviewAccepted && decision != ReviewRejected {
		return ReviewItem{}, fmt.Errorf("decision %q: want accepted or rejected", decision)
	}
	f, err := LoadReview(dir)
	if err != nil {
		return ReviewItem{}, err
	}
	i := slices.IndexFunc(f.Items, func(x ReviewItem) bool { return x.Task == task })
	if i < 0 {
		return ReviewItem{}, fmt.Errorf("no regression %s: %w", task, os.ErrNotExist)
	}
	suitePath := filepath.Join(dir, "suite.yaml")
	src, err := os.ReadFile(suitePath)
	if err != nil {
		return ReviewItem{}, fmt.Errorf("read suite.yaml: %w", err)
	}
	var out []byte
	if decision == ReviewAccepted {
		out, _, err = scaffold.AddTaskID(src, task)
	} else {
		out, err = removeTaskID(src, task)
	}
	if err != nil {
		return ReviewItem{}, err
	}
	if !bytes.Equal(out, src) {
		if err := os.WriteFile(suitePath, out, 0o644); err != nil {
			return ReviewItem{}, fmt.Errorf("write suite.yaml: %w", err)
		}
	}
	t := at.UTC()
	f.Items[i].State, f.Items[i].DecidedBy, f.Items[i].DecidedAt = decision, by, &t
	return f.Items[i], saveReview(dir, f)
}

// removeTaskID drops id from the flow list "tasks: [...]" that suiteYAML
// writes; any other layout is left to a person.
func removeTaskID(src []byte, id string) ([]byte, error) {
	lines := strings.Split(string(src), "\n")
	quoted, _ := json.Marshal(id)
	for i, l := range lines {
		if !strings.HasPrefix(l, "tasks: [") || !strings.HasSuffix(strings.TrimSpace(l), "]") {
			continue
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(l), "tasks: ["), "]")
		var keep []string
		for _, p := range strings.Split(inner, ",") {
			p = strings.TrimSpace(p)
			if p != "" && p != id && p != string(quoted) {
				keep = append(keep, p)
			}
		}
		lines[i] = "tasks: [" + strings.Join(keep, ", ") + "]"
		return []byte(strings.Join(lines, "\n")), nil
	}
	if !strings.Contains(string(src), id) {
		return src, nil
	}
	return nil, errors.New("suite.yaml lists tasks in a layout this edit does not know; remove the task by hand")
}
