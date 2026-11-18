package environments

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestConcatMemberStreams(t *testing.T) {
	dir := t.TempDir()
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(MemberStreamFile("lead"), "l1\nl2")
	write("agent.lead.stderr.txt", "e1\n")
	write(MemberStreamFile("w"), "w1\n")
	members := []schemas.MemberRun{{Member: schemas.TeamMember{ID: "lead"}}, {Member: schemas.TeamMember{ID: "w"}}, {Member: schemas.TeamMember{ID: "gone"}}}
	if err := concatMemberStreams(dir, members); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "agent.stream.jsonl"))
	errs, _ := os.ReadFile(filepath.Join(dir, "agent.stderr.txt"))
	if string(out) != "l1\nl2\nw1\n" || string(errs) != "e1\n" {
		t.Errorf("stream %q stderr %q", out, errs)
	}
	if memberContainer("r_1", "w") != "bench-r_1-m-w" {
		t.Errorf("container name %s", memberContainer("r_1", "w"))
	}
}
