package onboard

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// A held-out publish into an existing benchmark stamps the canary into the
// tasks it adds only: the benchmark's older tasks keep their files, so their
// readiness records still match.
func TestHeldOutPublishStampsOnlyNewTasks(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)
	in := Input{Data: faqCSV, Name: "faq.csv"}
	inv, err := Inspect(ctx, env, in, "")
	if err != nil {
		t.Fatal(err)
	}
	plan := inv.Suggest
	plan.Hold = SplitPlan{}
	set, err := Draft(ctx, env, in, plan)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Publish(ctx, env, set, PublishOptions{IncludeUnready: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.HeldOut != 0 || first.Canary != "" {
		t.Fatalf("first publish held out %d canary %q", first.HeldOut, first.Canary)
	}
	instructions := func() map[string]string {
		s, err := suite.Builtin(env.Benchmarks())[0].Load(ctx, "faq")
		if err != nil {
			t.Fatal(err)
		}
		files, err := suite.InstructionFiles(s)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, f := range files {
			b, err := os.ReadFile(f.Path)
			if err != nil {
				t.Fatal(err)
			}
			out[f.Task] += string(b)
		}
		return out
	}
	before := instructions()

	plan2 := Plan{Suite: "faq", Hold: SplitPlan{Pct: 100, Seed: 1}}
	set2, err := Draft(ctx, env, in, plan2)
	if err != nil {
		t.Fatal(err)
	}
	if !set2.HasHeldOut() {
		t.Fatal("second draft has no held-out task")
	}
	second, err := Publish(ctx, env, set2, PublishOptions{IncludeUnready: true})
	if err != nil {
		t.Fatal(err)
	}
	if second.HeldOut == 0 || !utils.ValidCanary(second.Canary) {
		t.Fatalf("second publish held out %d canary %q", second.HeldOut, second.Canary)
	}
	after := instructions()
	for _, id := range first.Tasks {
		if after[id] != before[id] {
			t.Errorf("older task %s changed by the held-out publish", id)
		}
	}
	for _, id := range second.Tasks {
		if !strings.Contains(after[id], second.Canary) {
			t.Errorf("published task %s lacks the canary", id)
		}
	}
}
