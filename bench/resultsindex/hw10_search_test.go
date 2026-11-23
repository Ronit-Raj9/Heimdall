package resultsindex

import "testing"

// N291: version values 1, v1 and V1 are the same version, in the query and
// on the run.
func TestSearchVersionForms(t *testing.T) {
	for _, q := range []string{"version:v1", "version:V1", "version:1"} {
		sq, err := ParseSearchQuery(q, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		for _, got := range []string{"v1", "1", "V1"} {
			if !sq.filters(SearchRun{Version: got}) {
				t.Errorf("%s does not match a run of version %q", q, got)
			}
		}
		if sq.filters(SearchRun{Version: "v12"}) || sq.filters(SearchRun{Version: ""}) {
			t.Errorf("%s matches another version", q)
		}
	}
	sq, _ := ParseSearchQuery("", map[string]string{"version": "2026-11-01"}, 0)
	if !sq.filters(SearchRun{Version: "2026-11-01"}) {
		t.Error("a free-form agent_version still matches exactly")
	}
}
