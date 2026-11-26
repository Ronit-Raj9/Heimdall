package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
)

// hostedInvestigatorMaxUSD caps every model-backed job of wave 4, local
// and hosted alike.
const hostedInvestigatorMaxUSD = 5.0

func init() { openCaller = openInvestigatorCaller }

// openInvestigatorCaller opens a budgeted runner for one job: the org's own
// model key (hosted), a cap of min(budget asked, 5.00, the key's month
// budget left), the job bound to the org on the gateway. done closes the
// runner (revoking the job's key) and adds the spend to the org key; it is
// safe to call more than once.
func openInvestigatorCaller(ctx context.Context, env jobEnv, j *jobs.Job, p modelParams) (investigator.Caller, func(), error) {
	switch {
	case p.Provider == "" || p.Model == "":
		return nil, nil, errors.New("Pick a model first.")
	case !p.Yes:
		return nil, nil, errors.New("Confirm the cost first.")
	case p.BudgetUSD <= 0 || math.IsNaN(p.BudgetUSD):
		return nil, nil, errors.New("Set a spending cap above $0 first.")
	}
	if env.App == nil {
		return nil, nil, errors.New("no project config on this server")
	}
	noPrice := errors.New("This model has no price on file, so the spending cap cannot hold. Pick another model.")
	if env.App.prices == nil {
		return nil, nil, noPrice
	}
	price := graders.PricesFrom(env.App.prices.Lookup)
	if _, ok := price(p.Model, schemas.Tokens{Fresh: 1000, Out: 1000}); !ok {
		return nil, nil, noPrice
	}
	budget := math.Min(p.BudgetUSD, hostedInvestigatorMaxUSD)
	gw, url, closeGW, err := calibrateGatewayFor(ctx, env)
	if err != nil {
		return nil, nil, err
	}
	release := func() {
		if closeGW != nil {
			closeGW()
		}
	}
	var orgKey *keystore.Key
	if env.Hosted {
		cg, ok := gw.(calibrateGateway)
		if !ok {
			release()
			return nil, nil, errors.New("this server's gateway can not route calls to the org's own key")
		}
		k, err := calibrateOrgKey(ctx, env.Services.Keys, env.OrgID, p.Provider, p.KeyID)
		if err != nil {
			release()
			if investigatorKeyBudgetUsed(ctx, env, p) {
				return nil, nil, fmt.Errorf("Your model key for %s has used its month budget.", p.Provider)
			}
			return nil, nil, err
		}
		if k.BudgetUSD > 0 {
			left := k.BudgetUSD - k.MonthSpentUSD
			if left <= 0 {
				release()
				return nil, nil, fmt.Errorf("Your model key for %s has used its month budget.", p.Provider)
			}
			budget = math.Min(budget, left)
		}
		value, err := env.Services.Keys.Reveal(ctx, env.OrgID, k.ID)
		if err != nil || value == "" {
			release()
			return nil, nil, fmt.Errorf("read key %s: it can not be decrypted; replace it on the models page", k.Label)
		}
		cred := schemas.ProviderCredential{KeyID: k.ID, Provider: k.Provider, BaseURL: k.BaseURL, Models: k.Models, Value: value}
		if err := cg.ApplyOrgCredentials(ctx, env.OrgID, []schemas.ProviderCredential{cred}); err != nil {
			release()
			return nil, nil, fmt.Errorf("register the org key with the gateway: %w", err)
		}
		cg.BindExperimentOrg(j.ID, env.OrgID)
		orgKey = k
	}
	r, err := investigator.New(investigator.Config{Gateway: gw, URL: url, HTTP: &http.Client{Timeout: 2 * time.Minute},
		Provider: p.Provider, Model: p.Model, Price: price, BudgetUSD: budget, JobID: j.ID, OrgID: env.OrgID, Logger: env.App.logger})
	if err != nil {
		release()
		return nil, nil, err
	}
	if err := r.Open(ctx); err != nil {
		release()
		return nil, nil, err
	}
	var once sync.Once
	done := func() {
		once.Do(func() {
			_ = r.Close(ctx)
			if spent := r.Spent(); orgKey != nil && spent > 0 {
				actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
				defer cancel()
				_ = env.Services.Keys.AddSpend(actx, env.OrgID, orgKey.ID, spent)
			}
			release()
		})
	}
	return r, done, nil
}

// investigatorKeyBudgetUsed is true when the org has a key for the job's
// provider (the one named, when named) whose month budget is spent, so the
// refusal can say so plainly.
func investigatorKeyBudgetUsed(ctx context.Context, env jobEnv, p modelParams) bool {
	if env.Services.Keys == nil {
		return false
	}
	keys, err := env.Services.Keys.List(ctx, env.OrgID, keystore.KindProvider)
	if err != nil {
		return false
	}
	for _, k := range keys {
		if p.KeyID != "" && k.ID != p.KeyID {
			continue
		}
		if judgeKeyProvider(k.Provider) == judgeKeyProvider(p.Provider) && k.BudgetUSD > 0 && k.MonthSpentUSD >= k.BudgetUSD {
			return true
		}
	}
	return false
}
