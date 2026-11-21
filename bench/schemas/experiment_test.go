package schemas

import (
	"encoding/json"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTaskSelectorJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want TaskSelector
		out  string
	}{
		{"all", `"all"`, TaskSelector{All: true}, `"all"`},
		{"list", `["t01","t02"]`, TaskSelector{IDs: []string{"t01", "t02"}}, `["t01","t02"]`},
		{"legacy object", `{"all":false,"ids":["t03"]}`, TaskSelector{IDs: []string{"t03"}}, `["t03"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got TaskSelector
			if err := json.Unmarshal([]byte(tt.in), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			b, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tt.out {
				t.Fatalf("marshal %s, want %s", b, tt.out)
			}
		})
	}
	var bad TaskSelector
	if err := json.Unmarshal([]byte(`"some"`), &bad); err == nil {
		t.Fatal("want an error for a string other than all")
	}
}

func TestTaskSelectorYAMLRoundTrip(t *testing.T) {
	for _, in := range []TaskSelector{{All: true}, {IDs: []string{"t01", "t05"}}} {
		b, err := yaml.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		var out TaskSelector
		if err := yaml.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(in, out) {
			t.Fatalf("round trip %+v -> %s -> %+v", in, b, out)
		}
	}
}

func TestBudgetParityIncludesConfiguredAndEffectiveLimits(t *testing.T) {
	e := &Experiment{Arms: map[string]ArmSpec{"A": {}, "B": {}}}
	base := func(wall int, retries int) ArmManifest {
		return ArmManifest{Config: AgentConfig{Budgets: Budgets{WallS: wall, MaxRetries: retries}}, BudgetSnapshot: &BudgetSnapshot{
			Configured: Budgets{WallS: wall, MaxRetries: retries}, Effective: Budgets{WallS: wall, MaxRetries: retries},
		}}
	}
	parity := BudgetParityOf(e, map[string]ArmManifest{"A": base(60, 1), "B": base(30, 2)})
	if parity.Status != BudgetParityUnexpectedDifference || len(parity.ConfiguredDiffs) == 0 || len(parity.EffectiveDiffs) == 0 {
		t.Fatalf("parity = %+v", parity)
	}
	e.Arms["B"] = ArmSpec{Set: map[string]any{"budgets.wall_s": 30, "budgets.max_retries": 2}}
	parity = BudgetParityOf(e, map[string]ArmManifest{"A": base(60, 1), "B": base(30, 2)})
	if parity.Status != BudgetParityDeclaredDifference {
		t.Fatalf("declared parity = %+v", parity)
	}
}

// TestTaskSelectorObjectFormIsStrict is QC2-11-09: the JSON object form of
// a task selector refuses unknown keys and a contradictory or empty
// selection, and still takes exactly one form.
func TestTaskSelectorObjectFormIsStrict(t *testing.T) {
	for _, bad := range []string{
		`{"all": true, "ids": ["t01"]}`,
		`{"ids": ["t01"], "idz": ["t02"]}`,
		`{"all": false}`,
		`{}`,
	} {
		var s TaskSelector
		if err := json.Unmarshal([]byte(bad), &s); err == nil {
			t.Errorf("%s accepted as %+v", bad, s)
		}
	}
	for in, want := range map[string]TaskSelector{
		`{"all": true}`:    {All: true},
		`{"ids": ["t01"]}`: {IDs: []string{"t01"}},
		`"all"`:            {All: true},
		`["t01"]`:          {IDs: []string{"t01"}},
	} {
		var s TaskSelector
		if err := json.Unmarshal([]byte(in), &s); err != nil || s.All != want.All || len(s.IDs) != len(want.IDs) {
			t.Errorf("%s: %+v %v", in, s, err)
		}
	}
}
