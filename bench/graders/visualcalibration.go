package graders

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/visual"
)

// X1 vision grading: calibration of the vision judge against labelled
// images (docs/visual-grading.md). Items are rows of the suite's
// labels.jsonl that name an image (calibration/images/<sha256>.png); the
// record is an ordinary W8 calibration record whose templates are the
// vision prompt's, so the judge_calibration instrument reads it unchanged.

// VisualCalibrationPlan is what a vision calibration will ask: one prompt
// per labelled image, both passes, and the calls that takes.
type VisualCalibrationPlan struct {
	Spec      schemas.VisualJudgeSpec
	PromptSHA string
	Templates map[string]string // visual-v1 -> sha256 of its text
	Items     []VisualPlannedItem
	Skipped   []string
	Calls     int
}

// VisualPlannedItem is one labelled image with its task's rubric and
// reference.
type VisualPlannedItem struct {
	Item  calibration.Labelled
	Image *visual.Image
	Ref   *visual.Image
	Msg   visionMessage
}

// VisualJudged reports whether task t of suite s can be calibrated: it has an
// output.visual rubric and pins a vision judge. A label on any other task is
// never used by visual calibrate.
func VisualJudged(s *schemas.Suite, t *schemas.Task) bool {
	if t == nil {
		return false
	}
	v := VisualSpec(s, t)
	return v != nil && v.Rubric != "" && v.Judge != nil
}

// PlanVisualCalibration resolves each labelled image item of suite s to its
// task's rubric and reference. Every task with a rubric must pin the same
// vision judge (one record covers one judge); text items and items whose
// task has no rubric are skipped and named.
func PlanVisualCalibration(s *schemas.Suite, items []calibration.Labelled) (*VisualCalibrationPlan, error) {
	var spec *schemas.VisualJudgeSpec
	for _, t := range s.Tasks {
		v := VisualSpec(s, t)
		if v == nil || v.Rubric == "" || v.Judge == nil {
			continue
		}
		if spec == nil {
			spec = v.Judge
			continue
		}
		if spec.Provider != v.Judge.Provider || spec.Model != v.Judge.Model || spec.PromptVersion != v.Judge.PromptVersion || spec.Votes != v.Judge.Votes {
			return nil, fmt.Errorf("tasks of suite %s pin different vision judges; one calibration covers one judge", s.ID)
		}
	}
	if spec == nil {
		return nil, fmt.Errorf("suite %s has no task with a rubric and a vision judge", s.ID)
	}
	if err := CheckVisualJudge(spec); err != nil {
		return nil, err
	}
	tmpl, sha, err := VisualPrompt(spec.PromptVersion)
	if err != nil {
		return nil, err
	}
	p := &VisualCalibrationPlan{Spec: *spec, PromptSHA: sha, Templates: map[string]string{spec.PromptVersion: sha}}
	p.Spec.Votes = max(p.Spec.Votes, 1)
	for _, it := range items {
		if it.Item.Image == "" {
			p.Skipped = append(p.Skipped, fmt.Sprintf("item %s: a text answer, not an image", it.Item.ID))
			continue
		}
		t := s.TaskByID(it.Item.Task)
		v := VisualSpec(s, t)
		if t == nil || v == nil || v.Rubric == "" {
			p.Skipped = append(p.Skipped, fmt.Sprintf("item %s: task %s has no output.visual rubric", it.Item.ID, it.Item.Task))
			continue
		}
		pi, err := planVisualItem(s, t, v, tmpl, it)
		if err != nil {
			p.Skipped = append(p.Skipped, fmt.Sprintf("item %s: %v", it.Item.ID, err))
			continue
		}
		p.Items = append(p.Items, pi)
		p.Calls += 2 * p.Spec.Votes
	}
	return p, nil
}

func planVisualItem(s *schemas.Suite, t *schemas.Task, v *schemas.VisualCheck, tmpl string, it calibration.Labelled) (VisualPlannedItem, error) {
	if err := calibration.ValidateImage(s.Root, it.Item.Image); err != nil {
		return VisualPlannedItem{}, err
	}
	data, err := os.ReadFile(calibration.ImagePath(s.Root, it.Item.Image))
	if err != nil {
		return VisualPlannedItem{}, err
	}
	img, err := visual.Decode(data)
	if err != nil {
		return VisualPlannedItem{}, err
	}
	rp, err := visual.TaskPath(t.Dir, v.Rubric)
	if err != nil {
		return VisualPlannedItem{}, err
	}
	rubric, err := readCapped(rp)
	if err != nil {
		return VisualPlannedItem{}, fmt.Errorf("rubric: %w", err)
	}
	var ref *visual.Image
	if v.Reference != "" {
		p, err := visual.TaskPath(t.Dir, v.Reference)
		if err != nil {
			return VisualPlannedItem{}, err
		}
		raw, err := readCapped(p)
		if err != nil {
			return VisualPlannedItem{}, fmt.Errorf("reference: %w", err)
		}
		if ref, err = visual.Decode(raw); err != nil {
			return VisualPlannedItem{}, fmt.Errorf("reference: %w", err)
		}
	}
	msg, err := visionPrompt(tmpl, string(rubric), img, ref)
	if err != nil {
		return VisualPlannedItem{}, err
	}
	return VisualPlannedItem{Item: it, Image: img, Ref: ref, Msg: msg}, nil
}

// RunVisualCalibration casts every vote of the plan on one vision judge key
// minted for the calibration (never a run's), revoked at the end also on
// failure: a first pass and a second identical pass, each with the judge's
// votes at temperature 0. There are no perturbations: the text ones do not
// apply to images. A failed call stops the calibration: a partial
// calibration is not written.
func RunVisualCalibration(ctx context.Context, cfg JudgeConfig, plan *VisualCalibrationPlan, progress func(done, total int)) ([]calibration.ItemRun, *CalibrationSpend, error) {
	g := newVisual(VisualConfig{Judge: cfg})
	if cfg.Gateway == nil || cfg.URL == "" {
		return nil, nil, errors.New("vision judge is not wired to the gateway")
	}
	runID := "r_cal" + strings.TrimPrefix(utils.NewID("x", 10), "x_")
	key, err := cfg.Gateway.MintKey(ctx, &schemas.RunContext{
		Run:   &schemas.Run{RunID: runID, ExpID: calibrationExpID},
		Agent: &schemas.AgentConfig{ID: VisualType + "_judge", Model: plan.Spec.Model, Gateway: schemas.GatewayPolicy{Provider: plan.Spec.Provider}},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("mint the vision judge's calibration key: %w", err)
	}
	defer (&judge{cfg: cfg}).revoke(ctx, key)
	spend := &CalibrationSpend{}
	served := map[string]bool{}
	var usd float64
	priced, unpriced := true, ""
	cast := func(msg visionMessage) ([]string, error) {
		votes := make([]string, 0, plan.Spec.Votes)
		for i := 0; i < plan.Spec.Votes; i++ {
			reply, tok, model, err := g.visionCall(ctx, key, &plan.Spec, msg)
			if err != nil {
				return nil, err
			}
			spend.Calls++
			if model != "" {
				served[model] = true
			}
			switch {
			case tok == nil:
				priced, unpriced = false, "a vision judge response carried no usage"
			case cfg.Price == nil:
				priced, unpriced = false, "no price snapshot wired to the judge"
			default:
				c, ok := cfg.Price(plan.Spec.Model, *tok)
				if !ok {
					priced, unpriced = false, fmt.Sprintf("vision judge model %s is not in the price snapshot", plan.Spec.Model)
				}
				usd += c
			}
			v, _, _ := ParseVisualReply(reply)
			votes = append(votes, textVotes([]string{v})[0])
		}
		return votes, nil
	}
	out := make([]calibration.ItemRun, 0, len(plan.Items))
	for n, it := range plan.Items {
		r := calibration.ItemRun{Item: it.Item, Category: it.Item.Item.Task, Template: plan.Spec.PromptVersion}
		if r.Votes, err = cast(it.Msg); err != nil {
			return nil, nil, fmt.Errorf("item %s: %w", it.Item.Item.ID, err)
		}
		if r.Repeat, err = cast(it.Msg); err != nil {
			return nil, nil, fmt.Errorf("item %s repeat: %w", it.Item.Item.ID, err)
		}
		out = append(out, r)
		if progress != nil {
			progress(n+1, len(plan.Items))
		}
	}
	spend.USD = schemas.Observed(usd, schemas.EvidenceGateway)
	if !priced {
		spend.USD = schemas.Unknown(unpriced, schemas.EvidenceGateway)
	}
	for m := range served {
		spend.ServedModels = append(spend.ServedModels, m)
	}
	sort.Strings(spend.ServedModels)
	return out, spend, nil
}

// VisualCalibrationRecord assembles the record of a vision calibration:
// templates are the vision prompt's, so a grade's JudgeRecord (model and
// the template's sha256) is covered by it. The caller writes it with
// calibration.WriteRecord.
func VisualCalibrationRecord(s *schemas.Suite, plan *VisualCalibrationPlan, set *calibration.Set, runs []calibration.ItemRun, spend *CalibrationSpend, disputed int, priceRef string) *schemas.CalibrationRecord {
	rec := &schemas.CalibrationRecord{
		Suite: s.ID, SuiteVersion: s.Version, Provider: plan.Spec.Provider, Model: plan.Spec.Model, PromptVersion: plan.Spec.PromptVersion,
		PromptSHA256: calibration.PromptHash(plan.Templates), Templates: plan.Templates, Votes: plan.Spec.Votes,
		ServedModels: spend.ServedModels, SetSHA256: set.SHA256, SetRows: len(set.Items), Calls: spend.Calls, USD: spend.USD, PriceRef: priceRef,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
	calibration.Build(rec, runs, disputed)
	return rec
}
