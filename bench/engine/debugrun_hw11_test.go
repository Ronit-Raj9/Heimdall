package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// A fork's run.json says what the operator changed at the fork call, so the
// run page can say "your message was added" or "the answer was replaced"
// (fix wave 3, N247).
func TestRunDebugCarriesEdit(t *testing.T) {
	for _, tc := range []struct {
		edit *schemas.DebugEdit
		want string // the edit key in run.json, "" for none
	}{
		{&schemas.DebugEdit{Message: "answer in one line"}, `"edit":"message"`},
		{&schemas.DebugEdit{Response: "{}"}, `"edit":"response"`},
		{nil, ""},
		{&schemas.DebugEdit{}, ""},
	} {
		m := &schemas.Manifest{}
		m.Experiment.Debug = &schemas.DebugSpec{Mode: schemas.DebugFork, FromRun: "r0", Call: 1, Edit: tc.edit}
		raw, err := json.Marshal(runDebugOf(m))
		if err != nil {
			t.Fatal(err)
		}
		got := string(raw)
		if tc.want == "" && strings.Contains(got, `"edit"`) || tc.want != "" && !strings.Contains(got, tc.want) {
			t.Fatalf("edit %+v: run debug %s, want %q", tc.edit, got, tc.want)
		}
	}
}
