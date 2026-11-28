package handlers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/canary"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestCanarySimulateFromWebBody posts the body the "What if" section sends
// (ui/app/canaries/views/whatIf.ufl8.test.ts).
func TestCanarySimulateFromWebBody(t *testing.T) {
	r := router.New()
	env := OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: schemas.ModeRecorded}
	for _, a := range OrgAreas() {
		a(env, r)
	}
	start := time.Now()
	ctx := evalCall(r, "POST", APIPrefix+"/canaries/simulate", `{"tasks":8,"trials":1,"sensitivity":"normal","drop":0.2}`)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("simulate: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("one study took %s", d)
	}
	var res canary.SimResult
	if err := json.Unmarshal(ctx.Response.Body(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Config.Tasks != 8 || res.Config.Triggers != simTriggers || res.DetectBy3 <= 0 {
		t.Fatalf("result: %+v", res)
	}
	for body, want := range map[string]string{
		`{"tasks":0}`:                      "tasks must be",
		`{"tasks":8,"drop":2}`:             "drop must be",
		`{"tasks":8,"sensitivity":"wild"}`: "sensitivity must be",
		`{"tasks":8,"reps":9999}`:          "unknown field",
	} {
		c := evalCall(r, "POST", APIPrefix+"/canaries/simulate", body)
		if c.Response.StatusCode() < 400 || !strings.Contains(string(c.Response.Body()), want) {
			t.Fatalf("%s: %d %s", body, c.Response.StatusCode(), c.Response.Body())
		}
	}
}
