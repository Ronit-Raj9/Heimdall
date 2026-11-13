package handlers

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestConfigDiff(t *testing.T) {
	t.Run("resolved configs", func(t *testing.T) {
		m := testManifest()
		m.Arms["A"] = schemas.ArmManifest{Config: schemas.AgentConfig{Model: "m1", Env: map[string]string{"X": "1"}}}
		m.Arms["B"] = schemas.ArmManifest{Config: schemas.AgentConfig{Model: "m1", Env: map[string]string{"X": "2"},
			Notes: schemas.AgentNotes{UserFile: "codewins.md"}}}
		got, err := ConfigDiff(m)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Path != "env.X" || got[0].A != "1" || got[0].B != "2" ||
			got[1].Path != "notes.user_file" || got[1].A != nil || got[1].B != "codewins.md" {
			t.Fatalf("diff = %+v", got)
		}
	})
	t.Run("falls back to set overrides", func(t *testing.T) {
		got, err := ConfigDiff(testManifest())
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Path != "notes.user_file" || got[0].A != nil || got[0].B != "x.md" {
			t.Fatalf("diff = %+v", got)
		}
	})
	t.Run("one arm", func(t *testing.T) {
		m := testManifest()
		delete(m.Arms, "B")
		got, err := ConfigDiff(m)
		if err != nil || len(got) != 0 {
			t.Fatalf("diff = %+v, %v", got, err)
		}
	})
}
