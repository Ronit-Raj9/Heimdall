package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
)

// brokenKeys is a key store whose reads fail.
type brokenKeys struct{ keystore.Store }

func (brokenKeys) List(context.Context, string, keystore.Kind) ([]keystore.Key, error) {
	return nil, errors.New("database is down")
}

// TestModelKeyRefusal covers X01: with require, a provider the org has no
// usable key for is refused; a disabled key does not count; the key store
// failing or missing refuses (fail closed); without require only budgets
// refuse, but a key store error still refuses.
func TestModelKeyRefusal(t *testing.T) {
	ctx := context.Background()
	ks := keystore.NewMemory()
	if _, err := ks.Create(ctx, "org_a", keystore.KindProvider, keystore.Put{Provider: "openai", Label: "main", Value: "sk-main-123456789"}, "t"); err != nil {
		t.Fatal(err)
	}
	if err := ModelKeyRefusal(ctx, ks, "org_a", []string{"openai"}, nil, true); err != nil {
		t.Fatalf("usable openai key refused: %v", err)
	}
	err := ModelKeyRefusal(ctx, ks, "org_a", []string{"openai", "anthropic"}, nil, true)
	if !errors.Is(err, ErrModelKeyMissing) || !strings.Contains(err.Error(), "no working anthropic key") || !strings.Contains(err.Error(), "Settings > Model keys") {
		t.Fatalf("missing anthropic key: %v", err)
	}
	if err := ModelKeyRefusal(ctx, ks, "org_a", []string{"anthropic"}, nil, false); err != nil {
		t.Fatalf("local mode must not require a key: %v", err)
	}
	if err := ModelKeyRefusal(ctx, ks, "org_a", []string{"", " "}, nil, true); err != nil {
		t.Fatalf("no provider named: %v", err)
	}
	if err := ModelKeyRefusal(ctx, brokenKeys{}, "org_a", []string{"openai"}, nil, false); !errors.Is(err, ErrKeyStoreUnavailable) {
		t.Fatalf("key store error must fail closed: %v", err)
	}
	if err := ModelKeyRefusal(ctx, nil, "org_a", []string{"openai"}, nil, true); !errors.Is(err, ErrKeyStoreUnavailable) {
		t.Fatalf("hosted with no key store: %v", err)
	}
	if err := ModelKeyRefusal(ctx, nil, "org_a", []string{"openai"}, nil, false); err != nil {
		t.Fatalf("local with no key store: %v", err)
	}
	dis, err := ks.Create(ctx, "org_a", keystore.KindProvider, keystore.Put{Provider: "gemini", Label: "off", Value: "g-123456789"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := ModelKeyRefusal(ctx, &statusKeys{Store: ks, id: dis.ID, status: "failed"}, "org_a", []string{"gemini"}, nil, true); !errors.Is(err, ErrModelKeyMissing) {
		t.Fatalf("a failing key must not count as working: %v", err)
	}
}

// statusKeys overrides one key's status in List.
type statusKeys struct {
	keystore.Store
	id, status string
}

func (s *statusKeys) List(ctx context.Context, org string, k keystore.Kind) ([]keystore.Key, error) {
	list, err := s.Store.List(ctx, org, k)
	for i := range list {
		if list[i].ID == s.id {
			list[i].Status = s.status
		}
	}
	return list, err
}

// TestPaidManifestProviders checks which arms need an org key: not a smoke
// experiment, not a scripted control that makes no model call, not a
// deployed agent with external model traffic.
func TestPaidManifestProviders(t *testing.T) {
	arm := func(c schemas.AgentConfig) schemas.ArmManifest { return schemas.ArmManifest{Config: c} }
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{
		"A": arm(schemas.AgentConfig{Adapter: "command", Gateway: schemas.GatewayPolicy{Provider: "openai"}}),
		"B": arm(schemas.AgentConfig{Adapter: "scripted", Scripted: &schemas.ScriptedSpec{Kind: "oracle"}, Gateway: schemas.GatewayPolicy{Provider: "anthropic"}}),
		"C": arm(schemas.AgentConfig{Adapter: "remote_http", Remote: &schemas.RemoteAgent{URL: "http://x"}, Gateway: schemas.GatewayPolicy{Provider: "gemini"}}),
		"D": arm(schemas.AgentConfig{Adapter: "scripted", Scripted: &schemas.ScriptedSpec{Kind: "toy"}, Gateway: schemas.GatewayPolicy{Provider: "groq"}}),
	}}
	got := PaidManifestProviders(m)
	if len(got) != 2 || !strings.Contains(strings.Join(got, ","), "openai") || !strings.Contains(strings.Join(got, ","), "groq") {
		t.Fatalf("paid providers %v, want openai and groq", got)
	}
	m.Experiment.Smoke = true
	if got := PaidManifestProviders(m); len(got) != 0 {
		t.Fatalf("smoke experiment providers %v", got)
	}
}

// TestDailyCapRefusal checks that the caps are off at 0 and refuse at the cap.
func TestDailyCapRefusal(t *testing.T) {
	if err := DailyCapRefusal(0, 0, 1000, 1000); err != nil {
		t.Fatalf("caps off: %v", err)
	}
	if err := DailyCapRefusal(10, 0, 9, 0); err != nil {
		t.Fatalf("under the run cap: %v", err)
	}
	if err := DailyCapRefusal(10, 0, 10, 0); !errors.Is(err, ErrDailyCap) || !strings.Contains(err.Error(), "10 of its 10 runs") {
		t.Fatalf("run cap: %v", err)
	}
	if err := DailyCapRefusal(0, 5, 0, 5.5); !errors.Is(err, ErrDailyCap) || !strings.Contains(err.Error(), "$5.50 of its $5.00") {
		t.Fatalf("spend cap: %v", err)
	}
}

// TestQuickRunHostedRefusesMissingKey covers X01 on QuickRun: a hosted org
// with no key for the provider gets 409 with a link to Models & keys, and
// nothing is planned.
func TestQuickRunHostedRefusesMissingKey(t *testing.T) {
	l := &fakeLauncher{}
	r, _ := quickRunServer(t, Services{OrgID: "org_a", Hosted: true, Launcher: l, Keys: keystore.NewMemory()})
	resp := postQuick(r, `{"model":{"provider":"openai","model":"gpt-x"},"start":false}`)
	body := string(resp.Response.Body())
	if resp.Response.StatusCode() != 409 || !strings.Contains(body, "no working openai key") || !strings.Contains(body, `"link":"/models"`) {
		t.Fatalf("status %d: %s", resp.Response.StatusCode(), body)
	}
	if l.planned != nil || l.created != nil {
		t.Fatal("a refused QuickRun still planned or created")
	}
	r, _ = quickRunServer(t, Services{OrgID: "org_a", Hosted: true, Launcher: l})
	resp = postQuick(r, `{"model":{"provider":"openai","model":"gpt-x"},"start":false}`)
	if resp.Response.StatusCode() != 503 {
		t.Fatalf("hosted with no key store: %d %s", resp.Response.StatusCode(), resp.Response.Body())
	}
}

// TestEvaluationHostedRefusesMissingKey covers X01 on POST /evaluations: a
// hosted org with no openai key is refused before anything is written, with a
// link to Models & keys; with a key the evaluation is accepted.
func TestEvaluationHostedRefusesMissingKey(t *testing.T) {
	body := `{"mode":"score","arms":[{"label":"A","agent":"scripted","model":"gpt-5.4-mini","provider":"openai"}],"benchmarks":["longmemeval"],"tasks_per_benchmark":4,"trials":1}`
	ks := keystore.NewMemory()
	l := &evalLauncher{}
	r, _ := evalServer(t, Services{Launcher: l, Suites: evalSuites(), OrgID: "org1", Hosted: true, Keys: ks})
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations", body)
	got := string(ctx.Response.Body())
	if ctx.Response.StatusCode() != 409 || !strings.Contains(got, "no working openai key") || !strings.Contains(got, `"link":"/models"`) {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), got)
	}
	if _, err := ks.Create(context.Background(), "org1", keystore.KindProvider, keystore.Put{Provider: "openai", Label: "main", Value: "sk-main-123456789"}, "t"); err != nil {
		t.Fatal(err)
	}
	ctx = evalCall(r, "POST", APIPrefix+"/evaluations", body)
	if ctx.Response.StatusCode() != 202 {
		t.Fatalf("with a key: status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}
