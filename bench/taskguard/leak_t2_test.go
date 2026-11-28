package taskguard

import "testing"

// TestLeakRequestWithoutAnswer: an instruction that states the customer's
// request without the expected reply passes the leak guard; one that also
// lists the reply fails it.
func TestLeakRequestWithoutAnswer(t *testing.T) {
	answer := "I could not check your refund status right now."
	s, task := traceTask(t, "A customer writes: Where is my refund for order 1042?\nLook it up with refund_status and reply to the customer.\n")
	if err := Write(task.Dir, Record{Builder: BuilderTrace, LeakTexts: []string{answer}}); err != nil {
		t.Fatal(err)
	}
	if c := check(t, Apply(s, task, okRec()), CheckLeak); !c.Passed {
		t.Fatalf("request without the answer: %+v", c)
	}
	s, task = traceTask(t, "Where is my refund for order 1042?\nReply with one of:\n- I could not check your refund status right now.\n")
	if err := Write(task.Dir, Record{Builder: BuilderTrace, LeakTexts: []string{answer}}); err != nil {
		t.Fatal(err)
	}
	if c := check(t, Apply(s, task, okRec()), CheckLeak); c.Passed {
		t.Fatal("instruction listing the answer passed the leak guard")
	}
}
