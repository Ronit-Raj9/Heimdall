package evidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestCollectAgents(t *testing.T) {
	t.Run("single agent writes nothing", func(t *testing.T) {
		dir := t.TempDir()
		ev := &schemas.Evidence{}
		steps := []schemas.StepRecord{{Seq: 1, Source: schemas.StepSourceGateway}}
		if err := collectAgents(&schemas.RunContext{Dir: dir}, ev, steps); err != nil {
			t.Fatal(err)
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 || ev.Agents != nil || ev.Messages != nil {
			t.Errorf("wrote %v, agents %v", entries, ev.Agents)
		}
	})
	t.Run("team", func(t *testing.T) {
		dir := t.TempDir()
		team := schemas.TeamRecord{Members: []schemas.TeamMember{{ID: "lead", Role: "lead"}, {ID: "w"}}}
		b, _ := json.Marshal(team)
		if err := os.WriteFile(filepath.Join(dir, schemas.TeamFile), b, 0o644); err != nil {
			t.Fatal(err)
		}
		state := filepath.Join(dir, "state", schemas.MailboxServer)
		if err := os.MkdirAll(state, 0o755); err != nil {
			t.Fatal(err)
		}
		rec := `{"seq":1,"id":"1","from":"lead","sender":"lead","to":"w","body":"go","at":"2026-11-18T06:00:00Z","read_by":["w"]}` + "\n"
		if err := os.WriteFile(filepath.Join(state, schemas.MailboxExport), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
		ev := &schemas.Evidence{}
		if err := collectAgents(&schemas.RunContext{Dir: dir}, ev, nil); err != nil {
			t.Fatal(err)
		}
		if ev.Agents == nil || len(ev.Agents.Agents) != 2 || len(ev.Messages) != 1 {
			t.Fatalf("agents %+v messages %+v", ev.Agents, ev.Messages)
		}
		for _, f := range []string{schemas.AgentsFile, schemas.MessagesFile} {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				t.Errorf("%s: %v", f, err)
			}
		}
	})
	t.Run("bad team.json", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, schemas.TeamFile), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := collectAgents(&schemas.RunContext{Dir: dir}, &schemas.Evidence{}, nil); err == nil {
			t.Error("a torn team.json is an error")
		}
	})
}

// TestCollectAgentsTaskMailbox: outside a team run, a task's own service
// that happens to be named mailbox gives no messages.
func TestCollectAgentsTaskMailbox(t *testing.T) {
	dir := t.TempDir()
	ev := &schemas.Evidence{ToolCalls: []schemas.ToolCall{sendRow(1, "", `{"from":"a","to":"b","body":"x"}`, `{"id":"1"}`)}}
	if err := collectAgents(&schemas.RunContext{Dir: dir}, ev, nil); err != nil {
		t.Fatal(err)
	}
	if ev.Messages != nil {
		t.Errorf("messages = %+v", ev.Messages)
	}
	if _, err := os.Stat(filepath.Join(dir, schemas.MessagesFile)); !os.IsNotExist(err) {
		t.Errorf("messages.jsonl written: %v", err)
	}
}
