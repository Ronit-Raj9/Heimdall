package export

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// EEESchemaVersion is the Every Eval Ever aggregate schema version written:
// the "version" of the vendored schemas/eee/eval.schema.json
// (evaleval/every_eval_ever commit 0791adc3, 18 Nov 2026).
const EEESchemaVersion = "0.3.0"

// EEESchemaCommit is the upstream commit the vendored schema was copied from.
const EEESchemaCommit = "0791adc3c2488cc8c91d109b3c541da492238e49"

//go:embed schemas/eee/eval.schema.json
var eeeSchemaJSON []byte

// ErrInvalid refuses to export an experiment whose verdict is INVALID.
var ErrInvalid = errors.New("the experiment's newest verdict is INVALID: its instrument failed, so its results are not findings and are not exported")

// ErrSynthetic refuses to export synthetic results (scripted agent, scripted
// model or smoke experiment) as if they were findings.
var ErrSynthetic = errors.New("the experiment's results are synthetic (scripted agent, scripted model or smoke experiment), not findings; pass -allow-synthetic to export them labelled")

// Evaluator relationships the schema allows (source_metadata).
var eeeRelationships = []string{"first_party", "third_party", "collaborative", "other"}

// EEEOptions sets what the records say about who ran the evaluation.
type EEEOptions struct {
	// Organization is source_metadata.source_organization_name: who
	// provides the data. Required by the schema; "unspecified" when empty.
	Organization string
	// Relationship is source_metadata.evaluator_relationship, relative to
	// the model developer: third_party (default), first_party,
	// collaborative or other.
	Relationship string
	// AllowSynthetic exports synthetic arms, labelled; otherwise any
	// synthetic arm refuses the export (ErrSynthetic).
	AllowSynthetic bool
	// GradeSource is the grade source the runs were read with (reported).
	GradeSource  string
	BenchVersion string
	Now          time.Time // retrieved_timestamp; zero means time.Now
}

// EEEInput is one experiment's sealed results.
type EEEInput struct {
	Manifest  *schemas.Manifest
	Runs      []schemas.SealedRun
	Synthetic map[string]bool // arm id -> synthetic (leaderboard.SyntheticArms)
	// Invalid is true when the experiment's newest analysis decided
	// INVALID. EEE refuses such an experiment (ErrInvalid); the flat run
	// table still lists its runs, labelled.
	Invalid bool
	// Split selects the frozen held-out split of the flat run table
	// (RunTable): "" or public drops every run of a held-out task,
	// held_out keeps only those, all keeps both. EEE always publishes the
	// public split.
	Split string
}

// SplitAll is the private run-table split that keeps public and held-out
// task runs, each row labelled with its split.
const SplitAll = "all"

// ValidSplit reports whether s is a run-table split.
func ValidSplit(s string) bool {
	return s == "" || s == schemas.SplitPublic || s == schemas.SplitHeldOut || s == SplitAll
}

// heldOut reports whether the manifest's frozen plan holds task out.
func heldOut(m *schemas.Manifest, task string) bool {
	return m != nil && slices.Contains(m.HeldOutTasks, task)
}

// inSplit reports whether a run of task belongs to split.
func inSplit(m *schemas.Manifest, task, split string) bool {
	switch split {
	case SplitAll:
		return true
	case schemas.SplitHeldOut:
		return heldOut(m, task)
	}
	return !heldOut(m, task)
}

// EEERecord is one Every Eval Ever aggregate record (EvaluationLog), kept as
// a generic JSON object so only fields the schema defines are written.
type EEERecord = map[string]any

// EEE builds one record per arm of the experiment: one agent config, one
// model, one suite. Each condition the experiment ran is one evaluation
// result group: verified success with its interval, pass^k, cost per
// verified success, and the run medians, computed by the same code as the
// leaderboard and the matrix (leaderboard.Matrix). Every record is
// validated against the vendored schema before it is returned.
func EEE(ctx context.Context, in EEEInput, opts EEEOptions) ([]EEERecord, error) {
	m := in.Manifest
	if m == nil {
		return nil, errors.New("export eee: no manifest")
	}
	rel := opts.Relationship
	if rel == "" {
		rel = "third_party"
	}
	if !contains(eeeRelationships, rel) {
		return nil, fmt.Errorf("export eee: relationship %q: want one of %s", rel, strings.Join(eeeRelationships, ", "))
	}
	anySynthetic := false
	for _, s := range in.Synthetic {
		anySynthetic = anySynthetic || s
	}
	if in.Invalid {
		return nil, ErrInvalid
	}
	if anySynthetic && !opts.AllowSynthetic {
		return nil, ErrSynthetic
	}
	q := leaderboard.MatrixQuery{GradeSource: opts.GradeSource}
	primary, err := leaderboard.Matrix(m, in.Runs, anySynthetic, q)
	if err != nil {
		return nil, fmt.Errorf("export eee: %w", err)
	}
	conds := append([]string{}, primary.Conditions...)
	if len(conds) == 0 {
		return nil, fmt.Errorf("export eee: experiment %s has no graded run", m.ExpID)
	}
	byCond := map[string]*schemas.ModelMatrix{}
	for _, c := range conds {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		mx := primary
		if c != primary.Condition {
			q.Condition = c
			if mx, err = leaderboard.Matrix(m, in.Runs, anySynthetic, q); err != nil {
				return nil, fmt.Errorf("export eee: condition %s: %w", c, err)
			}
		}
		byCond[c] = mx
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	seals, excludedSeals := sealsByArm(m, in.Runs, byCond)
	arms := make([]string, 0, len(m.Arms))
	for id := range m.Arms {
		arms = append(arms, id)
	}
	sort.Strings(arms)
	var out []EEERecord
	for _, arm := range arms {
		var results []any
		for _, c := range conds {
			row := rowOf(byCond[c], arm)
			if row == nil {
				continue
			}
			results = append(results, armResults(m, byCond[c], row, c)...)
		}
		if len(results) == 0 {
			continue
		}
		rec := armRecord(m, arm, in.Synthetic[arm], results, seals[arm], excludedSeals[arm], opts, rel, now)
		if err := ValidateEEE(rec); err != nil {
			return nil, fmt.Errorf("export eee: arm %s: the record does not validate against the vendored schema: %w", arm, err)
		}
		out = append(out, rec)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("export eee: experiment %s has no arm with graded runs", m.ExpID)
	}
	return out, nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// rowOf returns the matrix row of arm, or nil.
func rowOf(mx *schemas.ModelMatrix, arm string) *schemas.MatrixRow {
	for i := range mx.Rows {
		if mx.Rows[i].Arm == arm {
			return &mx.Rows[i]
		}
	}
	return nil
}

// sealsByArm lists, per arm and sorted, the seal hashes of the runs the
// results were computed from: counted public-split runs with no evidence
// problem whose outcome scores, on a condition with a result. A run of the
// sample that was left out (a seal that did not verify, a duplicate counted
// run, an outcome that does not score) is listed separately, so the
// provenance never names a seal that failed verification (finding 1-D-11).
func sealsByArm(m *schemas.Manifest, runs []schemas.SealedRun, byCond map[string]*schemas.ModelMatrix) (used, excluded map[string][]string) {
	used, excluded = map[string][]string{}, map[string][]string{}
	for _, r := range runs {
		if r.SealHash == "" || !r.Slot.Counted || !inSplit(m, r.Slot.Task, schemas.SplitPublic) || byCond[r.Slot.Condition] == nil {
			continue
		}
		if _, scores := r.Run.Outcome.Score(); scores && len(r.Problems) == 0 {
			used[r.Slot.Arm] = append(used[r.Slot.Arm], r.SealHash)
		} else {
			excluded[r.Slot.Arm] = append(excluded[r.Slot.Arm], r.SealHash)
		}
	}
	for _, m := range []map[string][]string{used, excluded} {
		for k := range m {
			sort.Strings(m[k])
		}
	}
	return used, excluded
}

// modelID is model_info.id: provider/model, the form the schema's examples
// use for API models, unless the model already names its provider.
func modelID(provider, model string) string {
	if model == "" {
		return "unknown"
	}
	if strings.Contains(model, "/") || provider == "" {
		return model
	}
	return provider + "/" + model
}

// armRecord builds the EvaluationLog of one arm.
func armRecord(m *schemas.Manifest, arm string, synthetic bool, results []any, seals, excludedSeals []string, opts EEEOptions, rel string, now time.Time) EEERecord {
	a := m.Arms[arm]
	cfg := a.Config
	provider := cfg.Gateway.Provider
	org := opts.Organization
	if org == "" {
		org = "unspecified"
	}
	src := map[string]string{
		"heimdall_experiment":      m.ExpID,
		"heimdall_arm":             arm,
		"heimdall_manifest_hash":   m.Hash,
		"heimdall_suite_lock_hash": m.SuiteLockHash,
		"heimdall_runs_sealed":     strconv.Itoa(len(seals)),
		"heimdall_split":           schemas.SplitPublic, // held-out task runs are never published
		"heimdall_seal_set_sha256": utils.SHA256Bytes([]byte(strings.Join(seals, "\n"))),
		"heimdall_synthetic":       strconv.FormatBool(synthetic),
		"heimdall_grade_source":    firstNonEmpty(opts.GradeSource, schemas.GradeSourceOriginal),
	}
	if b, err := json.Marshal(seals); err == nil {
		src["heimdall_seal_hashes"] = string(b)
	}
	if len(excludedSeals) > 0 {
		// Runs of the sample left out of the results; not provenance.
		src["heimdall_runs_excluded"] = strconv.Itoa(len(excludedSeals))
		if b, err := json.Marshal(excludedSeals); err == nil {
			src["heimdall_excluded_seal_hashes"] = string(b)
		}
	}
	if m.FingerprintHash != "" {
		src["heimdall_fingerprint_hash"] = m.FingerprintHash
	}
	if synthetic {
		src["heimdall_note"] = "synthetic: scripted agent, scripted model or smoke experiment; the numbers are not findings"
	}
	modelDetails := map[string]string{
		"deployment_type":    "externally_managed",
		"model_availability": "unknown",
		"heimdall_provider":  provider,
	}
	if synthetic {
		modelDetails["deployment_type"] = "unknown"
	}
	model := map[string]any{
		"name":               firstNonEmpty(cfg.Model, "unknown"),
		"id":                 modelID(provider, cfg.Model),
		"additional_details": modelDetails,
	}
	if provider != "" {
		model["inference_platform"] = provider
	}
	rec := EEERecord{
		"schema_version":       EEESchemaVersion,
		"evaluation_id":        fmt.Sprintf("heimdall.%s/%s/%s", m.SuiteID, m.ExpID, arm),
		"evaluation_timestamp": strconv.FormatInt(m.CreatedAt.Unix(), 10),
		"retrieved_timestamp":  strconv.FormatInt(now.Unix(), 10),
		"source_metadata": map[string]any{
			"source_name":              "Heimdall",
			"source_type":              "evaluation_run",
			"source_organization_name": org,
			"evaluator_relationship":   rel,
			"additional_details":       src,
		},
		"eval_library": map[string]any{
			"name":    "heimdall",
			"version": firstNonEmpty(m.BenchVersion, opts.BenchVersion, "unknown"),
			"additional_details": map[string]string{
				"exporter":    "bifrost-bench export eee",
				"exported_by": firstNonEmpty(opts.BenchVersion, "unknown"),
			},
		},
		"model_info":         model,
		"evaluation_results": results,
	}
	return rec
}

// armResults is one condition's results for one arm.
func armResults(m *schemas.Manifest, mx *schemas.ModelMatrix, row *schemas.MatrixRow, cond string) []any {
	a := m.Arms[row.Arm]
	name := fmt.Sprintf("heimdall.%s.%s", m.SuiteID, cond)
	source := map[string]any{
		"dataset_name": m.SuiteID + "@" + m.SuiteVersion,
		"source_type":  "other",
		"additional_details": map[string]string{
			"suite_lock_hash": m.SuiteLockHash,
			"condition":       cond,
			"tasks":           strconv.Itoa(row.Tasks),
			"note":            "a Heimdall suite: tasks with hidden verifiers, run in containers; the suite lock hash pins every task file",
		},
	}
	gen := generationConfig(a, cond)
	var out []any
	add := func(metricID, metricName, kind, unit string, lower bool, lo, hi any, v schemas.Value, params map[string]any, unc map[string]any, details map[string]string, desc string) {
		if v.State != schemas.ValueObserved || v.V == nil {
			return
		}
		mc := map[string]any{
			"evaluation_description": desc,
			"metric_id":              metricID,
			"metric_name":            metricName,
			"metric_kind":            kind,
			"metric_unit":            unit,
			"lower_is_better":        lower,
			"score_type":             "continuous",
			"min_score":              lo,
			"max_score":              hi,
		}
		if params != nil {
			mc["metric_parameters"] = params
		}
		sd := map[string]any{"score": *v.V}
		if unc != nil {
			sd["uncertainty"] = unc
		}
		if details != nil {
			sd["details"] = details
		}
		out = append(out, map[string]any{
			"evaluation_result_id": name + "." + metricID,
			"evaluation_name":      name,
			"source_data":          source,
			"metric_config":        mc,
			"score_details":        sd,
			"generation_config":    gen,
		})
	}
	unc := map[string]any{"num_samples": row.Runs}
	if b := row.Success.Bounds; b != nil {
		unc["confidence_interval"] = map[string]any{"lower": b[0], "upper": b[1], "confidence_level": 0.95,
			"method": "t interval over per-task success rates (descriptive)"}
	}
	add("heimdall.verified_success", "verified success", "pass_rate", "proportion", false, 0.0, 1.0, row.Success, nil, unc,
		map[string]string{"passes": fmt.Sprintf("%d/%d", row.Passes.Num, row.Passes.Den), "unknown_runs": strconv.Itoa(row.Passes.Unknown),
			"tasks": strconv.Itoa(row.Tasks), "rank": strconv.Itoa(row.Rank), "tier": strconv.Itoa(row.Tier), "tier_reason": row.TierReason},
		"mean over tasks of the per-task share of runs the hidden verifier passed (intention to treat)")
	add("heimdall.pass_hat_k", fmt.Sprintf("pass^%d", row.Trials), "pass_rate", "proportion", false, 0.0, 1.0, row.Reliability,
		map[string]any{"k": row.Trials}, map[string]any{"num_samples": row.Runs}, nil,
		"mean over tasks of the chance that all k tries pass")
	add("heimdall.cost_per_verified_success_usd", "cost per verified success", "cost", "usd", true, 0.0, "Infinity",
		row.Metrics[leaderboard.MetricCostPerSuccess], nil, nil, nil, "total observed gateway cost over verified successes")
	medians := []struct{ id, name, unit, desc string }{
		{leaderboard.MetricCostUSD, "cost per run (median)", "usd", "median gateway cost of one run"},
		{leaderboard.MetricTokens, "tokens per run (median)", "tokens", "median tokens of one run: fresh, cache read, cache write and output"},
		{leaderboard.MetricWallTime, "wall time per run (median)", "seconds", "median agent wall time of one run"},
		{leaderboard.MetricSteps, "steps per run (median)", "steps", "median agent steps of one run"},
	}
	for _, md := range medians {
		add("heimdall."+md.id+"_median", md.name, "count", md.unit, true, 0.0, "Infinity", row.Metrics[md.id], nil,
			map[string]any{"num_samples": row.Runs}, nil, md.desc)
	}
	add("heimdall.false_completion", "false completion", "rate", "proportion", true, 0.0, 1.0, row.Metrics[leaderboard.MetricFalseCompletion],
		nil, map[string]any{"num_samples": row.Runs}, nil, "share of runs whose agent claimed success the verifier did not confirm")
	return out
}

// generationConfig describes the agent config: the harness, its limits and
// sandbox in typed fields, the rest in additional_details.
func generationConfig(a schemas.ArmManifest, cond string) map[string]any {
	cfg := a.Config
	args := map[string]any{
		"sandbox": map[string]any{"type": "docker", "config": firstNonEmpty(cfg.Image, "unknown")},
		"agentic_eval_config": map[string]any{
			"additional_details": nonEmpty(map[string]string{
				"adapter":       cfg.Adapter,
				"agent_config":  cfg.ID,
				"agent_version": cfg.AgentVersion,
			}),
		},
	}
	limits := map[string]any{}
	if cfg.Budgets.WallS > 0 {
		limits["time_limit"] = cfg.Budgets.WallS
	}
	if cfg.Budgets.Turns > 0 {
		limits["message_limit"] = cfg.Budgets.Turns
	}
	if len(limits) > 0 {
		args["eval_limits"] = limits
	}
	details := map[string]string{
		"heimdall_arm_label":   a.Label,
		"heimdall_config_hash": a.ConfigHash,
		"heimdall_condition":   cond,
		"heimdall_provider":    cfg.Gateway.Provider,
		"heimdall_image_id":    a.ImageID,
	}
	if cfg.Budgets.USD > 0 {
		details["heimdall_budget_usd"] = strconv.FormatFloat(cfg.Budgets.USD, 'f', -1, 64)
	}
	return map[string]any{"generation_args": args, "additional_details": nonEmpty(details)}
}

// nonEmpty drops the empty values of a details map.
func nonEmpty(m map[string]string) map[string]string {
	for k, v := range m {
		if v == "" {
			delete(m, k)
		}
	}
	return m
}

var (
	eeeOnce   sync.Once
	eeeSchema *jsonschema.Schema
	eeeErr    error
)

// ValidateEEE validates one record against the vendored Every Eval Ever
// aggregate schema (JSON Schema draft-07), after a JSON round trip so the
// record is checked exactly as it will be written.
func ValidateEEE(rec any) error {
	eeeOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(eeeSchemaJSON))
		if err != nil {
			eeeErr = err
			return
		}
		c := jsonschema.NewCompiler()
		const url = "https://github.com/evaleval/every_eval_ever/eval.schema.json"
		if err := c.AddResource(url, doc); err != nil {
			eeeErr = err
			return
		}
		eeeSchema, eeeErr = c.Compile(url)
	})
	if eeeErr != nil {
		return fmt.Errorf("compile the vendored EEE schema: %w", eeeErr)
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return err
	}
	if err := eeeSchema.Validate(inst); err != nil {
		return err
	}
	return checkEEEBounds(rec)
}

// checkEEEBounds repeats the upstream validator's semantic score check
// (validation_core.check_score_metadata): a continuous metric has bounds and
// its score lies inside them.
func checkEEEBounds(rec any) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	var doc struct {
		Results []struct {
			Name   string `json:"evaluation_result_id"`
			Metric struct {
				Type string `json:"score_type"`
				Min  any    `json:"min_score"`
				Max  any    `json:"max_score"`
			} `json:"metric_config"`
			Score struct {
				Score float64 `json:"score"`
			} `json:"score_details"`
		} `json:"evaluation_results"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return err
	}
	bound := func(v any) (float64, bool) {
		switch x := v.(type) {
		case float64:
			return x, true
		case string:
			switch x {
			case "Infinity":
				return 1e308, true
			case "-Infinity":
				return -1e308, true
			}
		}
		return 0, false
	}
	for _, r := range doc.Results {
		lo, okLo := bound(r.Metric.Min)
		hi, okHi := bound(r.Metric.Max)
		if r.Metric.Type == "continuous" && (!okLo || !okHi) {
			return fmt.Errorf("%s: continuous metric without bounds", r.Name)
		}
		if okLo && okHi && (r.Score.Score < lo || r.Score.Score > hi) {
			return fmt.Errorf("%s: score %v outside [%v, %v]", r.Name, r.Score.Score, r.Metric.Min, r.Metric.Max)
		}
	}
	return nil
}
