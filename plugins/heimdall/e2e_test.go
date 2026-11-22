package heimdall_test

// End-to-end test: a real Bifrost core client with the heimdall plugin loaded,
// the OpenAI provider pointed at an httptest upstream. Proves a fault from a
// named plan is injected before the provider is called, the call after the
// fault window reaches the upstream, and the Heimdall ids land on the log
// dimensions. Guarded by HEIMDALL_E2E=1 because it boots a full Bifrost
// client; run it with bench/deploy/bifrost/e2e/run.sh.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/plugins/heimdall"
)

const e2eModel = "gpt-4o-mini"

type e2eAccount struct{ baseURL string }

func (a *e2eAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	return []schemas.ModelProvider{schemas.OpenAI}, nil
}

func (a *e2eAccount) GetKeysForProvider(_ context.Context, _ schemas.ModelProvider) ([]schemas.Key, error) {
	return []schemas.Key{{
		Value:  *schemas.NewSecretVar("e2e-dummy-key"),
		Models: []string{e2eModel},
		Weight: 1.0,
	}}, nil
}

func (a *e2eAccount) GetConfigForProvider(_ schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	nc := schemas.DefaultNetworkConfig
	nc.BaseURL = a.baseURL
	nc.MaxRetries = 0
	return &schemas.ProviderConfig{
		NetworkConfig:            nc,
		ConcurrencyAndBufferSize: schemas.DefaultConcurrencyAndBufferSize,
	}, nil
}

// mockUpstream answers /v1/chat/completions like OpenAI and counts calls.
func mockUpstream(t *testing.T, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-e2e",
			"object":  "chat.completion",
			"created": 1,
			"model":   e2eModel,
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "upstream says hi"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 3, "total_tokens": 6},
		})
	}))
}

func e2eRequest() *schemas.BifrostChatRequest {
	return &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI,
		Model:    e2eModel,
		Input: []schemas.ChatMessage{{
			Role:    schemas.ChatMessageRoleUser,
			Content: &schemas.ChatMessageContent{ContentStr: bifrost.Ptr("hello")},
		}},
	}
}

// reqCtx builds a context the way bifrost-http does: lowercased request
// headers under BifrostContextKeyRequestHeaders.
func reqCtx(headers map[string]string) *schemas.BifrostContext {
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	if headers != nil {
		ctx.SetValue(schemas.BifrostContextKeyRequestHeaders, headers)
	}
	return ctx
}

func dims(ctx *schemas.BifrostContext) map[string]string {
	d, _ := ctx.Value(schemas.BifrostContextKeyDimensions).(map[string]string)
	return d
}

func TestE2E_FaultInjectedAndTagged(t *testing.T) {
	if os.Getenv("HEIMDALL_E2E") != "1" {
		t.Skip("set HEIMDALL_E2E=1 to run the Bifrost end-to-end test")
	}
	var hits atomic.Int64
	up := mockUpstream(t, &hits)
	defer up.Close()

	plugin, err := heimdall.Init(heimdall.Config{
		Enabled: true,
		Plans: map[string]heimdall.Plan{
			"rl-first": {Mode: heimdall.ModeRateLimit, RetryAfterS: 7, Calls: 1},
		},
	})
	if err != nil {
		t.Fatalf("heimdall.Init: %v", err)
	}
	root := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	client, err := bifrost.Init(root, schemas.BifrostConfig{
		Account:    &e2eAccount{baseURL: up.URL},
		LLMPlugins: []schemas.LLMPlugin{plugin},
		Logger:     bifrost.NewDefaultLogger(schemas.LogLevelError),
	})
	if err != nil {
		t.Fatalf("bifrost.Init: %v", err)
	}
	defer client.Shutdown()

	headers := map[string]string{
		heimdall.HeaderRunID:       "run-e2e-1",
		heimdall.HeaderEvalID:      "eval-e2e",
		heimdall.HeaderOrg:         "org-e2e",
		heimdall.HeaderArm:         "baseline",
		heimdall.HeaderFaultPlanID: "rl-first",
	}

	// Call 1: inside the fault window. The plugin answers 429, upstream untouched.
	ctx1 := reqCtx(headers)
	resp, bErr := client.ChatCompletionRequest(ctx1, e2eRequest())
	if bErr == nil {
		t.Fatalf("call 1: want injected 429, got response %+v", resp)
	}
	if bErr.StatusCode == nil || *bErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("call 1: want status 429, got %v (%+v)", bErr.StatusCode, bErr.Error)
	}
	if bErr.ExtraFields.RetryAfter != 7000 {
		t.Errorf("call 1: want RetryAfter 7000ms, got %d", bErr.ExtraFields.RetryAfter)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("call 1: upstream must not be called during the fault, hits=%d", n)
	}
	d1 := dims(ctx1)
	for k, want := range map[string]string{
		"heimdall_run_id":        "run-e2e-1",
		"heimdall_eval_id":       "eval-e2e",
		"heimdall_org":           "org-e2e",
		"heimdall_arm":           "baseline",
		"heimdall_fault_plan_id": "rl-first",
		"heimdall_fault":         heimdall.ModeRateLimit,
	} {
		if d1[k] != want {
			t.Errorf("call 1: dimension %s = %q, want %q (all: %v)", k, d1[k], want, d1)
		}
	}

	// Call 2: the window was one call, so this reaches the mock upstream.
	ctx2 := reqCtx(headers)
	resp, bErr = client.ChatCompletionRequest(ctx2, e2eRequest())
	if bErr != nil {
		t.Fatalf("call 2: want upstream success, got %+v", bErr.Error)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("call 2: want 1 upstream hit, got %d", n)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message == nil || resp.Choices[0].Message.Content == nil ||
		resp.Choices[0].Message.Content.ContentStr == nil || *resp.Choices[0].Message.Content.ContentStr != "upstream says hi" {
		t.Fatalf("call 2: unexpected response %+v", resp)
	}
	d2 := dims(ctx2)
	if d2["heimdall_run_id"] != "run-e2e-1" {
		t.Errorf("call 2: run id tag missing: %v", d2)
	}
	if _, faulted := d2["heimdall_fault"]; faulted {
		t.Errorf("call 2: no fault expected, got heimdall_fault=%q", d2["heimdall_fault"])
	}

	// Call 3: no Heimdall headers. Passes through untagged.
	ctx3 := reqCtx(map[string]string{"content-type": "application/json"})
	if _, bErr = client.ChatCompletionRequest(ctx3, e2eRequest()); bErr != nil {
		t.Fatalf("call 3: plain request failed: %+v", bErr.Error)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("call 3: want 2 upstream hits, got %d", n)
	}
	for k := range dims(ctx3) {
		if len(k) >= 9 && k[:9] == "heimdall_" {
			t.Errorf("call 3: unexpected heimdall tag %s", k)
		}
	}
}
