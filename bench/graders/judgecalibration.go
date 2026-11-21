package graders

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// W8 judge calibration (docs/judge-calibration.md): the pinned judge run on
// a suite's labelled calibration items, through the gateway on the judge's
// own key, exactly as it grades a run, plus the optional auditor.

// calibrationExpID is the experiment id the calibration's judge key is
// minted under; it is not an experiment and no run folder is written.
const calibrationExpID = "calibration"

// tokensPerRune is the forecast's rough tokens per character of prompt.
const tokensPerRune = 0.25

// PromptTemplates returns every template of a shipped prompt version with
// the sha256 of its text (the hash a judged grade records as
// prompt_sha256).
func PromptTemplates(version string) (map[string]string, error) {
	if !knownPromptVersion(version) {
		return nil, fmt.Errorf("prompt version %q is not shipped", version)
	}
	entries, err := judgePromptFS.ReadDir("judgeprompts")
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".txt")
		if !strings.HasPrefix(name, version+"-") {
			continue
		}
		b, err := judgePromptFS.ReadFile("judgeprompts/" + e.Name())
		if err != nil {
			return nil, err
		}
		out[name] = utils.SHA256Bytes(b)
	}
	return out, nil
}

// CalibrationPlan is what a calibration will ask the judge: one prompt per
// item and perturbation, the number of calls and a cost forecast. Items
// whose task is not in the suite, or has no usable answer spec, are
// skipped and named.
type CalibrationPlan struct {
	Spec        schemas.JudgeSpec
	Templates   map[string]string // every template of the prompt version -> sha256
	Items       []PlannedItem
	Skipped     []string
	Calls       int
	InputTokens int64
	OutTokens   int64
	USD         float64
	Priced      bool
	PriceNote   string
}

// PlannedItem is one labelled item with its rendered prompts.
type PlannedItem struct {
	Item      calibration.Labelled
	Category  string
	Template  string
	Prompt    string
	Perturbed map[string]string // perturbation -> prompt
}

// PlanCalibration resolves each labelled item of suite s to its task's
// answer spec and the judge template the grader would use, renders the
// prompts, and forecasts calls, tokens and cost (price may be nil). It
// refuses a suite that is not judged or whose judge is not pinned.
func PlanCalibration(s *schemas.Suite, items []calibration.Labelled, price func(string, schemas.Tokens) (float64, bool)) (*CalibrationPlan, error) {
	spec, err := pinnedJudge(s)
	if err != nil {
		return nil, err
	}
	tmpls, err := PromptTemplates(spec.PromptVersion)
	if err != nil {
		return nil, err
	}
	p := &CalibrationPlan{Spec: spec, Templates: tmpls, Priced: price != nil}
	if price == nil {
		p.PriceNote = "no price snapshot"
	}
	maxTok := spec.MaxTokens
	if maxTok == 0 {
		maxTok = defaultJudgeTokens
	}
	// Adversarial controls (judgeguard.go): per task, an answer that tells
	// the judge to reply correct and a bare refusal, both labelled incorrect.
	var known []calibration.Labelled
	for _, it := range items {
		if s.TaskByID(it.Item.Task) != nil {
			known = append(known, it)
		}
	}
	items = append(append([]calibration.Labelled(nil), items...), AdversarialControls(known, func(task string) bool {
		t := s.TaskByID(task)
		if t == nil {
			return false
		}
		ans, err := LoadAnswerSpec(t)
		return err == nil && answerNonAnswer(ans, adversarialRefusalText) != ""
	})...)
	for _, it := range items {
		t := s.TaskByID(it.Item.Task)
		if t == nil {
			p.Skipped = append(p.Skipped, fmt.Sprintf("item %s: task %s is not in suite %s", it.Item.ID, it.Item.Task, s.ID))
			continue
		}
		ans, err := LoadAnswerSpec(t)
		if err != nil {
			p.Skipped = append(p.Skipped, fmt.Sprintf("item %s: task %s: answer spec: %v", it.Item.ID, t.ID, err))
			continue
		}
		name, tmpl, err := judgeTemplate(spec.PromptVersion, ans)
		if err != nil {
			p.Skipped = append(p.Skipped, fmt.Sprintf("item %s: task %s: %v", it.Item.ID, t.ID, err))
			continue
		}
		pi := PlannedItem{Item: it, Category: ans.Category, Template: name, Prompt: renderJudgePrompt(tmpl, ans, it.Item.Answer), Perturbed: map[string]string{}}
		prompts := []string{pi.Prompt, pi.Prompt}
		for _, name := range calibration.Perturbations {
			pi.Perturbed[name] = renderJudgePrompt(tmpl, ans, calibration.Perturb(name, it.Item.Answer))
			prompts = append(prompts, pi.Perturbed[name])
		}
		for _, pr := range prompts {
			in := int64(float64(utf8.RuneCountInString(pr))*tokensPerRune) + 1
			p.Calls += spec.Votes
			p.InputTokens += in * int64(spec.Votes)
			p.OutTokens += int64(maxTok) * int64(spec.Votes)
			if price != nil {
				v, ok := price(spec.Model, schemas.Tokens{Fresh: in, Out: int64(maxTok)})
				if !ok {
					p.Priced, p.PriceNote = false, fmt.Sprintf("judge model %s is not in the price snapshot", spec.Model)
				}
				p.USD += v * float64(spec.Votes)
			}
		}
		p.Items = append(p.Items, pi)
	}
	return p, nil
}

// pinnedJudge returns a judged suite's judge spec with defaults, or why it
// cannot be calibrated.
func pinnedJudge(s *schemas.Suite) (schemas.JudgeSpec, error) {
	switch {
	case s == nil:
		return schemas.JudgeSpec{}, errors.New("no suite")
	case utils.Grading(s) != schemas.GradingJudged || s.Judge == nil:
		return schemas.JudgeSpec{}, fmt.Errorf("suite %s is not judged; only a judged suite's judge is calibrated", s.ID)
	}
	spec := *s.Judge
	if spec.Votes == 0 {
		spec.Votes = defaultJudgeVotes
	}
	switch {
	case spec.Provider == "" || spec.Model == "" || spec.PromptVersion == "":
		return spec, errors.New("judge spec needs provider, model and prompt_version")
	case spec.Provider == judgePlaceholder || spec.Model == judgePlaceholder:
		return spec, fmt.Errorf("judge provider and model are still %s: pin them in suite.yaml", judgePlaceholder)
	case spec.Votes < 0 || spec.Votes%2 == 0 || spec.Votes > maxJudgeVotes:
		return spec, fmt.Errorf("judge votes %d: want an odd number from 1 to %d", spec.Votes, maxJudgeVotes)
	}
	return spec, nil
}

// CalibrationSpend is what a calibration's calls cost and which model ids
// the gateway reported.
type CalibrationSpend struct {
	Calls        int
	USD          schemas.Value
	ServedModels []string
}

// RunCalibration casts every vote of the plan on one judge key minted for
// the calibration (never a run's), revoked at the end also on failure: the
// first pass, a second identical pass, and one pass per perturbation, each
// with the suite's number of votes at temperature 0. A failed call stops
// the calibration with an error: a partial calibration is not written.
// progress, when set, is told after each item.
func RunCalibration(ctx context.Context, cfg JudgeConfig, plan *CalibrationPlan, progress func(done, total int)) ([]calibration.ItemRun, *CalibrationSpend, error) {
	g := newJudge(cfg)
	if g.cfg.Gateway == nil || g.cfg.URL == "" {
		return nil, nil, errors.New("judge is not wired to the gateway")
	}
	runID := "r_cal" + strings.TrimPrefix(utils.NewID("x", 10), "x_")
	expID := calibrationExpID
	if cfg.CalibrationExpID != "" {
		expID = cfg.CalibrationExpID
	}
	key, err := g.cfg.Gateway.MintKey(ctx, &schemas.RunContext{
		Run:   &schemas.Run{RunID: runID, ExpID: expID},
		Agent: &schemas.AgentConfig{ID: JudgeType, Model: plan.Spec.Model, Gateway: schemas.GatewayPolicy{Provider: plan.Spec.Provider}},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("mint the judge's calibration key: %w", err)
	}
	defer g.revoke(ctx, key)
	spend := &CalibrationSpend{}
	served := map[string]bool{}
	var usd float64
	priced, unpriced := true, ""
	cast := func(prompt string) ([]string, error) {
		votes := make([]string, 0, plan.Spec.Votes)
		for i := 0; i < plan.Spec.Votes; i++ {
			v, tok, model, err := g.call(ctx, key, plan.Spec, prompt)
			if err != nil {
				return nil, err
			}
			spend.Calls++
			if model != "" {
				served[model] = true
			}
			switch {
			case tok == nil:
				priced, unpriced = false, "a judge response carried no usage"
			case cfg.Price == nil:
				priced, unpriced = false, "no price snapshot wired to the judge"
			default:
				c, ok := cfg.Price(plan.Spec.Model, *tok)
				if !ok {
					priced, unpriced = false, fmt.Sprintf("judge model %s is not in the price snapshot", plan.Spec.Model)
				}
				usd += c
			}
			votes = append(votes, v)
		}
		return votes, nil
	}
	out := make([]calibration.ItemRun, 0, len(plan.Items))
	for n, it := range plan.Items {
		r := calibration.ItemRun{Item: it.Item, Category: it.Category, Template: it.Template, Perturbed: map[string][]string{}}
		if r.Votes, err = cast(it.Prompt); err != nil {
			return nil, nil, fmt.Errorf("item %s: %w", it.Item.Item.ID, err)
		}
		if r.Repeat, err = cast(it.Prompt); err != nil {
			return nil, nil, fmt.Errorf("item %s repeat: %w", it.Item.Item.ID, err)
		}
		for _, name := range calibration.Perturbations {
			if r.Perturbed[name], err = cast(it.Perturbed[name]); err != nil {
				return nil, nil, fmt.Errorf("item %s %s: %w", it.Item.Item.ID, name, err)
			}
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

// calibrationRef names the newest valid calibration record of the suite
// that covers the judge model and template a grade used, "" when none.
// It reads files only; a suite without a calibration folder costs a stat.
func calibrationRef(s *schemas.Suite, id calibration.JudgeID, promptSHA string) string {
	if s == nil || s.Root == "" || promptSHA == "" {
		return ""
	}
	recs, err := calibration.ReadRecords(s.Root)
	if err != nil {
		return ""
	}
	rec, _ := recs.Covering(id, []string{promptSHA})
	return calibration.Ref(rec)
}

// checkAuditor validates a judge spec's auditor: another provider than the
// judge's, a model, and an odd number of votes.
func checkAuditor(spec schemas.JudgeSpec) error {
	a := spec.Auditor
	if a == nil {
		return nil
	}
	switch {
	case a.Provider == "" || a.Model == "":
		return errors.New("judge auditor needs provider and model")
	case a.Provider == spec.Provider:
		return fmt.Errorf("judge auditor provider %s is the judge's own; the auditor must come from another vendor", a.Provider)
	case a.Votes < 0 || (a.Votes != 0 && a.Votes%2 == 0) || a.Votes > maxJudgeVotes:
		return fmt.Errorf("judge auditor votes %d: want an odd number from 1 to %d", a.Votes, maxJudgeVotes)
	}
	return nil
}

// audit asks the auditor the same prompt on its own key and records
// whether its majority agrees with the judge's. It never changes the grade;
// a failed audit is recorded with its error and the grade stands.
func (g *judge) audit(ctx context.Context, rc *schemas.RunContext, spec schemas.JudgeSpec, prompt string, judgeVotes []string) *schemas.JudgeAudit {
	a := spec.Auditor
	aspec := schemas.JudgeSpec{Provider: a.Provider, Model: a.Model, PromptVersion: spec.PromptVersion, Votes: a.Votes, MaxTokens: spec.MaxTokens}
	if aspec.Votes == 0 {
		aspec.Votes = 1
	}
	out := &schemas.JudgeAudit{Provider: a.Provider, Model: a.Model}
	rec, err := g.vote(ctx, rc, aspec, prompt)
	if err != nil {
		out.Error = err.Error()
		out.USD = schemas.Unknown("the audit did not complete", schemas.EvidenceGateway)
		return out
	}
	out.Votes, out.USD, out.ServedModel = rec.Votes, rec.USD, rec.ServedModel
	out.Verdict = calibration.Majority(rec.Votes)
	out.Agrees = out.Verdict == calibration.Majority(judgeVotes)
	return out
}

// StatusJudge resolves the judge that decides suite s's grades for `judge
// status`: the suite's text judge, with the templates its tasks can be
// graded with, or else the vision judge its task rubrics pin, as visual
// calibrate does (QC2-25-06, QC2-25-09). Nil when neither exists.
func StatusJudge(s *schemas.Suite) *calibration.StatusJudge {
	if s == nil {
		return nil
	}
	if j := s.Judge; j != nil {
		sj := &calibration.StatusJudge{ID: calibration.JudgeID{Provider: j.Provider, Model: j.Model, Votes: max(j.Votes, 1)}, PromptVersion: j.PromptVersion}
		sj.Templates, _ = PromptTemplates(j.PromptVersion)
		used := map[string]bool{}
		for _, t := range s.Tasks {
			ans, err := LoadAnswerSpec(t)
			if err != nil {
				continue
			}
			name, _, err := judgeTemplate(j.PromptVersion, ans)
			if err != nil {
				continue
			}
			if h, ok := sj.Templates[name]; ok {
				used[h] = true
			}
		}
		for h := range used {
			sj.Used = append(sj.Used, h)
		}
		sort.Strings(sj.Used)
		return sj
	}
	for _, t := range s.Tasks {
		if !VisualJudged(s, t) {
			continue
		}
		v := VisualSpec(s, t).Judge
		_, sha, err := VisualPrompt(v.PromptVersion)
		if err != nil {
			return nil
		}
		return &calibration.StatusJudge{ID: visualJudgeID(v), PromptVersion: v.PromptVersion,
			Templates: map[string]string{v.PromptVersion: sha}, Used: []string{sha}}
	}
	return nil
}
