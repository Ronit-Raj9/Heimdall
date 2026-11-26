package improve

import (
	"strings"
	"testing"
)

func TestParseSpec(t *testing.T) {
	good := "name: loop\nagent: agents/x.yaml\nsuite: s@1\neditable:\n  files: [/home/agent/AGENTS.md]\n"
	s, err := ParseSpec([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if s.Rounds != DefaultRounds || s.Commit.AuditLooks != DefaultRounds || len(s.Controls) != 2 || s.Controls[0] != ControlRetries || s.Kind != Kind {
		t.Errorf("defaults: %+v", s)
	}
	if a := s.RoundAlpha(); a != DefaultAlpha/3 {
		t.Errorf("round alpha %v", a)
	}
	cases := map[string]string{
		"unknown key": good + "bogus: 1\n",
		"bad name":    strings.Replace(good, "name: loop", "name: Loop!", 1),
		"no editable": strings.Replace(good, "  files: [/home/agent/AGENTS.md]\n", "  files: []\n", 1),
		"rel path":    strings.Replace(good, "/home/agent/AGENTS.md", "AGENTS.md", 1),
		"splits":      good + "splits: {dev: 0.5, audit: 0.5, sealed: 0.5}\n",
		"rounds":      good + "rounds: 99\n",
		"control":     good + "controls: [magic]\n",
		"alpha":       good + "commit: {alpha: 0.9}\n",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSpec([]byte(doc)); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestEditableAllows(t *testing.T) {
	e := Editable{Files: []string{"/a/notes.md"}, Dirs: []string{"/a/skills/"}}
	for p, want := range map[string]bool{"/a/notes.md": true, "/a/skills/x/SKILL.md": true, "/a/skills/": false, "/a/skills/../x": false, "/etc/x": false} {
		if e.Allows(p) != want {
			t.Errorf("%s: want %v", p, want)
		}
	}
}
