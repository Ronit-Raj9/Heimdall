package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestLatestDecision(t *testing.T) {
	root := t.TempDir()
	write := func(exp, analysis, body string) {
		t.Helper()
		dir := filepath.Join(root, exp, "analysis", analysis)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "verdict.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("e_invalid", "a_1", `{"analysis_id":"a_1","verdict":{"decision":"invalid"}}`)
	write("e_corrupt", "a_1", `{not json`)
	write("e_empty", "a_1", `{"verdict":{}}`)
	tests := []struct {
		exp    string
		want   schemas.Decision
		wantOK bool
	}{
		{exp: "e_invalid", want: schemas.DecisionInvalid, wantOK: true},
		{exp: "e_corrupt", wantOK: false},
		{exp: "e_empty", wantOK: false},
		{exp: "e_none", wantOK: false},
	}
	x := indexAdapter{root: root}
	for _, tt := range tests {
		t.Run(tt.exp, func(t *testing.T) {
			got, ok := x.LatestDecision(tt.exp)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("LatestDecision = %q %v, want %q %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestLeaderboardUsage(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "positional argument", args: []string{"extra"}},
		{name: "unknown format", args: []string{"-format", "xml"}},
		{name: "unknown flag", args: []string{"-alpha", "0.1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runLeaderboard(tt.args); got != exitUsage {
				t.Errorf("exit = %d, want %d", got, exitUsage)
			}
		})
	}
}
