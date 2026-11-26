package gwfeature

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// W4 feature 2 (audit/W4_DESIGN_3_BIFROST.md section 5): two versions of a
// prompt in Bifrost's Prompt Repository as two setups of one comparison. The
// Draft copies each version's messages and model params into its arm, so the
// run never reads the source repository and the config hash covers the text;
// the engine puts a per run copy on the run's gateway (engine/bifrostprompt.go).

const (
	promptMaxMessages = 50
	promptMaxBytes    = 64 << 10
	promptRunPrefix   = "hm-prompt-"
	promptErrConnect  = "Connect your Bifrost first"
)

func init() {
	Register(Feature{
		ID:      schemas.GatewayFeaturePromptVersion,
		Name:    "Prompt repository version",
		Draft:   promptDraft,
		Compute: promptCompute,
	})
}

// promptBaseExperiment is the base experiment every P2 Draft starts from
// (section 3.13), with no arms yet.
func promptBaseExperiment(env DraftEnv, req DraftRequest) *schemas.Experiment {
	tasks := schemas.TaskSelector{All: len(req.Tasks) == 0, IDs: req.Tasks}
	exp := &schemas.Experiment{
		Suite:        req.Suite,
		Base:         req.Base,
		Arms:         map[string]schemas.ArmSpec{},
		Primary:      schemas.Endpoint{Condition: schemas.ConditionClean, Metric: "verified_success"},
		Guards:       []schemas.GuardSpec{{Rule: "no_new_integrity_violation"}},
		Conditions:   []schemas.ConditionSpec{{ID: schemas.ConditionClean, Tasks: tasks, Trials: req.Trials}},
		Verdict:      schemas.VerdictSpec{Policy: "paired_signflip", Alpha: 0.05, AssumedSD: 0.30},
		ExtraGates:   []string{"model_fidelity"},
		Seed:         20261127,
		Concurrency:  2,
		ManifestMode: "agent_exposure",
	}
	if env.Root != "" {
		if _, err := os.Stat(filepath.Join(env.Root, "policies", "default.yaml")); err == nil {
			exp.Policy = "policies/default.yaml"
		}
	}
	return exp
}

// promptArmSet is an arm's overrides: the request's model and provider, so a
// changed base config cannot shift one arm only, plus extra.
func promptArmSet(req DraftRequest, extra map[string]any) map[string]any {
	set := map[string]any{}
	if req.Model != "" {
		set["model"] = req.Model
	}
	if req.Provider != "" {
		set["gateway.provider"] = req.Provider
	}
	for k, v := range extra {
		set[k] = v
	}
	return set
}

// promptRepoVersion is one version as Bifrost's Prompt Repository answers it.
type promptRepoVersion struct {
	VersionNumber int            `json:"version_number"`
	CommitMessage string         `json:"commit_message"`
	ModelParams   map[string]any `json:"model_params"`
	Model         string         `json:"model"`
	CreatedAt     string         `json:"created_at"`
	Messages      []struct {
		OrderIndex int            `json:"order_index"`
		Message    map[string]any `json:"message"`
	} `json:"messages"`
}

// promptHash is the BifrostPrompt.SHA256 of messages and model params.
func promptHash(messages []map[string]any, params map[string]any) (string, error) {
	return utils.HashJSON(map[string]any{"messages": messages, "model_params": params})
}

// promptBuild copies one version into a BifrostPrompt.
func promptBuild(id, name string, v promptRepoVersion) (schemas.BifrostPrompt, error) {
	msgs := v.Messages
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].OrderIndex < msgs[j].OrderIndex })
	p := schemas.BifrostPrompt{Name: name, Version: v.VersionNumber, SourceID: id, ModelParams: v.ModelParams}
	for _, m := range msgs {
		p.Messages = append(p.Messages, m.Message)
	}
	if len(p.Messages) == 0 {
		return p, fmt.Errorf("Version %d of prompt %s has no messages.", v.VersionNumber, name)
	}
	if len(p.Messages) > promptMaxMessages {
		return p, fmt.Errorf("Version %d of prompt %s has %d messages; at most %d can be tested.", v.VersionNumber, name, len(p.Messages), promptMaxMessages)
	}
	b, err := json.Marshal(map[string]any{"messages": p.Messages, "model_params": p.ModelParams})
	if err != nil {
		return p, err
	}
	if len(b) > promptMaxBytes {
		return p, fmt.Errorf("Version %d of prompt %s is larger than 64 KiB.", v.VersionNumber, name)
	}
	h, err := promptHash(p.Messages, p.ModelParams)
	if err != nil {
		return p, err
	}
	p.SHA256 = h
	return p, nil
}

// promptSetValue is the arm override value of a prompt: its JSON shape as a
// plain map, so the experiment round-trips through JSON and YAML.
func promptSetValue(p schemas.BifrostPrompt) (map[string]any, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	return out, json.Unmarshal(b, &out)
}

func promptDraft(ctx context.Context, env DraftEnv, req DraftRequest) (*schemas.Experiment, []string, error) {
	if env.SourceGet == nil {
		return nil, nil, errors.New(promptErrConnect)
	}
	id := strings.TrimSpace(req.Options["prompt_id"])
	if id == "" {
		return nil, nil, errors.New("Pick a prompt.")
	}
	variant, err := strconv.Atoi(strings.TrimSpace(req.Options["variant_version"]))
	if err != nil {
		return nil, nil, errors.New("Pick the prompt version to test.")
	}
	control := -1 // none
	if c := strings.TrimSpace(req.Options["control_version"]); c != "" && c != "none" {
		if control, err = strconv.Atoi(c); err != nil {
			return nil, nil, errors.New("The version to compare against must be a version number or none.")
		}
	}
	if control == variant {
		return nil, nil, errors.New("Pick two different versions.")
	}
	var head struct {
		Prompt struct {
			Name string `json:"name"`
		} `json:"prompt"`
	}
	base := "/api/prompt-repo/prompts/" + url.PathEscape(id)
	if err := env.SourceGet(ctx, base, &head); err != nil {
		return nil, nil, fmt.Errorf("Heimdall could not read prompt %s from your Bifrost: %v", id, err)
	}
	var list struct {
		Versions []promptRepoVersion `json:"versions"`
	}
	if err := env.SourceGet(ctx, base+"/versions", &list); err != nil {
		return nil, nil, fmt.Errorf("Heimdall could not read the versions of prompt %s from your Bifrost: %v", id, err)
	}
	name := head.Prompt.Name
	if name == "" {
		name = id
	}
	find := func(n int) (schemas.BifrostPrompt, error) {
		for _, v := range list.Versions {
			if v.VersionNumber == n {
				return promptBuild(id, name, v)
			}
		}
		return schemas.BifrostPrompt{}, fmt.Errorf("Prompt %s has no version %d.", name, n)
	}
	exp := promptBaseExperiment(env, req)
	if control < 0 {
		exp.Arms["A"] = schemas.ArmSpec{Label: "no Bifrost prompt", Set: promptArmSet(req, nil)}
	} else {
		pa, err := find(control)
		if err != nil {
			return nil, nil, err
		}
		va, err := promptSetValue(pa)
		if err != nil {
			return nil, nil, err
		}
		exp.Arms["A"] = schemas.ArmSpec{Label: fmt.Sprintf("prompt v%d", control), Set: promptArmSet(req, map[string]any{"gateway.bifrost.prompt": va})}
	}
	pb, err := find(variant)
	if err != nil {
		return nil, nil, err
	}
	vb, err := promptSetValue(pb)
	if err != nil {
		return nil, nil, err
	}
	exp.Arms["B"] = schemas.ArmSpec{Label: fmt.Sprintf("prompt v%d", variant), Set: promptArmSet(req, map[string]any{"gateway.bifrost.prompt": vb})}
	exp.Name = "Bifrost test: prompt " + name
	exp.GatewayTest = &schemas.GatewayTest{Feature: schemas.GatewayFeaturePromptVersion, Control: "A", Variants: []string{"B"}, Options: req.Options}
	notes := []string{
		"The prompt is put before your agent's own messages on every model request, as Bifrost does in production.",
		"The prompt adds input tokens to every model call, so its cost shows in the measured cost of each setup.",
		"Prompt variables are not filled in by Bifrost at request time in this test.",
	}
	return exp, notes, nil
}

// promptOfArm is the prompt an arm of the manifest carries, or nil.
func promptOfArm(m *schemas.Manifest, arm string) *schemas.BifrostPrompt {
	if m == nil {
		return nil
	}
	f := m.Arms[arm].Config.Gateway.Bifrost
	if f == nil {
		return nil
	}
	return f.Prompt
}

// promptArmName is how a headline names an arm: "v4", or "no prompt".
func promptArmName(p *schemas.BifrostPrompt) string {
	if p == nil {
		return "no prompt"
	}
	return fmt.Sprintf("v%d", p.Version)
}

func promptCompute(_ context.Context, in Input) Effect {
	control := in.Test.Control
	variant := ""
	if len(in.Test.Variants) > 0 {
		variant = in.Test.Variants[0]
	}
	e := Effect{Variant: variant, Arms: ArmResults(in.Runs, in.Manifest, append([]string{control}, in.Test.Variants...))}
	label := func(arm string) string {
		if in.Manifest != nil && in.Manifest.Arms[arm].Label != "" {
			return in.Manifest.Arms[arm].Label
		}
		return arm
	}

	// 1. Applied check, from the sealed Bifrost detail of each call.
	total, raw := 0, 0
	for _, arm := range append([]string{control}, in.Test.Variants...) {
		withPrompt := promptOfArm(in.Manifest, arm) != nil
		calls, miss := 0, 0
		for _, r := range in.Runs {
			if r.Arm != arm {
				continue
			}
			for _, row := range r.Rows {
				if row.Kind != "message" {
					continue
				}
				total++
				if !row.RawKnown {
					continue
				}
				raw++
				calls++
				if withPrompt && (row.PromptName != promptRunPrefix+r.RunID || row.PromptVersion == "") {
					miss++
				}
				if !withPrompt && row.PromptName != "" {
					miss++
				}
			}
		}
		if miss == 0 {
			continue
		}
		e.State, e.Result = StateNotApplied, "failed"
		if withPrompt {
			e.Reason = fmt.Sprintf("Bifrost did not apply the prompt on %d of %d calls of setup %s: is the prompts plugin loaded?", miss, calls, label(arm))
		} else {
			e.Reason = fmt.Sprintf("setup %s got a prompt it did not ask for", arm)
		}
		e.Headline = "Not applied: " + e.Reason
		return e
	}
	if total > 0 && raw*2 < total {
		e.State = StateUnknown
		e.Reason = fmt.Sprintf("Bifrost's full call records were kept for only %d of %d calls, so Heimdall cannot prove the prompt was applied", raw, total)
		e.Headline = "Unknown: " + e.Reason
		return e
	}

	// 2. Effect.
	solve := PairedTasks(in.Runs, control, variant, SolveValue)
	cost := PairedTasks(in.Runs, control, variant, CostValue)
	lat := PairedTasks(in.Runs, control, variant, LatencyValue)
	e.Tasks = solve.Tasks
	if solve.Tasks < MinTasks {
		e.State = StateNotEnoughData
		e.Reason = fmt.Sprintf("%d tasks have results on both setups; at least %d are needed", solve.Tasks, MinTasks)
		e.Headline = "Not enough data: " + e.Reason
		return e
	}
	e.State = StateMeasured
	e.SolveDelta = &solve.Delta
	if cost.Tasks > 0 {
		e.CostDelta = &cost.Delta
	}
	if lat.Tasks > 0 {
		e.LatencyDelta = &lat.Delta
	}

	// 3. Headline.
	pc, pv := promptOfArm(in.Manifest, control), promptOfArm(in.Manifest, variant)
	e.Headline = promptHeadline(pv, pc, e, promptControlCost(e.Arms, control))

	// 4. Setting.
	switch {
	case solve.Delta.High != nil && *solve.Delta.High < 0:
		e.Result = "lost"
	case e.CostDelta != nil && e.CostDelta.Point < 0 && solve.Delta.Low != nil && *solve.Delta.Low >= -0.03:
		e.Result = "saved"
	default:
		e.Result = "worked"
	}
	notWorse := solve.Delta.Low == nil || *solve.Delta.Low >= -0.03
	if notWorse && pv != nil {
		srcID := pv.SourceID
		if srcID == "" {
			srcID = in.Test.Options["prompt_id"]
		}
		e.Setting = &BifrostSetting{
			Kind:  "request_headers",
			Title: fmt.Sprintf("Use prompt %s v%d", pv.Name, pv.Version),
			Where: fmt.Sprintf("Send these headers on your app's model requests (or switch the version your app pins to %d).", pv.Version),
			Body:  fmt.Sprintf("x-bf-prompt-id: %s\nx-bf-prompt-version: %d", srcID, pv.Version),
		}
	} else {
		title := "Keep no prompt"
		if pc != nil {
			title = fmt.Sprintf("Keep prompt v%d", pc.Version)
		}
		e.Setting = &BifrostSetting{Kind: "none", Title: title}
	}
	return e
}

// promptControlCost is the control setup's mean cost per run, or 0.
func promptControlCost(arms []ArmResult, control string) float64 {
	for _, a := range arms {
		if a.Arm == control && a.CostPerRun != nil {
			return *a.CostPerRun
		}
	}
	return 0
}

// promptHeadline is "Prompt v4 solved 6 points more tasks than v3 (range -2
// to +14) at 3% more cost per task."
func promptHeadline(pv, pc *schemas.BifrostPrompt, e Effect, baseCost float64) string {
	d := e.SolveDelta
	pts := math.Round(d.Point * 100)
	more := "more"
	if pts < 0 {
		more = "fewer"
	}
	s := fmt.Sprintf("Prompt %s solved %.0f points %s tasks than %s", promptArmName(pv), math.Abs(pts), more, promptArmName(pc))
	if d.Low != nil && d.High != nil {
		s += fmt.Sprintf(" (range %s to %s)", promptSignedPoints(*d.Low), promptSignedPoints(*d.High))
	}
	if e.CostDelta != nil && baseCost > 0 {
		pct := math.Round(e.CostDelta.Point / baseCost * 100)
		word := "more"
		if pct < 0 {
			word = "less"
		}
		s += fmt.Sprintf(" at %.0f%% %s cost per task", math.Abs(pct), word)
	}
	return s + "."
}

// promptSignedPoints is a fraction as signed percentage points: -2, +14.
func promptSignedPoints(v float64) string {
	p := math.Round(v * 100)
	if p > 0 {
		return fmt.Sprintf("+%.0f", p)
	}
	return fmt.Sprintf("%.0f", p)
}
