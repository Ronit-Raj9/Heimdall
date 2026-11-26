package improve

import "testing"

func TestUpdateCountersFromFlips(t *testing.T) {
	pb := PlaybookObject{Next: 3, Items: []PlaybookItem{{ID: "pb-0001"}, {ID: "pb-0002", Helpful: 1}}}
	applied := []AppliedEdit{
		{Edit: Edit{Op: "add_item", ID: "pb-0001", PredictsFixed: []string{"t1", "t2", "t3"}}, Result: "applied"},
		{Edit: Edit{Op: "add_item", PredictsFixed: []string{"t1"}}, Result: "merged_into:pb-0002"},
		{Edit: Edit{Op: "edit_item", ID: "pb-0002", PredictsFixed: []string{"t1"}}, Result: "refused"},
	}
	flips := map[string]string{"t1": "fail_to_pass", "t2": "pass_to_fail"}
	out := UpdateCounters(pb, applied, flips)
	if out.Items[0].Helpful != 1 || out.Items[0].Harmful != 1 {
		t.Fatalf("pb-0001: %+v", out.Items[0])
	}
	if out.Items[1].Helpful != 2 || out.Items[1].Harmful != 0 {
		t.Fatalf("pb-0002: %+v", out.Items[1])
	}
	if pb.Items[0].Helpful != 0 {
		t.Fatal("the input playbook changed")
	}
}

func TestPruneHints(t *testing.T) {
	pb := PlaybookObject{Items: []PlaybookItem{{ID: "pb-0001", Harmful: 3, Helpful: 1}, {ID: "pb-0002", Harmful: 2, Helpful: 1}}}
	opened := map[string]int{"/s/old/SKILL.md": 1, "/s/new/SKILL.md": 3, "/notes.md": 0}
	h := PruneHints(pb, opened, 3)
	if len(h) != 2 || h[0].Op != "delete_item" || h[0].ID != "pb-0001" || h[1].Op != "retire_skill" || h[1].Path != "/s/old/SKILL.md" {
		t.Fatalf("%+v", h)
	}
	if h[1].Reason != "not opened in practice runs for 2 rounds" {
		t.Fatalf("reason %q", h[1].Reason)
	}
}
