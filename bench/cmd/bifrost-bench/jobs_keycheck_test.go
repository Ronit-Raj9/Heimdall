package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/jobs"
)

// Review drafting (authoring.assist) and Policy Draft scenarios
// (policy.generate, its estimate too) refuse a provider without a working
// org key when the job is created on the hosted server, as Tasks from your
// API does; a provider with a key is accepted (group D review of H15).
func TestPaidDraftJobsRefuseMissingOrgKeyAtCreate(t *testing.T) {
	env, _, _, _, _ := hostedCalibrateFixture(t) // one openai key in org1
	type validator func(context.Context, jobEnv, json.RawMessage) (any, string, error)
	for _, c := range []struct {
		name   string
		v      validator
		params func(provider string) string
	}{
		{"authoring.assist", validateAuthoringAssist, func(p string) string {
			return `{"suite":"desk","ops":["rubric"],"provider":"` + p + `","model":"m-1","yes":true}`
		}},
		{"policy.generate", validatePolicyGenerate, func(p string) string {
			return `{"suite":"desk","doc":"acme-policy","provider":"` + p + `","model":"m-1","yes":true}`
		}},
		{"policy.generate estimate", validatePolicyGenerate, func(p string) string {
			return `{"suite":"desk","doc":"acme-policy","provider":"` + p + `","model":"m-1","estimate":true}`
		}},
	} {
		_, _, err := c.v(context.Background(), env, json.RawMessage(c.params("anthropic")))
		if !errors.Is(err, jobs.ErrInvalidParams) || !strings.Contains(err.Error(), "Your organization has no working anthropic model key. Add one on the Models page.") {
			t.Errorf("%s with no anthropic key: %v", c.name, err)
		}
		if _, _, err := c.v(context.Background(), env, json.RawMessage(c.params("openai"))); err != nil {
			t.Errorf("%s with a working openai key: %v", c.name, err)
		}
		local := env
		local.Hosted = false
		if _, _, err := c.v(context.Background(), local, json.RawMessage(c.params("anthropic"))); err != nil {
			t.Errorf("%s on a local server: %v", c.name, err)
		}
	}
}
