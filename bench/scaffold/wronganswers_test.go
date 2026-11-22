package scaffold

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGenerateWrongAnswers(t *testing.T) {
	cases := []struct {
		name, ref, other string
		ticket           bool
		want             []string
	}{
		{"number and negation", "The refund window is 30 days.", "", false, []string{"The refund window is 31 days.", "The refund window is not 30 days."}},
		{"yes flip", "Yes, at the gate desk.", "", false, []string{"No, at the gate desk."}},
		{"negated becomes affirmative", "You cannot change it after 2 hours.", "", false, []string{"You cannot change it after 3 hours.", "You can change it after 2 hours."}},
		{"other row", "Paris", "Madrid", false, []string{"Madrid"}},
		{"fallback answer", "Paris", "", false, []string{vagueAnswer}},
		{"fallback reply", "Sorry about that", "", true, []string{vagueReply}},
		{"decimal", "It costs 4.50 euros.", "", false, []string{"It costs 5.50 euros."}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := GenerateWrongAnswers(Row{Ticket: c.ticket}, c.ref, c.other)
			for _, w := range c.want {
				found := false
				for _, g := range got {
					found = found || g == w
				}
				if !found {
					t.Errorf("%q: got %q, want %q among them", c.ref, got, w)
				}
			}
			if len(got) > maxGeneratedWrong {
				t.Errorf("%d answers", len(got))
			}
			for _, g := range got {
				if normWrong(g) == normWrong(c.ref) {
					t.Errorf("generated the right answer %q", g)
				}
			}
		})
	}
	// Deterministic: the same row gives the same answers.
	a := GenerateWrongAnswers(Row{}, "The fee is 12 dollars.", "x")
	b := GenerateWrongAnswers(Row{}, "The fee is 12 dollars.", "x")
	if strings.Join(a, "|") != strings.Join(b, "|") {
		t.Fatal("not deterministic")
	}
}

// A judged row with no wrong answers of its own gets generated ones in
// generated_wrong_answers (another row's reference among them); an exact
// row keeps the old default; the author's own wrong answers are kept as
// they are.
func TestImportRowsGeneratedWrong(t *testing.T) {
	rows := []Row{
		{Line: 1, Question: "Refund window?", Expected: []string{"30 days"}, Rubric: "Says 30 days"},
		{Line: 2, Question: "Capital?", Expected: []string{"Paris"}, Wrong: []string{"Lyon"}},
	}
	tasks, problems := ImportRows(rows, RowTaskOptions{Grader: RowGraderJudge})
	if len(problems) > 0 || len(tasks) != 2 {
		t.Fatalf("%+v %+v", tasks, problems)
	}
	spec := func(rt RowTask) rowAnswerSpec {
		for _, f := range rt.Files {
			if f.Path == "verifier/answer.yaml" {
				var s rowAnswerSpec
				if err := yaml.Unmarshal(f.Data, &s); err != nil {
					t.Fatal(err)
				}
				return s
			}
		}
		t.Fatal("no answer.yaml")
		return rowAnswerSpec{}
	}
	s0, s1 := spec(tasks[0]), spec(tasks[1])
	if !tasks[0].Generated || len(s0.WrongOnes) != 0 || len(s0.GeneratedWrong) == 0 {
		t.Fatalf("row 1: %+v %+v", tasks[0], s0)
	}
	if !strings.Contains(strings.Join(s0.GeneratedWrong, "|"), "Paris") {
		t.Fatalf("row 1 lacks the other row's answer: %q", s0.GeneratedWrong)
	}
	if tasks[1].Generated || strings.Join(s1.WrongOnes, "|") != "Lyon" || len(s1.GeneratedWrong) != 0 {
		t.Fatalf("row 2: %+v %+v", tasks[1], s1)
	}
	exact, _ := ImportRows(rows[:1], RowTaskOptions{})
	if se := spec(exact[0]); exact[0].Generated || strings.Join(se.WrongOnes, "|") != "I don't know." {
		t.Fatalf("exact row: %+v", se)
	}
}

// A rubric reply gets one wrong reply per criterion, each missing what that
// criterion asks for.
func TestGenerateWrongRepliesPerCriterion(t *testing.T) {
	ref := "Sorry for the double charge. I have refunded the second payment. You will see it within 5 days."
	rubric := "Apologise for the double charge\n- Confirm the refund of the duplicate payment\n- Say when the money arrives (5 days)"
	got := GenerateWrongAnswers(Row{Ticket: true, Rubric: rubric}, ref, "")
	want := []string{
		"I have refunded the second payment. You will see it within 5 days.",
		"Sorry for the double charge. You will see it within 5 days.",
		"Sorry for the double charge. I have refunded the second payment.",
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			found = found || g == w
		}
		if !found {
			t.Errorf("missing %q in %q", w, got)
		}
	}
	if len(got) > maxRubricWrong {
		t.Fatalf("%d replies", len(got))
	}
}

// A one-sentence ticket resolution loses the clause each criterion asks
// for; a criterion nothing breaks still gets the reply that does nothing.
func TestGenerateWrongRepliesOneSentence(t *testing.T) {
	ref := "Apologise, confirm the duplicate charge and refund it within 5 days."
	rubric := "Apologise to the customer; Confirm the duplicate charge; Refund it within 5 days"
	got := GenerateWrongAnswers(Row{Ticket: true, Rubric: rubric}, ref, "")
	for _, w := range []string{
		"Confirm the duplicate charge and refund it within 5 days.",
		"Apologise and refund it within 5 days.",
		"Apologise and confirm the duplicate charge.",
	} {
		if !slices.Contains(got, w) {
			t.Errorf("missing %q in %q", w, got)
		}
	}
	got = GenerateWrongAnswers(Row{Ticket: true, Rubric: "Be warm in tone throughout"}, "Send a new reset link.", "")
	if !slices.Contains(got, vagueReply) {
		t.Fatalf("no reply misses the criterion: %q", got)
	}
}
