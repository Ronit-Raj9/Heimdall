package handlers

// Lane I28 (W4-41, D4 10.4): custom behaviour checks. The org's own rules
// live in behaviour-checks.yaml at the project root; every run is checked
// on read with the built-in and the org's rules, and nothing is stored.
// The checks never change a score.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/steprules"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// BehaviourChecksFile is the org's rule file, at the project root.
const BehaviourChecksFile = "behaviour-checks.yaml"

const (
	maxBehaviourRules     = 50
	maxBehaviourFileBytes = 256 << 10
	maxBehaviourRuns      = 500
)

// behaviourFile is the on-disk form of BehaviourChecksFile.
type behaviourFile struct {
	Version int                `yaml:"version"`
	Classes *steprules.Classes `yaml:"classes,omitempty"`
	Rules   []steprules.Rule   `yaml:"rules"`
}

// BehaviourChecks answers GET /behaviour-checks.
type BehaviourChecks struct {
	Builtin []steprules.Rule  `json:"builtin"`
	Rules   []steprules.Rule  `json:"rules"`
	Classes steprules.Classes `json:"classes"`
}

// BehaviourChecksRequest is the body of PUT /behaviour-checks.
type BehaviourChecksRequest struct {
	Rules   []steprules.Rule   `json:"rules"`
	Classes *steprules.Classes `json:"classes,omitempty"`
}

// BehaviourRuleError is one rule a PUT refused.
type BehaviourRuleError struct {
	Rule  string `json:"rule"`
	Error string `json:"error"`
}

// BehaviourChecksInvalid answers a refused PUT (422).
type BehaviourChecksInvalid struct {
	Error  lib.ErrorField       `json:"error"`
	Errors []BehaviourRuleError `json:"errors"`
}

// RunBehaviour answers GET /runs/{id}/behaviour.
type RunBehaviour struct {
	RunID     string             `json:"run_id"`
	Available bool               `json:"available"`
	Truncated bool               `json:"truncated"`
	Results   []steprules.Result `json:"results"`
}

// BehaviourArmCounts is one rule's counts over one arm's runs.
type BehaviourArmCounts struct {
	Fired       int `json:"fired"`
	Runs        int `json:"runs"`
	Unknown     int `json:"unknown"`
	FiredPassed int `json:"fired_passed"`
	FiredFailed int `json:"fired_failed"`
}

// ExperimentBehaviourRule is one rule over an experiment.
type ExperimentBehaviourRule struct {
	Rule   steprules.Rule                `json:"rule"`
	PerArm map[string]BehaviourArmCounts `json:"per_arm"`
	Runs   map[string][]string           `json:"runs"` // arm: run ids where it fired
}

// ExperimentBehaviour answers GET /experiments/{id}/behaviour.
type ExperimentBehaviour struct {
	ExpID   string                    `json:"exp_id"`
	Rules   []ExperimentBehaviourRule `json:"rules"`
	Partial bool                      `json:"partial,omitempty"`
}

type behaviourHandler struct {
	mode  schemas.Mode
	root  string
	index ReadIndex
	mu    *sync.Mutex
}

// behaviourLocks holds one lock per project root, so two org routers over
// the same root never interleave a rewrite.
var behaviourLocks sync.Map

func init() {
	RegisterOrgArea("behaviour", func(env OrgEnv, r *router.Router) {
		root := ""
		if env.Cfg != nil {
			root = env.Cfg.Root
		}
		mu, _ := behaviourLocks.LoadOrStore(root, &sync.Mutex{})
		h := &behaviourHandler{mode: env.Mode, root: root, index: env.Services.Index, mu: mu.(*sync.Mutex)}
		r.GET(APIPrefix+"/behaviour-checks", env.R(h.get))
		r.PUT(APIPrefix+"/behaviour-checks", env.W(h.put))
		r.GET(APIPrefix+"/runs/{id}/behaviour", env.R(h.run))
		r.GET(APIPrefix+"/experiments/{id}/behaviour", env.R(h.experiment))
	})
}

func (h *behaviourHandler) path() string { return filepath.Join(h.root, BehaviourChecksFile) }

// load reads the org's rule file; a missing file is an empty one.
func (h *behaviourHandler) load() (behaviourFile, error) {
	f := behaviourFile{Version: 1}
	data, err := os.ReadFile(h.path())
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if len(data) > maxBehaviourFileBytes {
		return f, errors.New("the behaviour checks file is larger than 256 KiB")
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return f, errors.New("the behaviour checks file is not valid: " + err.Error())
	}
	return f, nil
}

func (f behaviourFile) classes() steprules.Classes {
	c := steprules.DefaultClasses()
	if f.Classes != nil {
		c = c.Merge(*f.Classes)
	}
	return c
}

// compiled returns the built-in rules then the org's, each compiled with the
// org's classes unless the rule carries its own. A user rule that no longer
// compiles is left out.
func (f behaviourFile) compiled() []*steprules.Compiled {
	cls := f.classes()
	var out []*steprules.Compiled
	for _, r := range append(steprules.Builtin(), f.Rules...) {
		if r.Classes == nil {
			c := cls
			r.Classes = &c
		}
		if c, err := steprules.Compile(r); err == nil {
			out = append(out, c)
		}
	}
	return out
}

func (h *behaviourHandler) get(ctx *fasthttp.RequestCtx) {
	f, err := h.load()
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	out := BehaviourChecks{Builtin: steprules.Builtin(), Rules: f.Rules, Classes: f.classes()}
	if out.Rules == nil {
		out.Rules = []steprules.Rule{}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

func (h *behaviourHandler) put(ctx *fasthttp.RequestCtx) {
	if h.root == "" {
		sendUnavailable(ctx, h.mode, "the project folder")
		return
	}
	if len(ctx.PostBody()) > maxBehaviourFileBytes {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, "The behaviour checks can be at most 256 KiB.")
		return
	}
	var req BehaviourChecksRequest
	if err := decodeJSONStrict(ctx.PostBody(), &req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "invalid body: "+err.Error())
		return
	}
	if len(req.Rules) > maxBehaviourRules {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "There can be at most 50 behaviour checks.")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	old, err := h.load()
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	prev := map[string]steprules.Rule{}
	for _, r := range old.Rules {
		prev[r.ID] = r
	}
	var bad []BehaviourRuleError
	seen := map[string]bool{}
	rules := make([]steprules.Rule, 0, len(req.Rules))
	for _, r := range req.Rules {
		switch {
		case steprules.IsBuiltinID(r.ID):
			bad = append(bad, BehaviourRuleError{Rule: r.ID, Error: "this id belongs to a built-in check; copy it under a new id"})
			continue
		case seen[r.ID]:
			bad = append(bad, BehaviourRuleError{Rule: r.ID, Error: "two checks have this id"})
			continue
		}
		seen[r.ID] = true
		if _, err := steprules.Compile(r); err != nil {
			bad = append(bad, BehaviourRuleError{Rule: r.ID, Error: err.Error()})
			continue
		}
		r.Version = nextRuleVersion(prev, r)
		rules = append(rules, r)
	}
	if len(bad) > 0 {
		out := BehaviourChecksInvalid{Error: lib.ErrorField{Message: "Some behaviour checks are not valid.", Type: lib.ErrTypeBadRequest}, Errors: bad}
		lib.SendJSON(ctx, h.mode, fasthttp.StatusUnprocessableEntity, &out)
		return
	}
	f := behaviourFile{Version: 1, Classes: req.Classes, Rules: rules}
	data, err := yaml.Marshal(&f)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "encode the behaviour checks: "+err.Error())
		return
	}
	if err := writeBehaviourFile(h.path(), data); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "save the behaviour checks: "+err.Error())
		return
	}
	out := BehaviourChecks{Builtin: steprules.Builtin(), Rules: rules, Classes: f.classes()}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

// nextRuleVersion keeps the stored version of an unchanged rule and bumps a
// changed one; a new rule starts at 1.
func nextRuleVersion(prev map[string]steprules.Rule, r steprules.Rule) int {
	p, ok := prev[r.ID]
	if !ok {
		return 1
	}
	a, b := p, r
	a.Version, b.Version = 0, 0
	if reflect.DeepEqual(a, b) {
		return p.Version
	}
	return p.Version + 1
}

// writeBehaviourFile writes data beside path and renames it into place.
func writeBehaviourFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// checkRun evaluates every rule on one run.
func (h *behaviourHandler) checkRun(runID string, rules []*steprules.Compiled) (RunBehaviour, error) {
	out := RunBehaviour{RunID: runID, Results: []steprules.Result{}}
	steps, trunc, err := steprules.Load(func(n string) ([]byte, error) { return h.index.RunFile(runID, n) }, 0)
	if err != nil {
		return out, err
	}
	out.Available, out.Truncated = len(steps) > 0, trunc
	for _, c := range rules {
		out.Results = append(out.Results, c.Eval(steps, trunc))
	}
	return out, nil
}

func (h *behaviourHandler) run(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.mode, "id", "run")
	if !ok {
		return
	}
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "the run index")
		return
	}
	if _, ok := h.index.Run(id); !ok {
		sendNotFound(ctx, h.mode, "run", id)
		return
	}
	f, err := h.load()
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	out, err := h.checkRun(id, f.compiled())
	if err != nil {
		sendDepError(ctx, h.mode, "read the steps of run "+id, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

func (h *behaviourHandler) experiment(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.mode, "id", "experiment")
	if !ok {
		return
	}
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "the run index")
		return
	}
	if _, ok := h.index.Manifest(id); !ok {
		sendNotFound(ctx, h.mode, "experiment", id)
		return
	}
	f, err := h.load()
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	rules := f.compiled()
	out := ExperimentBehaviour{ExpID: id, Rules: make([]ExperimentBehaviourRule, len(rules))}
	for i, c := range rules {
		out.Rules[i] = ExperimentBehaviourRule{Rule: c.Rule(), PerArm: map[string]BehaviourArmCounts{}, Runs: map[string][]string{}}
	}
	views := h.index.Runs(id)
	sort.SliceStable(views, func(i, j int) bool { return views[i].Run.RunID < views[j].Run.RunID })
	read := 0
	for _, v := range views {
		if v.Superseded || v.Run.RunID == "" {
			continue
		}
		if read >= maxBehaviourRuns {
			out.Partial = true
			break
		}
		read++
		rb, err := h.checkRun(v.Run.RunID, rules)
		if err != nil {
			sendDepError(ctx, h.mode, "read the steps of run "+v.Run.RunID, err)
			return
		}
		arm := v.Slot.Arm
		y, scored := v.Run.Outcome.Score()
		for i, res := range rb.Results {
			pa := out.Rules[i].PerArm[arm]
			pa.Runs++
			switch res.Status {
			case steprules.StatusUnknown:
				pa.Unknown++
			case steprules.StatusFired:
				pa.Fired++
				if scored && y == 1 {
					pa.FiredPassed++
				} else if scored {
					pa.FiredFailed++
				}
				out.Rules[i].Runs[arm] = append(out.Rules[i].Runs[arm], v.Run.RunID)
			}
			out.Rules[i].PerArm[arm] = pa
		}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}
