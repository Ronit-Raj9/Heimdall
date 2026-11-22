package reference

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/schemas"
)

// ImportedBy is the CreatedBy of an evaluation made for imported runs.
const ImportedBy = "reference import"

// WrapID is the evaluation id given to an experiment imported on its own:
// ev_ref_<experiment id>, or a hash of the id when that is too long.
func WrapID(expID string) string {
	if id := "ev_ref_" + expID; evalstore.ValidID(id) {
		return id
	}
	sum := sha256.Sum256([]byte(expID))
	return "ev_ref_" + hex.EncodeToString(sum[:8])
}

// Wrap makes the evaluation record of one experiment that ran somewhere
// else (bifrost-bench run on a fleet machine) and was imported into the
// reference organization, so its report card and share link work like any
// evaluation's. It copies what the frozen manifest says (benchmark, agents,
// models) and adds nothing: the scores come from the sealed runs when the
// report card is built. One arm is a score; two arms are an A/B whose mode
// follows what differs. More than two arms are refused.
func Wrap(m *schemas.Manifest, orgID string, now time.Time) (*schemas.Evaluation, error) {
	if m == nil || m.ExpID == "" {
		return nil, errors.New("no manifest")
	}
	if m.SuiteID == "" {
		return nil, fmt.Errorf("experiment %s names no benchmark", m.ExpID)
	}
	ids := make([]string, 0, len(m.Arms))
	for id := range m.Arms {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 || len(ids) > 2 {
		return nil, fmt.Errorf("experiment %s has %d agents; only one or two can be imported as an evaluation", m.ExpID, len(ids))
	}
	var arms []schemas.EvaluationArm
	for _, id := range ids {
		a := m.Arms[id]
		label := a.Label
		if label == "" {
			label = id
		}
		agent := a.Config.ID
		if agent == "" {
			return nil, fmt.Errorf("experiment %s: agent %s has no id", m.ExpID, id)
		}
		arms = append(arms, schemas.EvaluationArm{Label: label, Agent: agent, Model: a.Config.Model})
	}
	mode := schemas.EvaluationModeScore
	if len(arms) == 2 {
		switch {
		case arms[0].Agent != arms[1].Agent:
			mode = schemas.EvaluationModeOpponent
		case arms[0].Model != arms[1].Model:
			mode = schemas.EvaluationModeModels
		default:
			mode = schemas.EvaluationModeVersions
		}
	}
	split := schemas.SplitPublic
	if len(m.HeldOutTasks) > 0 {
		split = schemas.SplitHeldOut
	}
	created := m.CreatedAt
	if created.IsZero() {
		created = now
	}
	names := make([]string, len(arms))
	for i, a := range arms {
		names[i] = a.Agent
		if a.Model != "" {
			names[i] += " on " + a.Model
		}
	}
	return &schemas.Evaluation{
		ID:  WrapID(m.ExpID),
		Org: orgID,
		Spec: schemas.EvaluationSpec{
			Name:       fmt.Sprintf("%s: %s", m.SuiteID, strings.Join(names, " vs ")),
			Mode:       mode,
			Arms:       arms,
			Benchmarks: []string{m.SuiteID},
			Faults:     []string{schemas.ConditionClean},
			Split:      split,
		},
		Status:    schemas.EvaluationDone,
		Children:  []schemas.EvaluationChild{{Benchmark: m.SuiteID, ExperimentID: m.ExpID, Status: schemas.EvaluationDone}},
		CreatedAt: created.UTC(),
		CreatedBy: ImportedBy,
	}, nil
}
