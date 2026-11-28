package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/compat"
	"github.com/maximhq/bifrost/bench/gwfeature"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Feature test form checks (round 4, lane F4): the task count, ready tasks
// only, a name, and one compatibility rule for the agent and the benchmark
// (compat.Full, the rule the agent page and the evaluation wizard use).

const (
	gatewayDraftMaxTasks = 200
	gatewayDraftMaxName  = 120
)

// draftTasks checks the name, the task count and the agent against the
// benchmark, and fills req.Tasks with ready tasks when the request names
// none. It answers the notes to show, in plain words, and the number of
// tasks the test runs (0 when it cannot be known here).
func (h *gatewayTestsHandler) draftTasks(ctx context.Context, req *gwfeature.DraftRequest) ([]string, int, error) {
	notes := []string{}
	if len([]rune(strings.TrimSpace(req.Name))) > gatewayDraftMaxName {
		return nil, 0, fmt.Errorf("Keep the name to %d characters or fewer.", gatewayDraftMaxName) //nolint:staticcheck // ST1005: a full sentence shown to the user as is
	}
	if req.TaskCount < 0 || req.TaskCount > gatewayDraftMaxTasks {
		return nil, 0, fmt.Errorf("The number of tasks must be between 1 and %d.", gatewayDraftMaxTasks) //nolint:staticcheck // ST1005: a full sentence shown to the user as is
	}
	src := h.env.Services.Suites
	if src == nil {
		return notes, max(len(req.Tasks), req.TaskCount), nil
	}
	id, _, _ := strings.Cut(strings.TrimSpace(req.Suite), "@")
	s, err := src.LoadSuite(ctx, id)
	if err != nil || s == nil {
		return nil, 0, fmt.Errorf("The benchmark %s could not be read.", id) //nolint:staticcheck // ST1005: a full sentence shown to the user as is
	}
	if cfg := (&evaluationsHandler{env: h.env}).agentConfigs(ctx)[strings.TrimSpace(req.Base)]; cfg != nil {
		if ok, _, reason, _ := compat.Full(cfg, s); !ok {
			if reason == "" {
				reason = "it does not have what the benchmark needs"
			}
			reason = strings.TrimSuffix(reason, ".")
			if !strings.HasPrefix(reason, "agent ") {
				reason = fmt.Sprintf("agent %s cannot run this benchmark: %s", cfg.ID, reason)
			}
			return nil, 0, fmt.Errorf("The %s. Pick an agent that uses the benchmark's tools.", reason) //nolint:staticcheck // ST1005: a full sentence shown to the user as is
		}
	}
	if len(req.Tasks) > 0 {
		return notes, len(req.Tasks), nil
	}
	ready := readyTaskIDs(s)
	if len(ready) == 0 {
		return nil, 0, errors.New("No task of this benchmark is ready yet, so a test cannot start.") //nolint:staticcheck // ST1005: a full sentence shown to the user as is
	}
	total := len(s.TaskIDs)
	pick := ready
	if req.TaskCount > 0 && req.TaskCount < len(ready) {
		pick = ready[:req.TaskCount]
	}
	if len(pick) < total {
		req.Tasks = append([]string(nil), pick...)
		if len(ready) < total {
			notes = append(notes, fmt.Sprintf("Runs %d of the %d ready tasks; %d tasks of this benchmark are not ready and are left out.", len(pick), len(ready), total-len(ready)))
		} else {
			notes = append(notes, fmt.Sprintf("Runs %d of the %d tasks.", len(pick), total))
		}
	}
	return notes, len(pick), nil
}

// readyTaskIDs is the tasks of s the lock admitted, in suite order; every
// task when s has no lock (the plan still decides).
func readyTaskIDs(s *schemas.Suite) []string {
	if s.Lock == nil {
		return append([]string(nil), s.TaskIDs...)
	}
	var out []string
	for _, id := range s.TaskIDs {
		if tl, ok := s.Lock.Tasks[id]; ok && tl.Status == schemas.TaskAdmitted && tl.Admission != nil && tl.Admission.Admitted {
			out = append(out, id)
		}
	}
	return out
}
