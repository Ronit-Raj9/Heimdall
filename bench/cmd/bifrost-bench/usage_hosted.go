package main

// Usage and spend area (web parity L25): the platform route GET
// /api/bench/admin/usage. For each org it indexes the org's run store
// (read-only, never creating a missing folder) and sums the same cost and
// token metrics the org's own GET /usage does.

import (
	"context"
	"os"
	"strings"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/store"
)

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		var src handlers.OrgUsageSource
		if env.db != nil {
			src = &orgsUsage{env: env}
		}
		return handlers.NewAdminUsageHandler(env.base.cfg.Server.Mode, src), nil
	})
}

// orgsUsage computes every org's usage from its folders.
type orgsUsage struct{ env *hostedEnv }

func (u *orgsUsage) keys() keystore.Store {
	if strings.TrimSpace(os.Getenv(keystore.MasterKeyEnv)) == "" {
		return nil // hosted keeps keys only with a configured master key
	}
	mk, err := keystore.MasterKey()
	if err != nil {
		return nil
	}
	ks, err := keystore.Open(u.env.db, mk)
	if err != nil {
		return nil
	}
	return ks
}

func (u *orgsUsage) OrgsUsage(ctx context.Context, q handlers.UsageQuery) ([]handlers.AdminOrgUsage, error) {
	orgs, err := u.env.db.ListOrgs(ctx)
	if err != nil {
		return nil, err
	}
	ks := u.keys()
	out := make([]handlers.AdminOrgUsage, 0, len(orgs))
	for _, o := range orgs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		row := handlers.AdminOrgUsage{OrgID: o.ID, Slug: o.Slug, Name: o.Name}
		var keys []keystore.Key
		if ks != nil {
			if list, err := ks.List(ctx, o.ID, keystore.KindProvider); err == nil {
				keys = list
			}
		}
		var index handlers.ReadIndex
		p, err := u.env.layout.Paths(o.ID)
		if err != nil {
			row.Error = "org folders: " + err.Error()
		} else if st, statErr := os.Stat(p.StoreRoot); statErr == nil && st.IsDir() {
			fs, err := store.NewFSStore(p.StoreRoot, u.env.base.logger)
			if err != nil {
				row.Error = "open run store: " + err.Error()
			} else {
				ix := store.NewIndex(fs)
				if err := ix.Rebuild(ctx); err != nil {
					row.Error = "index runs: " + err.Error()
				} else {
					index = indexAdapter{ix: ix, root: fs.Root()}
				}
			}
		}
		rep := handlers.ComputeUsage(index, keys, q)
		row.CostUSD, row.Tokens, row.Runs = rep.TotalCostUSD, rep.TotalTokens, rep.Runs
		row.UnknownCostRuns, row.KeysMonthSpent = rep.UnknownCostRuns, rep.KeysMonthSpent
		out = append(out, row)
	}
	return out, nil
}
