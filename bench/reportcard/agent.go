package reportcard

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Evaluation statuses the report card reads.
const (
	StatusDone    = "done"
	StatusRunning = "running"
)

var agentRefVersion = regexp.MustCompile(`@v([0-9]+)$`)

// AgentRef splits an arm's agent reference ("codex", "agents/codex.yaml",
// "agents/codex.yaml@v2") into the agent id and its pinned version (0: none).
func AgentRef(ref string) (id string, version int) {
	ref = strings.TrimSpace(ref)
	if m := agentRefVersion.FindStringSubmatch(ref); m != nil {
		version, _ = strconv.Atoi(m[1])
		ref = strings.TrimSuffix(ref, m[0])
	}
	ref = strings.TrimPrefix(ref, "agents/")
	ref = strings.TrimSuffix(strings.TrimSuffix(ref, ".yaml"), ".yml")
	return ref, version
}

// ArmMatches reports whether arm runs agent id, at version (0: any) with
// model ("": any).
func ArmMatches(arm schemas.EvaluationArm, agent string, version int, model string) bool {
	id, v := AgentRef(arm.Agent)
	if arm.Version != 0 {
		v = arm.Version
	}
	return id == agent && (version == 0 || v == version) && (model == "" || arm.Model == model)
}

// SelectForAgent picks the evaluation an agent's report card shows: the
// newest done evaluation with an arm that matches, else the newest running
// one with at least one child experiment. It returns the evaluation and the
// labels of its matching arms, or nil when none matches.
func SelectForAgent(evals []schemas.Evaluation, agent string, version int, model string) (*schemas.Evaluation, []string) {
	sorted := slices.Clone(evals)
	slices.SortStableFunc(sorted, func(a, b schemas.Evaluation) int { return b.CreatedAt.Compare(a.CreatedAt) })
	pick := func(ok func(schemas.Evaluation) bool) (*schemas.Evaluation, []string) {
		for i := range sorted {
			if !ok(sorted[i]) {
				continue
			}
			var labels []string
			for _, a := range sorted[i].Spec.Arms {
				if ArmMatches(a, agent, version, model) {
					labels = append(labels, a.Label)
				}
			}
			if len(labels) > 0 {
				return &sorted[i], labels
			}
		}
		return nil, nil
	}
	if ev, labels := pick(func(e schemas.Evaluation) bool { return e.Status == StatusDone }); ev != nil {
		return ev, labels
	}
	return pick(func(e schemas.Evaluation) bool {
		return e.Status == StatusRunning && slices.ContainsFunc(e.Children, func(c schemas.EvaluationChild) bool { return c.ExperimentID != "" })
	})
}

// matchingLabels lists the labels of ev's arms that run agent at version
// (0: any) with model ("": any).
func matchingLabels(ev schemas.Evaluation, agent string, version int, model string) []string {
	var labels []string
	for _, a := range ev.Spec.Arms {
		if ArmMatches(a, agent, version, model) {
			labels = append(labels, a.Label)
		}
	}
	return labels
}

// SelectForAgentEvaluation is SelectForAgent with the evaluation the page
// asked for (?evaluation=): that evaluation when it is done, or running
// with a child, and has an arm that matches; otherwise the evaluation
// SelectForAgent picks (fix wave 3, N206).
func SelectForAgentEvaluation(evals []schemas.Evaluation, agent string, version int, model, evaluationID string) (*schemas.Evaluation, []string) {
	if evaluationID != "" {
		for i := range evals {
			ev := evals[i]
			if ev.ID != evaluationID {
				continue
			}
			usable := ev.Status == StatusDone || (ev.Status == StatusRunning &&
				slices.ContainsFunc(ev.Children, func(c schemas.EvaluationChild) bool { return c.ExperimentID != "" }))
			if labels := matchingLabels(ev, agent, version, model); usable && len(labels) > 0 {
				return &evals[i], labels
			}
			break
		}
	}
	return SelectForAgent(evals, agent, version, model)
}

// MatchingEvaluations lists the done evaluations with an arm that runs
// agent at version (0: any) with model ("": any), newest first: the
// choices of the agent card's evaluation picker (fix wave 3, N206).
func MatchingEvaluations(evals []schemas.Evaluation, agent string, version int, model string) []schemas.Evaluation {
	var out []schemas.Evaluation
	for _, ev := range evals {
		if ev.Status == StatusDone && len(matchingLabels(ev, agent, version, model)) > 0 {
			out = append(out, ev)
		}
	}
	slices.SortStableFunc(out, func(a, b schemas.Evaluation) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out
}

// ModelsForAgent lists the models an agent's report card can be filtered
// by: the distinct non-empty models of every arm that runs agent (at
// version, 0: any) in a done or running evaluation, sorted.
func ModelsForAgent(evals []schemas.Evaluation, agent string, version int) []string {
	var out []string
	for _, ev := range evals {
		if ev.Status != StatusDone && ev.Status != StatusRunning {
			continue
		}
		for _, a := range ev.Spec.Arms {
			if a.Model != "" && ArmMatches(a, agent, version, "") && !slices.Contains(out, a.Model) {
				out = append(out, a.Model)
			}
		}
	}
	slices.Sort(out)
	return out
}

// MapArms maps a child experiment's arm ids to the evaluation's arm labels,
// the same way the report card reads the child (readChild). Callers skip the
// "compat" arm themselves.
func MapArms(arms []schemas.EvaluationArm, m *schemas.Manifest) map[string]string {
	return mapArms(arms, m)
}
