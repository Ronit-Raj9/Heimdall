package tau2

import (
	"encoding/json"
	"strings"
	"testing"
)

func fixtureCandidate(t *testing.T, domain, id string) (candidate, []upAction) {
	t.Helper()
	u, err := openUpstream(fixtureDir(t), fixtureRef)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := u.baseTasks(domain)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.ID == id {
			acts, err := task.Criteria.actions()
			if err != nil {
				t.Fatal(err)
			}
			return candidate{domain: domain, task: task, id: taskID(domain, id)}, acts
		}
	}
	t.Fatalf("no task %s", id)
	return candidate{}, nil
}

func TestControlPlans(t *testing.T) {
	writes := map[string]bool{"cancel_booking": true}
	cases := []struct {
		name       string
		id         string
		reference  []string
		mutant     []string
		wantMutant bool
	}{
		{name: "write and info", id: "a1",
			reference:  []string{"say", "get_booking", "cancel_booking", "say:250", "say"},
			mutant:     []string{"say", "get_booking", "say:250", "say"},
			wantMutant: true},
		{name: "info only", id: "a3",
			reference:  []string{"say", "get_booking", "say:7", "say"},
			mutant:     []string{"say", "get_booking", "say"},
			wantMutant: true},
		{name: "nothing to drop", id: "a2", reference: []string{"say", "say"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cand, acts := fixtureCandidate(t, "airline", c.id)
			plans := controlPlans(cand, acts, writes)
			if got := shape(plans["reference"]); strings.Join(got, ",") != strings.Join(c.reference, ",") {
				t.Fatalf("reference = %v, want %v", got, c.reference)
			}
			m, ok := plans["mutant1"]
			if ok != c.wantMutant {
				t.Fatalf("mutant present = %v, want %v", ok, c.wantMutant)
			}
			if ok && strings.Join(shape(m), ",") != strings.Join(c.mutant, ",") {
				t.Fatalf("mutant = %v, want %v", shape(m), c.mutant)
			}
			if e := plans["exploit"]; len(e) != 3 || !e[1].EchoUser {
				t.Fatalf("exploit = %+v", e)
			}
		})
	}
}

// shape names each step: say, say:<info> when the message carries the
// required information, or the tool called.
func shape(steps []controlStep) []string {
	var out []string
	for _, s := range steps {
		switch {
		case s.Say != "" && strings.HasPrefix(s.Say, "Here is the information"):
			out = append(out, "say:"+strings.TrimSuffix(strings.TrimPrefix(s.Say, "Here is the information you need: "), "."))
		case s.Say != "":
			out = append(out, "say")
		case s.EchoUser:
			out = append(out, "echo")
		default:
			out = append(out, s.Call)
		}
	}
	return out
}

func TestControlShell(t *testing.T) {
	cand, acts := fixtureCandidate(t, "airline", "a1")
	plans := controlPlans(cand, acts, map[string]bool{"cancel_booking": true})
	sh, err := controlShell("reference", cand, plans["reference"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sh, "#!/bin/sh\n") || !strings.Contains(sh, "<<'PY'\n") || !strings.Contains(sh, "\nPY\n") {
		t.Fatalf("not a self-contained shell control:\n%s", sh)
	}
	// The plan is the one line between the JSON markers.
	_, after, ok := strings.Cut(sh, "<<'JSON'\n")
	if !ok {
		t.Fatal("no plan here-document")
	}
	line, rest, _ := strings.Cut(after, "\n")
	if rest != "JSON\n" {
		t.Fatalf("plan is not one line: %q", after)
	}
	var plan struct {
		Server string        `json:"server"`
		Steps  []controlStep `json:"steps"`
	}
	if err := json.Unmarshal([]byte(line), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Server != "tau2" || len(plan.Steps) != 5 || plan.Steps[2].Call != "cancel_booking" || string(plan.Steps[2].Arguments) != `{"booking_id":"BK1"}` {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestInstruction(t *testing.T) {
	got := instruction("retail", "# Policy\n\nBe kind.\n\n")
	for _, want := range []string{"# Customer service: retail", "`respond_to_user`", "`###TRANSFER###`", "## Policy\n\n# Policy\n\nBe kind.\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("instruction lacks %q:\n%s", want, got)
		}
	}
	for _, leak := range []string{"hidden test", "grader", "agents.md", "claude.md"} {
		if strings.Contains(strings.ToLower(got), leak) {
			t.Errorf("instruction holds lint leak term %q", leak)
		}
	}
}

func TestQuote(t *testing.T) {
	if got := q(`a "b": c`); got != `"a \"b\": c"` {
		t.Fatalf("q = %s", got)
	}
}

func TestRequirements(t *testing.T) {
	cand, _ := fixtureCandidate(t, "airline", "a1")
	got := requirements(cand)
	for _, want := range []string{"tau2 airline task a1", "`db`", "`communicate`", `"250"`} {
		if !strings.Contains(got, want) {
			t.Errorf("requirements lack %q:\n%s", want, got)
		}
	}
	cand, _ = fixtureCandidate(t, "retail", "2")
	got = requirements(cand)
	if strings.Contains(got, "`communicate`") || !strings.Contains(got, "Not scored: 1 natural-language") {
		t.Errorf("retail requirements:\n%s", got)
	}
}
