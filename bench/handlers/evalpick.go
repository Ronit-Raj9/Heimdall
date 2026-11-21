package handlers

// One task picker for evaluations: the estimate forecasts exactly the tasks
// the launch runs (PROD_PROD3 N8). Both read admission from suite.lock only,
// so the pick needs no task file and works on a suite head as well as on a
// full load.

import (
	"github.com/maximhq/bifrost/bench/schemas"
)

// evalPickTasks is the n tasks an evaluation runs on st in the given split:
// n tasks spread evenly over the sorted pick of admittedTasks (admitted, of
// the split, not quarantined). Empty when st admits none.
func evalPickTasks(st *schemas.Suite, split string, n int) []string {
	return spreadPick(admittedTasks(st, split).IDs, n)
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
