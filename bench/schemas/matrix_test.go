package schemas

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMatrixModelDecode(t *testing.T) {
	tests := []struct {
		name, yamlDoc, jsonDoc string
		want                   MatrixModel
		wantErr                bool
	}{
		{name: "name only", yamlDoc: "gpt-5-mini", jsonDoc: `"gpt-5-mini"`, want: MatrixModel{Model: "gpt-5-mini"}},
		{name: "with provider", yamlDoc: "{model: claude-sonnet-5, provider: anthropic}", jsonDoc: `{"model":"claude-sonnet-5","provider":"anthropic"}`,
			want: MatrixModel{Model: "claude-sonnet-5", Provider: "anthropic"}},
		{name: "list is not a model", yamlDoc: "[a, b]", jsonDoc: `["a"]`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var y, j MatrixModel
			yerr := yaml.Unmarshal([]byte(tt.yamlDoc), &y)
			jerr := json.Unmarshal([]byte(tt.jsonDoc), &j)
			if tt.wantErr {
				if yerr == nil || jerr == nil {
					t.Fatalf("errors %v %v, want both", yerr, jerr)
				}
				return
			}
			if yerr != nil || jerr != nil || y != tt.want || j != tt.want {
				t.Fatalf("yaml %+v (%v) json %+v (%v), want %+v", y, yerr, j, jerr, tt.want)
			}
		})
	}
}

func TestMatrixFieldOmittedWhenEmpty(t *testing.T) {
	e := Experiment{Name: "x", Arms: map[string]ArmSpec{"A": {}, "B": {}}}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "matrix") {
		t.Fatalf("experiment without a matrix encodes one: %s", raw)
	}
	out, err := yaml.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "matrix") {
		t.Fatalf("experiment without a matrix encodes one in YAML: %s", out)
	}
	e.Matrix = &MatrixSpec{Models: []MatrixModel{{Model: "m1"}, {Model: "m2", Provider: "p"}}}
	raw, err = json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"matrix":{"models":[{"model":"m1"},{"model":"m2","provider":"p"}]}`) {
		t.Fatalf("matrix encoding %s", raw)
	}
}

func TestMatrixRowFlattensEntry(t *testing.T) {
	raw, err := json.Marshal(MatrixRow{Arm: "A", Provider: "openai", LeaderboardEntry: LeaderboardEntry{Rank: 1, Agent: "codex"}})
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]any
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"arm", "provider", "rank", "agent", "success", "metrics"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("row lacks %q: %s", k, raw)
		}
	}
	if _, nested := keys["LeaderboardEntry"]; nested {
		t.Fatalf("entry nested instead of flattened: %s", raw)
	}
}
