package handlers

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// GET /suites lists each suite's tiers: defined ones resolved in suite order,
// nightly added as every task when suite.yaml leaves it out, and a tier that
// planning would refuse carrying the reason.
func TestSuitesListTiers(t *testing.T) {
	f := testSuites(t)
	f.suites["canary"].Tiers = map[string]schemas.TaskSelector{
		"pr":       {IDs: []string{"c03", "c01"}},
		"headroom": {IDs: []string{"c02"}},
		"smoke":    {IDs: []string{"c09"}},
	}
	body := decodeBody(t, newSuitesClient(t, f, nil, nil).do(t, "GET", APIPrefix+"/suites", nil, nil))

	type tier struct {
		Name    string
		Tasks   []string
		Defined bool
		Error   string
	}
	tiersOf := func(id string) []tier {
		var out []tier
		for i, obj := range objectsAt(t, suiteByID(t, body, id), "tiers[]") {
			checkKeys(t, fmt.Sprintf("%s tier #%d", id, i), obj, loadKeys(t, "SuiteTier"), false)
			x := tier{Name: obj["name"].(string), Defined: obj["defined"].(bool)}
			for _, v := range obj["tasks"].([]any) {
				x.Tasks = append(x.Tasks, v.(string))
			}
			x.Error, _ = obj["error"].(string)
			out = append(out, x)
		}
		return out
	}

	want := []tier{
		{Name: "pr", Tasks: []string{"c01", "c03"}, Defined: true},
		{Name: "nightly", Tasks: []string{"c01", "c02", "c03"}},
		{Name: "headroom", Tasks: []string{"c02"}, Defined: true},
		{Name: "smoke", Defined: true, Error: "lists task c09, which is not a task of suite canary"},
	}
	if got := tiersOf("canary"); !reflect.DeepEqual(got, want) {
		t.Errorf("canary tiers = %+v, want %+v", got, want)
	}
	if got := tiersOf("drifted"); !reflect.DeepEqual(got, []tier{{Name: "nightly", Tasks: []string{"d01", "d02"}}}) {
		t.Errorf("drifted tiers = %+v, want nightly only", got)
	}
	if _, ok := suiteByID(t, body, "broken")["tiers"]; ok {
		t.Error("a broken suite lists tiers")
	}
}
