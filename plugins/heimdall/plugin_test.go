package heimdall

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

func newCtx(headers map[string]string) *schemas.BifrostContext {
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	ctx.SetValue(schemas.BifrostContextKeyRequestHeaders, headers)
	return ctx
}

func chatReq(model string) *schemas.BifrostRequest {
	return &schemas.BifrostRequest{
		RequestType: schemas.ChatCompletionRequest,
		ChatRequest: &schemas.BifrostChatRequest{Provider: schemas.OpenAI, Model: model},
	}
}

func mustInit(t *testing.T, cfg Config) *Plugin {
	t.Helper()
	p, err := Init(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p.sleep = func(*schemas.BifrostContext, time.Duration) bool { return true }
	return p
}

func hdr(run, plan string) map[string]string {
	return map[string]string{HeaderRunID: run, HeaderEvalID: "ev1", HeaderOrg: "org1", HeaderArm: "a", HeaderFaultPlanID: plan}
}

func run(t *testing.T, p *Plugin, h map[string]string, model string) (*schemas.BifrostContext, *schemas.LLMPluginShortCircuit) {
	t.Helper()
	ctx := newCtx(h)
	_, sc, err := p.PreLLMHook(ctx, chatReq(model))
	if err != nil {
		t.Fatal(err)
	}
	return ctx, sc
}

func status(sc *schemas.LLMPluginShortCircuit) int {
	if sc == nil || sc.Error == nil || sc.Error.StatusCode == nil {
		return 0
	}
	return *sc.Error.StatusCode
}

func TestNoHeadersPassesThrough(t *testing.T) {
	p := mustInit(t, Config{Enabled: true, Plans: map[string]Plan{"p": {Mode: ModeError}}})
	ctx, sc := run(t, p, map[string]string{"content-type": "application/json"}, "gpt")
	if sc != nil {
		t.Fatal("untagged request must pass")
	}
	if ctx.Value(schemas.BifrostContextKeyDimensions) != nil {
		t.Fatal("untagged request must not get dimensions")
	}
}

func TestTagsOnContextAndHeaders(t *testing.T) {
	p := mustInit(t, Config{})
	ctx, sc := run(t, p, hdr("r1", ""), "gpt")
	if sc != nil {
		t.Fatal("no plan, no fault")
	}
	dims := ctx.Value(schemas.BifrostContextKeyDimensions).(map[string]string)
	if dims["heimdall_run_id"] != "r1" || dims["heimdall_eval_id"] != "ev1" || dims["heimdall_org"] != "org1" || dims["heimdall_arm"] != "a" {
		t.Fatalf("dims %v", dims)
	}
	meta := schemas.AcquireHTTPResponseMetadata()
	defer schemas.ReleaseHTTPResponseMetadata(meta)
	if err := p.HTTPTransportResponseHeadersHook(ctx, nil, meta); err != nil {
		t.Fatal(err)
	}
	if meta.Header(HeaderRunID) != "r1" || meta.Header(HeaderEvalID) != "ev1" {
		t.Fatal("response not tagged")
	}
}

func TestDisabledNeverFaults(t *testing.T) {
	p := mustInit(t, Config{Enabled: false, Plans: map[string]Plan{"p": {Mode: ModeError}}})
	if _, sc := run(t, p, hdr("r", "p"), "gpt"); sc != nil {
		t.Fatal("disabled plugin faulted")
	}
}

func TestErrorWindowByCallCount(t *testing.T) {
	p := mustInit(t, Config{Enabled: true, Plans: map[string]Plan{"p": {Mode: ModeError, Status: 502, FromCall: 2, Calls: 2}}})
	want := []int{0, 502, 502, 0, 0}
	for i, w := range want {
		_, sc := run(t, p, hdr("r", "p"), "gpt")
		if got := status(sc); got != w {
			t.Fatalf("call %d: status %d want %d", i+1, got, w)
		}
	}
	// a different run has its own counter
	if _, sc := run(t, p, hdr("other", "p"), "gpt"); sc != nil {
		t.Fatal("call 1 of another run must pass")
	}
}

func TestRateLimitRetryAfter(t *testing.T) {
	p := mustInit(t, Config{Enabled: true, Plans: map[string]Plan{"rl": {Mode: ModeRateLimit, RetryAfterS: 7}}})
	ctx, sc := run(t, p, hdr("r", "rl"), "gpt")
	if status(sc) != 429 || sc.Error.ExtraFields.RetryAfter != 7000 {
		t.Fatalf("got %d %d", status(sc), sc.Error.ExtraFields.RetryAfter)
	}
	meta := schemas.AcquireHTTPResponseMetadata()
	defer schemas.ReleaseHTTPResponseMetadata(meta)
	_ = p.HTTPTransportResponseHeadersHook(ctx, nil, meta)
	if meta.Header("Retry-After") != "7" || meta.Header(HeaderFaultApplied) != ModeRateLimit {
		t.Fatalf("headers %q %q", meta.Header("Retry-After"), meta.Header(HeaderFaultApplied))
	}
	dims := ctx.Value(schemas.BifrostContextKeyDimensions).(map[string]string)
	if dims["heimdall_fault"] != ModeRateLimit {
		t.Fatal("fault not in log dims")
	}
}

func TestTimeoutAndLatency(t *testing.T) {
	var slept []time.Duration
	p := mustInit(t, Config{Enabled: true, Plans: map[string]Plan{
		"to":  {Mode: ModeTimeout, DelayMS: 1500},
		"lat": {Mode: ModeLatency, DelayMS: 250},
	}})
	p.sleep = func(_ *schemas.BifrostContext, d time.Duration) bool { slept = append(slept, d); return true }
	if _, sc := run(t, p, hdr("r", "to"), "gpt"); status(sc) != 504 {
		t.Fatal("timeout must answer 504")
	}
	if _, sc := run(t, p, hdr("r", "lat"), "gpt"); sc != nil {
		t.Fatal("latency must pass after the hold")
	}
	if len(slept) != 2 || slept[0] != 1500*time.Millisecond || slept[1] != 250*time.Millisecond {
		t.Fatalf("slept %v", slept)
	}
}

func TestRealSleepHonoursCancel(t *testing.T) {
	ctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	cancel()
	if ctxSleep(ctx, time.Hour) {
		t.Fatal("cancelled sleep must return false")
	}
}

func TestFlakySeededAndRepeatable(t *testing.T) {
	cfg := Config{Enabled: true, Plans: map[string]Plan{"f": {Mode: ModeFlaky, FailProb: 0.3, Seed: "s1"}}}
	pattern := func() []int {
		p := mustInit(t, cfg)
		var out []int
		for i := 0; i < 200; i++ {
			_, sc := run(t, p, hdr("r", "f"), "gpt")
			out = append(out, status(sc))
		}
		return out
	}
	a, b := pattern(), pattern()
	fails := 0
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("flaky must be repeatable for one seed")
		}
		if a[i] == 503 {
			fails++
		}
	}
	if fails < 30 || fails > 90 {
		t.Fatalf("fail count %d not near 30%% of 200", fails)
	}
}

func TestOutageWindowBySeconds(t *testing.T) {
	p := mustInit(t, Config{Enabled: true, Plans: map[string]Plan{"o": {Mode: ModeOutage, UntilS: 10}}})
	now := time.Unix(1000, 0)
	p.tracker.now = func() time.Time { return now }
	if _, sc := run(t, p, hdr("r", "o"), "gpt"); status(sc) != 503 {
		t.Fatal("outage must answer 503")
	}
	now = now.Add(9 * time.Second)
	if _, sc := run(t, p, hdr("r", "o"), "gpt"); status(sc) != 503 {
		t.Fatal("still inside window")
	}
	now = now.Add(2 * time.Second)
	if _, sc := run(t, p, hdr("r", "o"), "gpt"); sc != nil {
		t.Fatal("window over, model back")
	}
}

func TestModelFilterLetsFallbackThrough(t *testing.T) {
	p := mustInit(t, Config{Enabled: true, Plans: map[string]Plan{"o": {Mode: ModeOutage, Model: "primary"}}})
	if _, sc := run(t, p, hdr("r", "o"), "primary"); status(sc) != 503 {
		t.Fatal("primary must be down")
	}
	if _, sc := run(t, p, hdr("r", "o"), "backup"); sc != nil {
		t.Fatal("fallback model must pass")
	}
}

func TestInlinePlanOnlyWhenAllowed(t *testing.T) {
	h := map[string]string{HeaderRunID: "r", HeaderFaultPlan: `{"mode":"error","status":418}`}
	p := mustInit(t, Config{Enabled: true})
	if _, sc := run(t, p, h, "gpt"); sc != nil {
		t.Fatal("inline plan must be ignored by default")
	}
	p = mustInit(t, Config{Enabled: true, AllowInlinePlans: true})
	if _, sc := run(t, p, h, "gpt"); status(sc) != 418 {
		t.Fatal("inline plan must apply when allowed")
	}
	// base64url form
	h[HeaderFaultPlan] = "eyJtb2RlIjoib3V0YWdlIn0" // {"mode":"outage"}
	h[HeaderRunID] = "r2"
	if _, sc := run(t, p, h, "gpt"); status(sc) != 503 {
		t.Fatal("base64 inline plan must apply")
	}
}

func TestHTTPPreHookReadsHeaders(t *testing.T) {
	p := mustInit(t, Config{Enabled: true, Plans: map[string]Plan{"p": {Mode: ModeError}}})
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	req := &schemas.HTTPRequest{Headers: map[string]string{"X-Heimdall-Run-Id": "r9", "X-Heimdall-Fault-Plan-Id": "p"}}
	if resp, err := p.HTTPTransportPreHook(ctx, req); resp != nil || err != nil {
		t.Fatal("pre hook must not answer")
	}
	_, sc, _ := p.PreLLMHook(ctx, chatReq("gpt"))
	if status(sc) != 500 {
		t.Fatal("plan from transport headers must apply")
	}
}

func TestValidate(t *testing.T) {
	bad := []Plan{
		{Mode: "nope"},
		{Mode: ModeFlaky},
		{Mode: ModeError, FailProb: 0.5},
		{Mode: ModeLatency},
		{Mode: ModeError, Status: 200},
		{Mode: ModeLatency, DelayMS: maxDelayMS + 1},
	}
	for _, b := range bad {
		if b.Validate() == nil {
			t.Fatalf("plan %+v must fail", b)
		}
	}
	if _, err := Init(Config{Plans: map[string]Plan{"x": {Mode: "nope"}}}); err == nil {
		t.Fatal("Init must reject a bad plan")
	}
}
