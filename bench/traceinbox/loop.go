package traceinbox

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/schemas"
)

// IncidentTask is a task drafted from an inbox item.
type IncidentTask struct {
	Suite    string
	Task     string
	Incident schemas.TaskIncident
}

// Key is suite/task.
func (t IncidentTask) Key() string { return t.Suite + "/" + t.Task }

// IncidentTasks reads every task.yaml under the benchmarks folder and
// returns the tasks with provenance.incident, by suite/task. It reads only
// the provenance, so a task that does not load for other reasons still
// counts.
func IncidentTasks(benchmarksRoot string) (map[string]IncidentTask, error) {
	out := map[string]IncidentTask{}
	if benchmarksRoot == "" {
		return out, nil
	}
	files, err := filepath.Glob(filepath.Join(benchmarksRoot, "*", "tasks", "*", "task.yaml"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		data, err := readCapped(f, 4<<20)
		if err != nil {
			continue
		}
		var head struct {
			Provenance struct {
				Incident *schemas.TaskIncident `yaml:"incident"`
			} `yaml:"provenance"`
		}
		if yaml.Unmarshal(data, &head) != nil || head.Provenance.Incident == nil || head.Provenance.Incident.ID == "" {
			continue
		}
		taskDir := filepath.Dir(f)
		suite := filepath.Base(filepath.Dir(filepath.Dir(taskDir)))
		t := IncidentTask{Suite: suite, Task: filepath.Base(taskDir), Incident: *head.Provenance.Incident}
		out[t.Key()] = t
	}
	return out, nil
}

// incidentRuns counts one task's runs in one arm.
type incidentRuns struct {
	failed, passed int
}

// IncidentSection is the report section that closes the loop: how many of
// the experiment's tasks came from production incidents, and for each such
// task that failed a run, "regression of incident X from <date>". A run with
// no grade, or a grade the evaluator could not decide, counts as neither.
// It returns "" when no task of the experiment came from an incident.
func IncidentSection(runs []schemas.SealedRun, incidents map[string]IncidentTask) string {
	if len(incidents) == 0 || len(runs) == 0 {
		return ""
	}
	tasks := map[string]bool{}
	byTask := map[string]map[string]*incidentRuns{} // suite/task -> arm -> counts
	for _, r := range runs {
		suite := r.Slot.Suite
		if i := strings.IndexByte(suite, '@'); i >= 0 {
			suite = suite[:i]
		}
		key := suite + "/" + r.Slot.Task
		tasks[key] = true
		if _, ok := incidents[key]; !ok || r.Grade == nil || r.Grade.InvalidEvaluator {
			continue
		}
		if byTask[key] == nil {
			byTask[key] = map[string]*incidentRuns{}
		}
		c := byTask[key][r.Slot.Arm]
		if c == nil {
			c = &incidentRuns{}
			byTask[key][r.Slot.Arm] = c
		}
		if r.Grade.Passed {
			c.passed++
		} else {
			c.failed++
		}
	}
	var fromIncidents []string
	for key := range tasks {
		if _, ok := incidents[key]; ok {
			fromIncidents = append(fromIncidents, key)
		}
	}
	if len(fromIncidents) == 0 {
		return ""
	}
	sort.Strings(fromIncidents)
	var regressions, holding []string
	for _, key := range fromIncidents {
		inc := incidents[key].Incident
		arms := byTask[key]
		var failedArms []string
		total := 0
		for arm, c := range arms {
			total += c.passed + c.failed
			if c.failed > 0 {
				failedArms = append(failedArms, fmt.Sprintf("%d of %d runs in arm %s", c.failed, c.passed+c.failed, arm))
			}
		}
		sort.Strings(failedArms)
		where := incidentWhere(inc)
		switch {
		case len(failedArms) > 0:
			regressions = append(regressions, fmt.Sprintf("- Regression of incident %s from %s%s: task %s failed %s.",
				inc.ID, incidentDate(inc.FirstSeen), where, key, strings.Join(failedArms, ", ")))
		case total > 0:
			holding = append(holding, fmt.Sprintf("- Incident %s from %s: task %s passed every graded run.", inc.ID, incidentDate(inc.FirstSeen), key))
		default:
			holding = append(holding, fmt.Sprintf("- Incident %s from %s: task %s has no graded run.", inc.ID, incidentDate(inc.FirstSeen), key))
		}
	}
	var b strings.Builder
	b.WriteString("## Production incidents\n\n")
	fmt.Fprintf(&b, "%d of %d tasks in this experiment came from production incidents (failure inbox, docs/failure-inbox.md); %d failed a run.\n\n",
		len(fromIncidents), len(tasks), len(regressions))
	for _, l := range regressions {
		b.WriteString(l + "\n")
	}
	for _, l := range holding {
		b.WriteString(l + "\n")
	}
	return b.String()
}

func incidentDate(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.DateOnly)
	}
	if s == "" {
		return "an unknown date"
	}
	return s
}

func incidentWhere(inc schemas.TaskIncident) string {
	var parts []string
	if inc.Source != "" && inc.TraceID != "" {
		parts = append(parts, inc.Source+" trace "+inc.TraceID)
	}
	if inc.Link != "" {
		parts = append(parts, inc.Link)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// RegressionCount is the number of tasks under the benchmarks folder that
// came from production incidents, for "N tasks came from production
// incidents".
func RegressionCount(benchmarksRoot string) int {
	m, err := IncidentTasks(benchmarksRoot)
	if err != nil {
		return 0
	}
	return len(m)
}
