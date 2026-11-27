package clusters

import "testing"

func TestFindRun(t *testing.T) {
	rep := &Report{all: []Cluster{
		{Signature: "c_a", Title: "Tool server unreachable", Count: 2, runs: []RunRef{{RunID: "r1"}, {RunID: "r2"}}},
		{Signature: "c_b", Title: "Tests failed", ErrorText: "502 Bad Gateway on DELETE /mcp", Count: 1, runs: []RunRef{{RunID: "r3"}}},
	}}
	if got, ok := FindRun(rep, "r2"); !ok || got.Signature != "c_a" || got.Text != "Tool server unreachable" || got.Count != 2 {
		t.Fatalf("r2: %+v %v", got, ok)
	}
	if got, ok := FindRun(rep, "r3"); !ok || got.Text != "502 Bad Gateway on DELETE /mcp" {
		t.Fatalf("r3: %+v %v", got, ok)
	}
	if _, ok := FindRun(rep, "r9"); ok {
		t.Fatal("unknown run found")
	}
	if _, ok := FindRun(nil, "r1"); ok {
		t.Fatal("nil report")
	}
}
