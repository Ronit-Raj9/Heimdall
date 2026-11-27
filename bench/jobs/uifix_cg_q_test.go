package jobs

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/bench/db/dbtest"
)

// TestListQ: Filter.Q matches kind, target or id, ignores case, treats %
// and _ as text, and never crosses organizations.
func TestListQ(t *testing.T) {
	d := dbtest.Open(t)
	r := New(d, fastOptions("a"))
	r.Register("echo", func(context.Context, *Job, Logger, func(any)) (any, error) { return nil, nil },
		WithValidator(func(_ context.Context, _ string, raw json.RawMessage) (any, string, error) {
			var p struct{ Name string }
			_ = json.Unmarshal(raw, &p)
			return p, "name:" + p.Name, nil
		}))
	ctx := context.Background()
	for _, c := range []struct{ org, name string }{{"org_a", "Alpha"}, {"org_a", "beta_1"}, {"org_b", "alpha"}} {
		if _, err := r.Create(ctx, NewJob{OrgID: c.org, Kind: "echo", Params: json.RawMessage(`{"name":"` + c.name + `"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	count := func(org, q string) int {
		p, err := r.List(ctx, org, Filter{Q: q})
		if err != nil {
			t.Fatal(err)
		}
		return len(p.Jobs)
	}
	if n := count("org_a", "ALPHA"); n != 1 {
		t.Fatalf("ALPHA in org_a = %d", n)
	}
	if n := count("org_a", "a_1"); n != 1 {
		t.Fatalf("a_1 = %d", n)
	}
	if n := count("org_a", "a%"); n != 0 {
		t.Fatalf("a%% must be text, got %d", n)
	}
	if n := count("org_a", "echo"); n != 2 {
		t.Fatalf("kind match = %d", n)
	}
	if n := count("org_b", "beta"); n != 0 {
		t.Fatalf("org_b sees org_a's job")
	}
	if p, ok := likePattern(string(make([]byte, 500))); ok && len(p) > QMaxLen+2 {
		t.Fatalf("pattern not bounded: %d", len(p))
	}
}
