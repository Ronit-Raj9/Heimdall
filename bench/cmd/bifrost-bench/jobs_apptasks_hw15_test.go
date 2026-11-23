package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/jobs"
)

// On the hosted server a drafting model whose provider has no working org
// key is refused when the job is created, in plain words (fix wave 3, N238).
func TestAppTasksCreateRefusesMissingOrgKey(t *testing.T) {
	env, _, _, _, _ := hostedCalibrateFixture(t) // one openai key in org1
	params := func(provider string) json.RawMessage {
		return json.RawMessage(`{"draft":"ag_0123456789ab","stage":"rules","yes":true,"provider":"` + provider + `","model":"m-1"}`)
	}
	_, _, err := validateAppTasksGenerate(context.Background(), env, params("anthropic"))
	if !errors.Is(err, jobs.ErrInvalidParams) || !strings.Contains(err.Error(), "Your organization has no working anthropic model key. Add one on the Models page.") {
		t.Fatalf("missing key: %v", err)
	}
	if strings.Contains(err.Error(), "for the judge") {
		t.Fatalf("refusal names the judge: %v", err)
	}
	if _, _, err := validateAppTasksGenerate(context.Background(), env, params("openai")); err != nil {
		t.Fatalf("working key refused: %v", err)
	}
}
