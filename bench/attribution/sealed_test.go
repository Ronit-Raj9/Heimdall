package attribution

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEvidence(t *testing.T) {
	write := func(t *testing.T, dir, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("files fill what evidence.json lacks", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "evidence.json", `{"requests":{"complete":true}}`)
		write(t, dir, "agents.json", `{"version":"1.0.0","shape":"team","agents":[{"id":"lead","depth":0,"source":"gateway_key","strength":"proven"}],"coverage":{"steps":0,"attributed":0,"unknown":0}}`)
		write(t, dir, "messages.jsonl", `{"seq":1,"from":"lead","to":"w","body":"x","body_sha256":"h","size":1,"at":"2026-11-18T06:00:00Z","delivered":true}`+"\n\n")
		write(t, dir, "steps.jsonl", `{"seq":1,"ts":"2026-11-18T06:00:00Z","kind":"command","source":"process_observer","proof":"strong","turn":0,"summary":"ls","agent_id":"lead"}`+"\n")
		write(t, dir, "tools.jsonl", `{"seq":1,"server":"mailbox","tool":"POST /send","protocol":"http","args_sha256":"a","result_sha256":"b","is_error":false,"started":"2026-11-18T06:00:00Z","latency_ms":1,"agent_id":"lead"}`+"\n")
		ev, err := LoadEvidence(dir)
		if err != nil {
			t.Fatal(err)
		}
		if ev.Agents == nil || len(ev.Messages) != 1 || len(ev.Steps) != 1 || ev.Steps[0].AgentID != "lead" || len(ev.ToolCalls) != 1 || ev.Events != nil {
			t.Fatalf("%+v", ev)
		}
	})
	t.Run("evidence.json wins", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "evidence.json", `{"messages":[{"seq":7,"from":"a","to":"b","body":"","body_sha256":"","size":0,"at":"2026-11-18T06:00:00Z","delivered":false}]}`)
		write(t, dir, "messages.jsonl", "not json\n")
		ev, err := LoadEvidence(dir)
		if err != nil || len(ev.Messages) != 1 || ev.Messages[0].Seq != 7 {
			t.Fatalf("%+v %v", ev, err)
		}
	})
	t.Run("a broken file is an error", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "evidence.json", `{}`)
		write(t, dir, "steps.jsonl", "{\n")
		if _, err := LoadEvidence(dir); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("no evidence.json is an error", func(t *testing.T) {
		if _, err := LoadEvidence(t.TempDir()); err == nil {
			t.Fatal("want an error")
		}
	})
}
