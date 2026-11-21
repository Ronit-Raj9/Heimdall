// Package toolbenchx imports ToolBench-X (arXiv 2606.25819) as a suite of
// track tool_use with output kind state.
//
// Each task runs its own upstream tool module (Python functions) as an MCP
// service behind the tool proxy (image/tbxsvc.py), with one extra tool,
// submit_answer. The service exports the submitted answer; the verifier, in
// the same image, compares it with the task's final_answer by upstream's
// exact-match rule. Variant clean serves tools/; variant hazard serves the
// tools_exception/ twin, whose seeded fault injection runs with upstream's
// strict_no_hint_profile. docs/importing-toolbench-x.md explains the format.
package toolbenchx

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// Name is the import format name.
const Name = "toolbench-x"

// Importer settings (-set key=value).
const (
	SetVariant = "variant" // clean (default) or hazard
	SetRef     = "ref"     // dataset revision (Hugging Face commit) to record
)

// Values of SetVariant.
const (
	VariantClean  = "clean"
	VariantHazard = "hazard"
)

const (
	upstreamRepo    = "https://huggingface.co/datasets/Foreverskyou/ToolBench-X"
	upstreamCode    = "https://github.com/Foreverskyou/ToolBench-X"
	upstreamLicense = "academic research only (dataset card); code MIT"
	serviceID       = "toolbench"
	servicePort     = 8000
	suiteVersion    = "1.0.0"
	taskVersion     = "1.0.0"
	agentTimeoutS   = 900
	verifierTimeout = 120
)

//go:embed image/Dockerfile
var serviceDockerfile string

//go:embed image/tbxsvc.py
var serviceScript string

//go:embed image/base.Dockerfile
var baseDockerfile string

//go:embed image/control.py
var controlScript string

func init() {
	importers.Register(&importer{now: time.Now})
}

type importer struct {
	now func() time.Time
}

// Info implements importers.Importer.
func (im *importer) Info() importers.Info {
	return importers.Info{
		Name:       Name,
		Benchmarks: []string{"ToolBench-X: 1,106 tasks (378 sequential, 358 parallel, 370 mixture), clean tools and hazard (tools_exception) variants"},
		Source:     "hf download Foreverskyou/ToolBench-X --repo-type dataset; unzip tasks.zip tools.zip tools_exception.zip",
		License:    upstreamLicense,
		Track:      schemas.TrackToolUse,
		Grading:    schemas.GradingVerified,
		Needs: []string{
			"network while the service image builds (pip install requests)",
			"services, state export, script controls and the tool proxy in the run",
		},
		Summary: "ToolBench-X multi-step tool-use tasks: each task's upstream tools as an MCP service, graded by exact match on the submitted answer; variant hazard adds upstream's recoverable tool faults",
	}
}

// upTask is one task of a ToolBench-X task file.
type upTask struct {
	ID         string            `json:"id"`
	UserPrompt string            `json:"user_prompt"`
	ToolsUsed  []json.RawMessage `json:"tools_used"`
	Final      json.RawMessage   `json:"final_answer"`
}

// candidate is one task with a tool module.
type candidate struct {
	id        string // bench task id
	taskType  string // sequential, parallel, mixture
	topic     string
	subtopic  string
	upID      string // task_N
	prompt    string
	tools     []string
	answer    string
	toolsPath string // upstream tool module for the chosen variant
	hazard    string // hazard category (hazard variant)
}

// layout finds the tasks, tools and tools_exception roots under from,
// accepting both the unzipped release (tasks/tasks/...) and a flat layout.
func layout(from string) (tasks, tools, exc string, err error) {
	pick := func(name string) string {
		for _, p := range []string{filepath.Join(from, name, name), filepath.Join(from, name)} {
			if st, e := os.Stat(filepath.Join(p, "sequential")); e == nil && st.IsDir() {
				return p
			}
		}
		return ""
	}
	tasks, tools, exc = pick("tasks"), pick("tools"), pick("tools_exception")
	if tasks == "" || tools == "" {
		return "", "", "", fmt.Errorf("%s: want tasks/ and tools/ folders with sequential/ inside (the unzipped release)", from)
	}
	return tasks, tools, exc, nil
}

var hexRef = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

var badIDChars = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(badIDChars.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

var typeShort = map[string]string{"sequential": "seq", "parallel": "par", "mixture": "mix"}

// taskID is the bench id of one upstream task: <type>-<topic>-<subtopic>-tN.
func taskID(taskType, topic, subtopic, upID string) string {
	n := strings.TrimPrefix(upID, "task_")
	id := typeShort[taskType] + "-" + slug(topic) + "-" + slug(subtopic) + "-t" + slug(n)
	if len(id) > 90 {
		id = id[:77] + "-" + shortHash(id)
	}
	return id
}

func shortHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// toolName reads a tools_used entry: a name, or {"tool_name": ...} in
// mixture tasks.
func toolName(raw json.RawMessage) (string, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	var o struct {
		ToolName string `json:"tool_name"`
	}
	if err := json.Unmarshal(raw, &o); err != nil || o.ToolName == "" {
		return "", fmt.Errorf("unreadable tools_used entry %s", string(raw))
	}
	return o.ToolName, nil
}

// answerText is str(final_answer) as upstream compares it.
func answerText(raw json.RawMessage) (string, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s), nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil || v == nil {
		return "", errors.New("no final_answer")
	}
	return strings.TrimSpace(string(raw)), nil
}

// scan lists every task that has a tool module for the variant; tasks
// without one are skipped with the reason.
func scan(from, variant string) ([]candidate, []importers.Skip, error) {
	tasksDir, toolsDir, excDir, err := layout(from)
	if err != nil {
		return nil, nil, err
	}
	if variant == VariantHazard && excDir == "" {
		return nil, nil, fmt.Errorf("%s: variant hazard needs the tools_exception/ folder", from)
	}
	// hazard: index tools_exception/<type>/<category>/<topic>/<subtopic>/task_N.py
	excIndex := map[string][2]string{}
	if variant == VariantHazard {
		files, _ := filepath.Glob(filepath.Join(excDir, "*", "*", "*", "*", "*.py"))
		for _, f := range files {
			rel, _ := filepath.Rel(excDir, f)
			p := strings.Split(filepath.ToSlash(rel), "/")
			key := p[0] + "/" + p[2] + "/" + p[3] + "/" + strings.TrimSuffix(p[4], ".py")
			excIndex[key] = [2]string{f, p[1]}
		}
	}
	files, err := filepath.Glob(filepath.Join(tasksDir, "*", "*", "*.json"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(files)
	var out []candidate
	var skips []importers.Skip
	for _, f := range files {
		rel, _ := filepath.Rel(tasksDir, f)
		p := strings.Split(filepath.ToSlash(rel), "/")
		taskType, topic, subtopic := p[0], p[1], strings.TrimSuffix(p[2], ".json")
		if typeShort[taskType] == "" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, nil, err
		}
		var doc struct {
			Tasks []upTask `json:"tasks"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", rel, err)
		}
		for _, t := range doc.Tasks {
			id := taskID(taskType, topic, subtopic, t.ID)
			c := candidate{id: id, taskType: taskType, topic: topic, subtopic: subtopic, upID: t.ID, prompt: t.UserPrompt}
			if variant == VariantHazard {
				hit, ok := excIndex[taskType+"/"+topic+"/"+subtopic+"/"+t.ID]
				if !ok {
					skips = append(skips, importers.Skip{ID: id, Reason: "no tool module in the release (upstream ships tools for 1,106 of the listed tasks)"})
					continue
				}
				c.toolsPath, c.hazard = hit[0], hit[1]
			} else {
				c.toolsPath = filepath.Join(toolsDir, taskType, topic, subtopic, t.ID+".py")
				if _, err := os.Stat(c.toolsPath); err != nil {
					skips = append(skips, importers.Skip{ID: id, Reason: "no tool module in the release (upstream ships tools for 1,106 of the listed tasks)"})
					continue
				}
			}
			if strings.TrimSpace(t.UserPrompt) == "" {
				skips = append(skips, importers.Skip{ID: id, Reason: "empty user_prompt"})
				continue
			}
			if c.answer, err = answerText(t.Final); err != nil || c.answer == "" {
				skips = append(skips, importers.Skip{ID: id, Reason: "no final_answer to match"})
				continue
			}
			for _, raw := range t.ToolsUsed {
				n, err := toolName(raw)
				if err != nil {
					return nil, nil, fmt.Errorf("%s %s: %w", rel, t.ID, err)
				}
				if !slices.Contains(c.tools, n) {
					c.tools = append(c.tools, n)
				}
			}
			out = append(out, c)
		}
	}
	return out, skips, nil
}

// Import implements importers.Importer.
func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	if opts.SuiteID == "" || opts.From == "" || opts.BenchRoot == "" {
		return nil, errors.New("toolbench-x: bench root, -from and -suite are required")
	}
	variant := VariantClean
	for k, v := range opts.Extra {
		switch k {
		case SetVariant:
			if v != VariantClean && v != VariantHazard {
				return nil, fmt.Errorf("toolbench-x: %s=%q: want clean or hazard", k, v)
			}
			variant = v
		case SetRef:
		default:
			return nil, fmt.Errorf("toolbench-x: unknown setting %q (known: %s, %s)", k, SetVariant, SetRef)
		}
	}
	cands, skips, err := scan(opts.From, variant)
	if err != nil {
		return nil, fmt.Errorf("toolbench-x: %w", err)
	}
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: opts.SuiteDir(), Skipped: skips}
	var picked []candidate
	for _, c := range cands {
		if len(opts.IDs) > 0 && !slices.Contains(opts.IDs, c.id) {
			continue
		}
		picked = append(picked, c)
	}
	if opts.Limit > 0 && len(picked) > opts.Limit {
		for _, c := range picked[opts.Limit:] {
			res.Skipped = append(res.Skipped, importers.Skip{ID: c.id, Reason: fmt.Sprintf("over -limit %d", opts.Limit)})
		}
		picked = picked[:opts.Limit]
	}
	rev := opts.Extra[SetRef]
	if !hexRef.MatchString(rev) {
		return nil, fmt.Errorf("toolbench-x: -set %s=<dataset commit> is required (hex; the Hugging Face dataset revision)", SetRef)
	}
	w := &writer{revision: rev, variant: variant, suiteID: opts.SuiteID, suiteDir: res.SuiteDir}
	w.baseTag = "bench/toolbench-x-base:" + shortHash(baseDockerfile)
	for _, c := range picked {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("toolbench-x: %w", err)
		}
		dir := filepath.Join(res.SuiteDir, "tasks", c.id)
		if _, err := os.Stat(dir); err == nil && !opts.Force {
			res.Skipped = append(res.Skipped, importers.Skip{ID: c.id, Reason: "task folder exists (pass -force to replace it)"})
			continue
		}
		if err := w.writeTask(dir, c); err != nil {
			return nil, fmt.Errorf("toolbench-x: task %s: %w", c.id, err)
		}
		res.Tasks = append(res.Tasks, c.id)
	}
	rec := schemas.ImportRecord{
		Importer:  Name,
		Benchmark: "ToolBench-X " + variant,
		Source:    upstreamRepo,
		Revision:  rev,
		License:   upstreamLicense,
		At:        im.now().UTC().Truncate(time.Second),
	}
	if err := w.writeSuite(opts.SuiteID, res.Tasks, rec); err != nil {
		return nil, fmt.Errorf("toolbench-x: %w", err)
	}
	if err := importers.Finish(res.SuiteDir); err != nil {
		return nil, err
	}
	res.Notes = append(res.Notes,
		"bifrost-bench suite lint "+opts.SuiteID,
		"bifrost-bench suite lock "+opts.SuiteID,
		"bifrost-bench suite admit "+opts.SuiteID,
		"tools run offline (deterministic fallback path); scores are not upstream leaderboard numbers (docs/importing-toolbench-x.md)",
	)
	return res, nil
}

func existingTasks(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "suite.yaml"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s schemas.Suite
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("read existing suite.yaml: %w", err)
	}
	return s.TaskIDs, nil
}
