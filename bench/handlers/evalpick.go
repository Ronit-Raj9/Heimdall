package handlers

// One task picker for evaluations: the estimate forecasts exactly the tasks
// the launch runs (PROD_PROD3 N8). Both read admission from suite.lock only,
// so the pick needs no task file and works on a suite head as well as on a
// full load.

import (
	"github.com/maximhq/bifrost/bench/compat"
	"github.com/maximhq/bifrost/bench/schemas"
)

// evalPickTasks is the n tasks an evaluation runs on st in the given split
// for the arm configs cfgs: n tasks spread evenly over the sorted pick of
// admittedTasks (admitted, of the split, not quarantined) that every arm
// can produce the output of (compat.RunnableTasks). Empty when none is
// left; why is then the first reason a task was refused, "" when st admits
// none. The launch (launchChild) picks the same way.
func evalPickTasks(st *schemas.Suite, split string, n int, cfgs []*schemas.AgentConfig) (ids []string, why string) {
	ids, why = runnableTasks(cfgs, st, admittedTasks(st, split).IDs)
	return spreadPick(ids, n), why
}

// runnableTasks keeps the ids every config in cfgs can run (nil configs are
// skipped) and the first refusal reason.
func runnableTasks(cfgs []*schemas.AgentConfig, st *schemas.Suite, ids []string) ([]string, string) {
	why := ""
	for _, cfg := range cfgs {
		if cfg == nil {
			continue
		}
		var w string
		if ids, w = compat.RunnableTasks(cfg, st, ids); why == "" {
			why = w
		}
	}
	return ids, why
}

// evalArmConfigs is the loaded agent config of each arm (nil when missing).
func evalArmConfigs(arms []schemas.EvaluationArm, agents map[string]*schemas.AgentConfig) []*schemas.AgentConfig {
	out := make([]*schemas.AgentConfig, len(arms))
	for i, a := range arms {
		out[i] = agents["agents/"+a.Agent+".yaml"]
	}
	return out
}

// suiteTaskIDs is every task id of st: the loaded tasks in order, else the
// ids suite.yaml or the tasks folder lists (a suite head has no Tasks).
func suiteTaskIDs(st *schemas.Suite) []string {
	if len(st.Tasks) == 0 {
		return append([]string(nil), st.TaskIDs...)
	}
	ids := make([]string, 0, len(st.Tasks))
	for _, t := range st.Tasks {
		if t != nil {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

// lockAdmits reports whether st's lock admits task id: status admitted with
// a passing admission record. A suite without a lock admits nothing here;
// admittedTasks treats an unlocked suite as every task.
func lockAdmits(st *schemas.Suite, id string) bool {
	if st.Lock == nil {
		return false
	}
	tl, ok := st.Lock.Tasks[id]
	return ok && tl.Status == schemas.TaskAdmitted && tl.Admission != nil && tl.Admission.Admitted
}
