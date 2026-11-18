package attribution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArtifactRoundTrip(t *testing.T) {
	root := t.TempDir()
	b := &Blame{Version: Version, RunID: "r_abc", ExpID: "e_team", ParentSeal: "sealhash0123456789", Tier: TierGateProven, AgentID: "worker-b", Detail: "x"}
	if err := Write(root, b); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "e_team", BlameDir, "r_abc", BlameFile)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("blame not at %s: %v", p, err)
	}
	t.Run("read back", func(t *testing.T) {
		got, err := Read(root, "e_team", "r_abc", "sealhash0123456789")
		if err != nil || got == nil || got.AgentID != "worker-b" {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("other seal refused", func(t *testing.T) {
		if _, err := Read(root, "e_team", "r_abc", "othersealhash"); err == nil || !strings.Contains(err.Error(), "names seal") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("absent is nil", func(t *testing.T) {
		got, err := Read(root, "e_team", "r_none", "")
		if err != nil || got != nil {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("replaced by a later blame", func(t *testing.T) {
		b2 := *b
		b2.Tier = TierReplayValidated
		if err := Write(root, &b2); err != nil {
			t.Fatal(err)
		}
		got, err := Read(root, "e_team", "r_abc", "")
		if err != nil || got.Tier != TierReplayValidated {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("content hash is stable", func(t *testing.T) {
		h1, err1 := ContentHash(b)
		h2, err2 := ContentHash(b)
		if err1 != nil || err2 != nil || h1 != h2 || len(h1) != 64 {
			t.Fatalf("%s %s %v %v", h1, h2, err1, err2)
		}
	})
}

func TestPathRefusesBadIDs(t *testing.T) {
	for _, ids := range [][2]string{{"../x", "r_1"}, {"e_1", "r/../../x"}, {"E_1", "r_1"}, {"", "r_1"}} {
		if _, err := Path("/root", ids[0], ids[1]); err == nil {
			t.Errorf("%v: want an error", ids)
		}
	}
	if err := Write(t.TempDir(), &Blame{ExpID: "e_1", RunID: "../r"}); err == nil {
		t.Error("write with a bad run id must fail")
	}
}
