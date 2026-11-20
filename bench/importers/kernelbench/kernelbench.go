// Package kernelbench imports KernelBench (X7, docs/gpu-tasks.md): PyTorch
// reference models (class Model, get_inputs, get_init_inputs) that an agent
// rewrites as a faster ModelNew with its own GPU kernels. Each problem
// becomes a diff task on the ml_engineering track that asks for one GPU.
//
// Upstream facts, checked on 18 Nov 2026 against the primary source,
// https://github.com/ScalingIntelligence/KernelBench at commit 423217d9
// (5 Mar 2026): the LICENSE file is the MIT license (GitHub's detector
// reports it as "Other" because of an extra copyright line); level 1 is 100
// files KernelBench/level1/<n>_<Name>.py; src/kernelbench/eval.py builds
// Model and ModelNew after the same seed, calls them on get_inputs(), and
// compares in float32 with atol = rtol = 1e-2 over several random draws
// before timing. Upstream ships no ModelNew solutions.
package kernelbench

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Format is the importer's name on the command line.
const Format = "kernelbench"

const (
	source   = "https://github.com/ScalingIntelligence/KernelBench"
	revision = "423217d9fda91e0c2d67e4a43bf62f96f6d104f1"
	license  = "MIT"
	// defaultImage is the acme-gpu example's CUDA image (PyTorch 2.15 with
	// CUDA 13.2, Triton 3.9): the agent, the controls and the verifier all
	// run in it. -set image=<tag> names another with PyTorch on CUDA.
	defaultImage      = "heimdall-example/acme-gpu-cuda:1"
	defaultMaxInputMB = 256
	defaultMinVRAMMB  = 2048
	defaultAgentS     = 1800
	defaultVerifyS    = 900
	// callBudgetBytes bounds the inputs one timed round holds on the GPU.
	callBudgetBytes = 256 << 20
	maxCalls        = 16
)

//go:embed templates/verify.py templates/harness.py
var templates embed.FS

func init() {
	importers.Register(&importer{})
}

type importer struct{}

// Info implements importers.Importer.
func (im *importer) Info() importers.Info {
	return importers.Info{
		Name:       Format,
		Benchmarks: []string{"KernelBench level 1 (100 single-operator problems)"},
		Source:     "git clone " + source + " (checked at " + revision[:8] + "); -from is the clone or its KernelBench/level1 folder",
		License:    license,
		Track:      schemas.TrackMLEngineering,
		Grading:    schemas.GradingVerified,
		Needs: []string{
			"an NVIDIA GPU: every task asks for one (resources.gpu); bifrost-bench gpu check",
			"an image with PyTorch on CUDA and Triton: default " + defaultImage + " (examples/acme-gpu), or -set image=<tag>",
		},
		Summary: "rewrite a PyTorch reference model as a faster ModelNew with custom GPU kernels; correct within atol = rtol = 1e-2, speedup measured in the clean room with an interval",
	}
}

// settings are the -set options.
type settings struct {
	image         string
	maxInputMB    float64
	minVRAMMB     int
	speedupTarget float64
}

func parseSettings(extra map[string]string) (settings, error) {
	s := settings{image: defaultImage, maxInputMB: defaultMaxInputMB, minVRAMMB: defaultMinVRAMMB}
	for k, v := range extra {
		switch k {
		case "image":
			if strings.TrimSpace(v) == "" {
				return s, errors.New("-set image needs a tag")
			}
			s.image = v
		case "max_input_mb":
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f <= 0 {
				return s, fmt.Errorf("-set max_input_mb=%q must be a positive number", v)
			}
			s.maxInputMB = f
		case "min_vram_mb":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return s, fmt.Errorf("-set min_vram_mb=%q must be a whole number", v)
			}
			s.minVRAMMB = n
		case "speedup_target":
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 || math.IsInf(f, 0) {
				return s, fmt.Errorf("-set speedup_target=%q must be a number, 0 or more", v)
			}
			s.speedupTarget = f
		case "level":
			if v != "1" {
				return s, fmt.Errorf("-set level=%s: only level 1 is imported (levels 2 to 4 fuse several operators and need more GPU memory than this importer was checked with)", v)
			}
		default:
			return s, fmt.Errorf("unknown setting %q (image, max_input_mb, min_vram_mb, speedup_target, level)", k)
		}
	}
	return s, nil
}

// problem is one upstream file.
type problem struct {
	num    int
	name   string // file name without .py
	file   string
	source []byte
}

var fileRe = regexp.MustCompile(`^([0-9]+)_([A-Za-z0-9_]+)\.py$`)

// Import implements importers.Importer.
func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	if opts.Logger == nil {
		opts.Logger = schemas.NoopLogger{}
	}
	if !validID(opts.SuiteID) {
		return nil, fmt.Errorf("suite id %q must match [a-z0-9][a-z0-9._-]*", opts.SuiteID)
	}
	set, err := parseSettings(opts.Extra)
	if err != nil {
		return nil, err
	}
	probs, err := readProblems(opts.From)
	if err != nil {
		return nil, err
	}
	dir := opts.SuiteDir()
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: dir}
	want := map[string]bool{}
	for _, id := range opts.IDs {
		want[strings.TrimSuffix(id, ".py")] = true
	}
	var written []string
	for _, p := range probs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if len(want) > 0 && !want[strconv.Itoa(p.num)] && !want[p.name] && !want[taskID(p)] {
			continue
		}
		if opts.Limit > 0 && len(written) >= opts.Limit {
			break
		}
		if why := checkProblem(p.source); why != "" {
			res.Skipped = append(res.Skipped, importers.Skip{ID: p.name, Reason: why})
			continue
		}
		bytes, why := EstimateInputBytes(p.source)
		if why != "" {
			res.Skipped = append(res.Skipped, importers.Skip{ID: p.name, Reason: "input size not known without running it: " + why})
			continue
		}
		if mb := float64(bytes) / (1 << 20); mb > set.maxInputMB {
			res.Skipped = append(res.Skipped, importers.Skip{ID: p.name,
				Reason: fmt.Sprintf("inputs take about %.0f MiB, over max_input_mb %.0f (the GPU also holds the outputs and both models)", mb, set.maxInputMB)})
			continue
		}
		id := taskID(p)
		tdir := filepath.Join(dir, "tasks", id)
		if _, err := os.Stat(tdir); err == nil && !opts.Force {
			res.Skipped = append(res.Skipped, importers.Skip{ID: p.name, Reason: "task folder exists (use -force to replace it)"})
			continue
		}
		if err := writeTask(tdir, id, p, set, bytes); err != nil {
			return nil, err
		}
		written = append(written, id)
	}
	if len(written) == 0 {
		return res, errors.New("no task imported: " + skipSummary(res.Skipped))
	}
	if err := writeSuite(dir, opts.SuiteID, written, set); err != nil {
		return nil, err
	}
	if err := importers.Finish(dir); err != nil {
		return nil, err
	}
	res.Tasks = written
	res.Notes = []string{
		"bifrost-bench gpu check, to see the GPU every task asks for",
		fmt.Sprintf("bifrost-bench suite lock %s, then bifrost-bench suite admit %s: the reference control is ModelNew = Model unchanged, so it proves the verifier and the correctness check, not a speedup", opts.SuiteID, opts.SuiteID),
		"speedup is reported per run with its interval; -set speedup_target=1 makes a pass also need the interval above 1 (KernelBench fast_1), and the unchanged reference can then no longer be admitted",
	}
	return res, nil
}

func skipSummary(skips []importers.Skip) string {
	if len(skips) == 0 {
		return "no problem matched -ids"
	}
	return fmt.Sprintf("%d problems skipped, first %s: %s", len(skips), skips[0].ID, skips[0].Reason)
}

// readProblems lists the level-1 files of a clone, or of the level1 folder
// itself, in problem order.
func readProblems(from string) ([]problem, error) {
	if from == "" {
		return nil, errors.New("-from is required: a clone of " + source + " or its KernelBench/level1 folder")
	}
	dir := from
	if st, err := os.Stat(filepath.Join(from, "KernelBench", "level1")); err == nil && st.IsDir() {
		dir = filepath.Join(from, "KernelBench", "level1")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var out []problem
	for _, e := range entries {
		m := fileRe.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		if len(b) > 256<<10 {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		out = append(out, problem{num: n, name: strings.TrimSuffix(e.Name(), ".py"), file: e.Name(), source: b})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s holds no <n>_<Name>.py problem files", dir)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].num < out[j].num })
	return out, nil
}

// checkProblem says why a file is not a KernelBench problem, or "".
func checkProblem(src []byte) string {
	s := string(src)
	for _, need := range []string{"class Model(", "def get_inputs(", "def get_init_inputs("} {
		if !strings.Contains(s, need) {
			return "no " + strings.TrimSuffix(need, "(")
		}
	}
	if strings.Contains(s, "class ModelNew") {
		return "already defines ModelNew"
	}
	return ""
}

var nonID = regexp.MustCompile(`[^a-z0-9]+`)

// taskID is l1-<nnn>-<slug of the name>, at most 64 characters.
func taskID(p problem) string {
	name := strings.TrimPrefix(p.name, strconv.Itoa(p.num)+"_")
	slug := strings.Trim(nonID.ReplaceAllString(strings.ToLower(name), "-"), "-")
	id := fmt.Sprintf("l1-%03d-%s", p.num, slug)
	if len(id) > 64 {
		id = strings.TrimRight(id[:64], "-")
	}
	return id
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func validID(id string) bool { return idRe.MatchString(id) }

// Static size estimate of get_inputs(). KernelBench problems set their sizes
// as module-level integer constants and build inputs with torch.rand-like
// calls on them, so the estimate needs no Python: it evaluates integer
// constants (literals, earlier names, + - * // ** and parentheses) and sums
// the float32 bytes of every torch.rand/randn/randint/zeros/ones/empty call
// inside get_inputs. Anything else (a loop, a list comprehension of tensors,
// an unknown name) makes the size unknown, and the problem is skipped with
// the reason rather than guessed.

var (
	constRe   = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*([^#\n]+?)\s*(?:#.*)?$`)
	tensorRe  = regexp.MustCompile(`torch\.(rand|randn|randint|zeros|ones|empty)\(`)
	loopRe    = regexp.MustCompile(`\bfor\b|\bwhile\b`)
	funcRe    = regexp.MustCompile(`(?m)^def get_inputs\(\):\s*$`)
	nextDefRe = regexp.MustCompile(`(?m)^(def |class |[A-Za-z_][A-Za-z0-9_]*\s*=)`)
	idNameRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// EstimateInputBytes returns the float32 bytes get_inputs() allocates, or
// why it cannot tell. It is pure.
func EstimateInputBytes(src []byte) (int64, string) {
	s := string(src)
	env := map[string]int64{}
	for _, m := range constRe.FindAllStringSubmatch(s, -1) {
		// A chained assignment (height = width = 256) names every target.
		parts := strings.Split(m[2], "=")
		if strings.Contains(m[2], "==") {
			continue
		}
		v, err := evalInt(parts[len(parts)-1], env)
		if err != nil {
			continue
		}
		env[m[1]] = v
		for _, name := range parts[:len(parts)-1] {
			if n := strings.TrimSpace(name); idNameRe.MatchString(n) {
				env[n] = v
			}
		}
	}
	loc := funcRe.FindStringIndex(s)
	if loc == nil {
		return 0, "no module-level def get_inputs()"
	}
	body := s[loc[1]:]
	if next := nextDefRe.FindStringIndex(body); next != nil {
		body = body[:next[0]]
	}
	if loopRe.MatchString(body) {
		return 0, "get_inputs() loops"
	}
	calls := tensorRe.FindAllStringSubmatchIndex(body, -1)
	if len(calls) == 0 {
		return 0, "get_inputs() makes no tensor with torch.rand, randn, randint, zeros, ones or empty"
	}
	var total int64
	for _, c := range calls {
		fn := body[c[2]:c[3]]
		args, ok := callArgs(body[c[1]:])
		if !ok {
			return 0, "unbalanced call in get_inputs()"
		}
		var dims []string
		for _, a := range splitTop(args) {
			a = strings.TrimSpace(a)
			if a == "" || strings.Contains(a, "=") {
				continue
			}
			dims = append(dims, a)
		}
		if fn == "randint" {
			if len(dims) < 3 {
				return 0, "torch.randint without a size"
			}
			dims = dims[2:]
		}
		if len(dims) == 1 && strings.HasPrefix(strings.TrimSpace(dims[0]), "(") {
			dims = splitTop(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(dims[0]), "("), ")"))
		}
		n := int64(1)
		for _, d := range dims {
			if strings.TrimSpace(d) == "" {
				continue
			}
			v, err := evalInt(d, env)
			if err != nil {
				return 0, fmt.Sprintf("size %q: %v", strings.TrimSpace(d), err)
			}
			if v <= 0 || n > math.MaxInt64/max(v, 1)/4 {
				return 0, fmt.Sprintf("size %q is not a positive size", strings.TrimSpace(d))
			}
			n *= v
		}
		total += n * 4
	}
	return total, ""
}

// callArgs returns the text up to the parenthesis that closes a call whose
// opening parenthesis was just consumed.
func callArgs(s string) (string, bool) {
	depth := 1
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[:i], true
			}
		}
	}
	return "", false
}

// splitTop splits on commas outside parentheses and brackets.
func splitTop(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// evalInt evaluates an integer expression of literals, known names, + - *
// // ** and parentheses. It is pure.
func evalInt(expr string, env map[string]int64) (int64, error) {
	p := &intParser{s: strings.TrimSpace(expr), env: env}
	v, err := p.sum()
	if err != nil {
		return 0, err
	}
	if p.skip(); p.i != len(p.s) {
		return 0, fmt.Errorf("unexpected %q", p.s[p.i:])
	}
	return v, nil
}

type intParser struct {
	s   string
	i   int
	env map[string]int64
}

func (p *intParser) skip() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

func (p *intParser) sum() (int64, error) {
	v, err := p.product()
	for err == nil {
		p.skip()
		if p.i >= len(p.s) || (p.s[p.i] != '+' && p.s[p.i] != '-') {
			return v, nil
		}
		op := p.s[p.i]
		p.i++
		var w int64
		if w, err = p.product(); err == nil {
			if op == '+' {
				v += w
			} else {
				v -= w
			}
		}
	}
	return 0, err
}

func (p *intParser) product() (int64, error) {
	v, err := p.power()
	for err == nil {
		p.skip()
		switch {
		case strings.HasPrefix(p.s[p.i:], "**"):
			return v, nil
		case strings.HasPrefix(p.s[p.i:], "//"):
			p.i += 2
			var w int64
			if w, err = p.power(); err == nil {
				if w == 0 {
					return 0, errors.New("division by zero")
				}
				v /= w
			}
		case p.i < len(p.s) && p.s[p.i] == '*':
			p.i++
			var w int64
			if w, err = p.power(); err == nil {
				v *= w
			}
		default:
			return v, nil
		}
	}
	return 0, err
}

func (p *intParser) power() (int64, error) {
	base, err := p.atom()
	if err != nil {
		return 0, err
	}
	p.skip()
	if !strings.HasPrefix(p.s[p.i:], "**") {
		return base, nil
	}
	p.i += 2
	exp, err := p.power()
	if err != nil {
		return 0, err
	}
	if exp < 0 || exp > 62 {
		return 0, errors.New("exponent out of range")
	}
	out := int64(1)
	for range exp {
		out *= base
	}
	return out, nil
}

func (p *intParser) atom() (int64, error) {
	p.skip()
	if p.i >= len(p.s) {
		return 0, errors.New("expression ends early")
	}
	switch c := p.s[p.i]; {
	case c == '(':
		p.i++
		v, err := p.sum()
		if err != nil {
			return 0, err
		}
		p.skip()
		if p.i >= len(p.s) || p.s[p.i] != ')' {
			return 0, errors.New("missing )")
		}
		p.i++
		return v, nil
	case c == '-':
		p.i++
		v, err := p.atom()
		return -v, err
	case c >= '0' && c <= '9':
		j := p.i
		for j < len(p.s) && (p.s[j] >= '0' && p.s[j] <= '9' || p.s[j] == '_') {
			j++
		}
		v, err := strconv.ParseInt(strings.ReplaceAll(p.s[p.i:j], "_", ""), 10, 64)
		p.i = j
		return v, err
	case c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z':
		j := p.i
		for j < len(p.s) && (p.s[j] == '_' || p.s[j] >= 'A' && p.s[j] <= 'Z' || p.s[j] >= 'a' && p.s[j] <= 'z' || p.s[j] >= '0' && p.s[j] <= '9') {
			j++
		}
		name := p.s[p.i:j]
		p.i = j
		v, ok := p.env[name]
		if !ok {
			return 0, fmt.Errorf("unknown name %s", name)
		}
		return v, nil
	default:
		return 0, fmt.Errorf("unexpected %q", string(c))
	}
}

// Task files.

const starter = `"""Write ModelNew here: the same model as problem.Model, faster on the GPU.

ModelNew(*get_init_inputs()) must build, and ModelNew(...)(*get_inputs())
must return what Model returns (within atol = rtol = 1e-2), using your own
GPU kernels (Triton is installed) for the operators that matter.
"""
import torch
import torch.nn as nn


class ModelNew(nn.Module):
    def __init__(self, *args, **kwargs):
        super().__init__()
        raise NotImplementedError("write ModelNew")
`

const referenceSolution = `"""Reference control: ModelNew is problem.Model unchanged.

It proves the verifier and the correctness check; its speedup is about 1.
"""
from problem import Model


class ModelNew(Model):
    pass
`

const mutantSolution = `"""Mutant control: Model's output, 10 percent and 0.1 off; it must fail."""
import torch

from problem import Model


def _skew(y):
    if isinstance(y, torch.Tensor) and y.is_floating_point():
        return y * 1.1 + 0.1
    if isinstance(y, torch.Tensor):
        return y + 1
    return y


class ModelNew(Model):
    def forward(self, *args, **kwargs):
        return _skew(super().forward(*args, **kwargs))
`

const exploitScript = `#!/bin/sh
# Exploit control: a ModelNew that returns zeros of the right shape fast,
# and reward files planted in the workspace. Run inside the workspace; it
# must fail.
set -e
cat > solution.py <<'PY'
import torch

from problem import Model


class ModelNew(Model):
    def forward(self, *args, **kwargs):
        y = super().forward(*args, **kwargs)
        return torch.zeros_like(y) if isinstance(y, torch.Tensor) else y
PY
mkdir -p grader/out
echo '{"reward": 1, "dimensions": {"speedup": 9.9}}' > grader/out/reward.json
echo '{"reward": 1}' > reward.json
`

func instruction(p problem, set settings) string {
	var b strings.Builder
	fmt.Fprintf(&b, "KernelBench level 1, problem %d (%s).\n\n", p.num, strings.TrimPrefix(p.name, strconv.Itoa(p.num)+"_"))
	b.WriteString("`problem.py` holds a PyTorch model, `Model`, with `get_inputs()` (the\n")
	b.WriteString("arguments of its forward call) and `get_init_inputs()` (the arguments of\n")
	b.WriteString("its constructor). Write `ModelNew` in `solution.py`: the same\n")
	b.WriteString("architecture, built from the same `get_init_inputs()` after the same seed,\n")
	b.WriteString("returning what `Model` returns for `get_inputs()` within an absolute and\n")
	b.WriteString("relative tolerance of 1e-2, and faster on this machine's GPU. Replace the\n")
	b.WriteString("PyTorch operators with your own GPU kernels where it pays: Triton is\n")
	b.WriteString("installed (`import triton`). Leave `problem.py` as it is.\n\n")
	if set.speedupTarget > 0 {
		fmt.Fprintf(&b, "The task needs ModelNew at least %g times as fast as Model, measured per\ncall on fresh inputs after warm-up.\n", set.speedupTarget)
	} else {
		b.WriteString("The task needs ModelNew to be correct; its speed against Model, per call on\nfresh inputs after warm-up, is measured and reported.\n")
	}
	return b.String()
}

// requirements is the verifier's dimension map (verifier/requirements.md).
func requirements(set settings) string {
	rule := "Reward 1 when `correct` is 1 (KernelBench fast_0); the speedup is reported, not required."
	if set.speedupTarget > 0 {
		rule = fmt.Sprintf("Reward 1 when `correct` is 1 and `speedup_lo` is at least %g (KernelBench fast_%g).", set.speedupTarget, set.speedupTarget)
	}
	return "# What the verifier checks\n\n" +
		"| Dimension | Holds | Instruction line |\n|---|---|---|\n" +
		"| `correct` | 1 when ModelNew matches Model within atol = rtol = 1e-2 on 5 input draws seeded at grading time, and on one random timed call per round | \"returning what `Model` returns for `get_inputs()` within an absolute and relative tolerance of 1e-2\" |\n" +
		"| `speedup`, `speedup_lo`, `speedup_hi` | median per-round ratio of Model's time over ModelNew's, with the 2nd smallest and 2nd largest of 9 rounds (about 96% for the median) | \"faster on this machine's GPU\" |\n" +
		"| `ref_ms`, `agent_ms` | median milliseconds per call | |\n" +
		"| `out_of_memory` | 1 when ModelNew ran out of GPU memory on the first draw: the problem does not fit this GPU as written | |\n\n" + rule + "\n"
}

type taskDoc struct {
	ID           string               `yaml:"id"`
	Version      string               `yaml:"version"`
	Title        string               `yaml:"title"`
	Project      string               `yaml:"project"`
	Kind         string               `yaml:"kind"`
	Instruction  string               `yaml:"instruction"`
	RepoDir      string               `yaml:"repo_dir"`
	Resources    schemas.Resources    `yaml:"resources"`
	Verifier     verifierDoc          `yaml:"verifier"`
	Protected    []string             `yaml:"protected"`
	Controls     controlsDoc          `yaml:"controls"`
	Timeouts     schemas.TaskTimeouts `yaml:"timeouts"`
	Tags         []string             `yaml:"tags"`
	Capabilities []string             `yaml:"capabilities"`
	Provenance   provenanceDoc        `yaml:"provenance"`
}

type verifierDoc struct {
	Tests    string   `yaml:"tests"`
	Required []string `yaml:"required"`
}

type controlsDoc struct {
	Reference string   `yaml:"reference"`
	Empty     string   `yaml:"empty"`
	Mutants   []string `yaml:"mutants"`
	Exploit   string   `yaml:"exploit"`
}

type provenanceDoc struct {
	Source       string `yaml:"source"`
	License      string `yaml:"license"`
	ImportedFrom string `yaml:"imported_from"`
	CreatedBy    string `yaml:"created_by"`
}

// calls is how many input sets one timed round holds: as many as fit the
// round's budget, 1 to maxCalls.
func calls(inputBytes int64) int {
	if inputBytes <= 0 {
		return maxCalls
	}
	return int(min(max(callBudgetBytes/inputBytes, 1), maxCalls))
}

// writeTask writes one task folder.
func writeTask(dir, id string, p problem, set settings, inputBytes int64) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("replace %s: %w", dir, err)
	}
	files := map[string][]byte{
		"repo/problem.py":           p.source,
		"repo/solution.py":          []byte(starter),
		"repo/README.md":            []byte(fmt.Sprintf("# KernelBench level 1: %s\n\n- `problem.py`: the reference model, from %s (%s), unchanged.\n- `solution.py`: write `ModelNew` here.\n\nThe container has PyTorch on CUDA and Triton; no network.\n", p.name, source, p.file)),
		"instruction.md":            []byte(instruction(p, set)),
		"verifier/tests/problem.py": p.source,
		"verifier/tests/kb.json":    []byte(fmt.Sprintf("{\"tolerance\": 0.01, \"correct_trials\": 5, \"calls\": %d, \"speedup_target\": %g}\n", calls(inputBytes), set.speedupTarget)),
		"verifier/requirements.md":  []byte(requirements(set)),
		"controls/empty.diff":       nil,
		"controls/exploit.sh":       []byte(exploitScript),
		"solution/patch.diff":       []byte(solutionDiff(starter, referenceSolution)),
		"controls/mutant-skew.diff": []byte(solutionDiff(starter, mutantSolution)),
	}
	for _, name := range []string{"verify.py", "harness.py"} {
		b, err := templates.ReadFile("templates/" + name)
		if err != nil {
			return fmt.Errorf("kernelbench: template %s: %w", name, err)
		}
		files["verifier/tests/"+name] = b
	}
	doc := taskDoc{
		ID: id, Version: "1.0.0",
		Title:       fmt.Sprintf("KernelBench level 1 #%d: a faster %s", p.num, strings.TrimPrefix(p.name, strconv.Itoa(p.num)+"_")),
		Project:     "kernelbench-level1",
		Kind:        string(schemas.TaskPerformance),
		Instruction: "instruction.md",
		RepoDir:     "repo",
		Resources:   schemas.Resources{GPU: &schemas.GPURequest{Count: 1, MinVRAMMB: set.minVRAMMB}},
		Verifier:    verifierDoc{Tests: "verifier/tests", Required: []string{}},
		Protected:   []string{"problem.py"},
		Controls: controlsDoc{Reference: "solution/patch.diff", Empty: "controls/empty.diff",
			Mutants: []string{"controls/mutant-skew.diff"}, Exploit: "controls/exploit.sh"},
		Timeouts:     schemas.TaskTimeouts{AgentS: defaultAgentS},
		Tags:         []string{"kernelbench", "level1"},
		Capabilities: []string{"gpu_kernels", "performance", "gpu"},
		Provenance: provenanceDoc{Source: source, License: license,
			ImportedFrom: fmt.Sprintf("kernelbench@%s KernelBench/level1/%s", revision[:8], p.file),
			CreatedBy:    "bifrost-bench import " + Format},
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("kernelbench: marshal task: %w", err)
	}
	files["task.yaml"] = out
	for rel, b := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(path, b, mode); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

// solutionDiff is a git patch that replaces solution.py's whole content.
func solutionDiff(from, to string) string {
	old := strings.Split(strings.TrimSuffix(from, "\n"), "\n")
	nw := strings.Split(strings.TrimSuffix(to, "\n"), "\n")
	var b strings.Builder
	b.WriteString("diff --git a/solution.py b/solution.py\n--- a/solution.py\n+++ b/solution.py\n")
	fmt.Fprintf(&b, "@@ -1,%d +1,%d @@\n", len(old), len(nw))
	for _, l := range old {
		b.WriteString("-" + l + "\n")
	}
	for _, l := range nw {
		b.WriteString("+" + l + "\n")
	}
	return b.String()
}

type suiteDoc struct {
	ID          string               `yaml:"id"`
	Version     string               `yaml:"version"`
	Description string               `yaml:"description"`
	Track       string               `yaml:"track"`
	Environment string               `yaml:"environment"`
	Grader      string               `yaml:"grader"`
	BaseImage   imageDoc             `yaml:"base_image"`
	Workdir     string               `yaml:"workdir"`
	Verifier    suiteVerifierDoc     `yaml:"verifier"`
	Tasks       []string             `yaml:"tasks"`
	Probes      []string             `yaml:"probes"`
	Imported    schemas.ImportRecord `yaml:"imported"`
}

type imageDoc struct {
	Tag string `yaml:"tag"`
}

type suiteVerifierDoc struct {
	Command       []string `yaml:"command"`
	Result        string   `yaml:"result"`
	ResultPath    string   `yaml:"result_path"`
	PassThreshold float64  `yaml:"pass_threshold"`
	Restore       []string `yaml:"restore"`
	TimeoutS      int      `yaml:"timeout_s"`
}

// writeSuite writes suite.yaml, keeping the tasks an earlier import of this
// importer listed. A suite.yaml written by anything else is refused.
func writeSuite(dir, id string, tasks []string, set settings) error {
	path := filepath.Join(dir, "suite.yaml")
	var prev suiteDoc
	if b, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(b, &prev); err != nil || prev.Imported.Importer != Format {
			return fmt.Errorf("%s exists and was not written by the %s importer; pick another suite id", path, Format)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	all := slices.Clone(prev.Tasks)
	for _, t := range tasks {
		if !slices.Contains(all, t) {
			all = append(all, t)
		}
	}
	sort.Strings(all)
	doc := suiteDoc{
		ID: id, Version: "1.0.0",
		Description: "KernelBench level 1: rewrite a PyTorch model as a faster ModelNew with custom GPU kernels; correct within atol = rtol = 1e-2, speedup measured in the clean room.",
		Track:       schemas.TrackMLEngineering,
		Environment: "docker_workspace",
		Grader:      "verifier_command",
		BaseImage:   imageDoc{Tag: set.image},
		Workdir:     "/workspace",
		Verifier: suiteVerifierDoc{Command: []string{"python3", "-I", "-B", "/grader/tests/verify.py"}, Result: schemas.VerifierResultReward,
			ResultPath: "/grader/out/reward.json", PassThreshold: 1, Restore: []string{"problem.py"}, TimeoutS: defaultVerifyS},
		Tasks:  all,
		Probes: []string{},
		Imported: schemas.ImportRecord{Importer: Format, Benchmark: "kernelbench level1", Source: source, Revision: revision,
			License: license, At: time.Now().UTC().Truncate(time.Second)},
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal suite: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("write suite: %w", err)
	}
	return os.WriteFile(path, out, 0o644)
}
