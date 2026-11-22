package canary

import (
	"math"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestDefinitionProblemsAllAtOnce: the website form gets every problem of an
// empty definition in one answer, in plain words and under its field, never
// the YAML key names Validate uses for the CLI.
func TestDefinitionProblemsAllAtOnce(t *testing.T) {
	d := &Definition{Name: "Bad Name!", Trials: 1}
	got := map[string]string{}
	for _, p := range d.Problems() {
		if _, dup := got[p.Field]; dup {
			t.Errorf("field %s listed twice: %+v", p.Field, d.Problems())
		}
		got[p.Field] = p.Message
	}
	want := map[string]string{
		"name":    "Use lowercase letters, digits, - or _ for the name, at most 48 characters.",
		"agent":   "Pick an agent.",
		"suite":   "Pick a benchmark.",
		"tasks":   "Pick the tasks to check, or choose all tasks.",
		"targets": "Add at least one model to watch.",
		"budget":  "Set both spending limits, or tick Practice mode.",
	}
	for f, msg := range want {
		if got[f] != msg {
			t.Errorf("field %s: got %q, want %q", f, got[f], msg)
		}
	}
	for f, msg := range got {
		for _, yamlWord := range []string{"per_run_usd", "per_day_usd", "targets is", "tasks is", "smoke canary", "{provider"} {
			if strings.Contains(msg, yamlWord) {
				t.Errorf("field %s message %q uses the YAML word %q", f, msg, yamlWord)
			}
		}
	}

	more := &Definition{Name: "ok", Agent: "agents/a.yaml", Suite: "s@1", Tasks: schemas.TaskSelector{All: true}, Trials: 1,
		Targets:  []Target{{Provider: "openai", Model: "gpt"}, {Provider: "openai", Model: "gpt"}},
		Schedule: &ScheduleSpec{Cron: "not a cron"},
		Budget:   Budget{PerRunUSD: 5, PerDayUSD: 1}}
	msgs := []string{}
	for _, p := range more.Problems() {
		msgs = append(msgs, p.Field+": "+p.Message)
	}
	joined := strings.Join(msgs, "\n")
	for _, w := range []string{
		"targets: openai/gpt is listed twice.",
		"schedule: The schedule is not a valid cron line: ",
		"budget: The limit per check is above the limit per day, so no check could start.",
	} {
		if !strings.Contains(joined, w) {
			t.Errorf("missing %q in\n%s", w, joined)
		}
	}

	// A smoke definition needs no limits; a clean one has no problems and
	// passes Validate too.
	okDef := &Definition{Name: "ok", Agent: "agents/a.yaml", Suite: "s@1", Tasks: schemas.TaskSelector{IDs: []string{"t1"}}, Trials: 1,
		Targets: []Target{{Model: "gpt"}}, Smoke: true}
	if p := okDef.Problems(); len(p) != 0 {
		t.Errorf("clean smoke definition: %+v", p)
	}
	if err := okDef.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

// TestDefinitionProblemsCoverValidate: every definition Validate refuses has
// at least one problem, so the website never accepts what the CLI refuses.
func TestDefinitionProblemsCoverValidate(t *testing.T) {
	base := func() *Definition {
		return &Definition{Name: "ok", Agent: "agents/a.yaml", Suite: "s@1", Tasks: schemas.TaskSelector{All: true}, Trials: 1,
			Targets: []Target{{Model: "gpt"}}, Budget: Budget{PerRunUSD: 1, PerDayUSD: 2}}
	}
	cases := map[string]func(d *Definition){
		"tier without all": func(d *Definition) { d.Tasks = schemas.TaskSelector{IDs: []string{"a"}}; d.Tier = "fast" },
		"trials":           func(d *Definition) { d.Trials = 0 },
		"condition twice":  func(d *Definition) { d.Conditions = []string{"clean", "clean"} },
		"too many targets": func(d *Definition) {
			d.Targets = make([]Target, 7)
			for i := range d.Targets {
				d.Targets[i].Model = string(rune('a' + i))
			}
		},
		"target no model": func(d *Definition) { d.Targets = []Target{{Provider: "openai"}} },
		"timezone":        func(d *Definition) { d.Schedule = &ScheduleSpec{Cron: "0 6 * * *", Timezone: "Mars/Base"} },
		"negative":        func(d *Definition) { d.Budget.PerRunUSD = -1 },
		"not finite":      func(d *Definition) { d.Budget.PerDayUSD = math.Inf(1) },
		"sensitivity":     func(d *Definition) { d.Sensitivity = "extreme" },
		"noise floor":     func(d *Definition) { d.NoiseFloor = &NoiseFloor{Success: 2} },
		"baseline age":    func(d *Definition) { d.MaxBaselineAgeDays = -1 },
		"concurrency":     func(d *Definition) { d.Concurrency = -1 },
	}
	for name, mut := range cases {
		d := base()
		mut(d)
		if d.Validate() == nil {
			t.Errorf("%s: Validate accepts it; the case is wrong", name)
			continue
		}
		if len(d.Problems()) == 0 {
			t.Errorf("%s: Validate refuses it but Problems lists nothing", name)
		}
	}
	if p := base().Problems(); len(p) != 0 {
		t.Errorf("base: %+v", p)
	}
}
