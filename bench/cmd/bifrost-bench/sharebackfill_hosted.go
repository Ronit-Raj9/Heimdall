package main

// Share link index, hosted wiring (fix wave 3, N293). A hosted share link's
// token is opaque (s_...) and names no organization; the dispatcher finds
// the org through the database's share_tokens table (tenancy.DBDirectory).
// The share handler writes a row when a link is created, and once at start
// backfillShareIndex writes one for every active link of every org, so links
// made before the change get their opaque token too (old tokens keep
// working and are moved to the new address).

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/sharelinks"
	"github.com/maximhq/bifrost/bench/tenancy"
)

func init() {
	// Not a route area: it registers the org runtimes' share index when the
	// hosted server is built, so every hosted org hands out opaque tokens.
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		if dir, ok := hostedProjectsDir(env); ok && env.db != nil {
			handlers.RegisterShareIndex(dir, dbShareIndex{d: env.db})
		}
		return nil, nil
	})
}

// dbShareIndex is handlers.ShareIndexWriter over the database.
type dbShareIndex struct{ d *db.DB }

func (x dbShareIndex) PutShareToken(ctx context.Context, token, orgID, shareID string) error {
	return x.d.PutShareToken(ctx, token, orgID, shareID)
}

// hostedProjectsDir is the folder that holds every org's project root.
func hostedProjectsDir(env *hostedEnv) (string, bool) {
	if env == nil || !env.layout.Hosted || env.layout.StorageRoot == "" {
		return "", false
	}
	root, err := filepath.Abs(env.layout.StorageRoot)
	if err != nil {
		return "", false
	}
	return filepath.Join(root, tenancy.ProjectsDir), true
}

// backfillShareIndex indexes every active share link at start (serveHosted
// runs it in the background) and logs how many.
func backfillShareIndex(ctx context.Context, env *hostedEnv) {
	n, err := backfillShareIndexOnce(ctx, env)
	if err != nil {
		env.base.logger.Error("share index backfill failed; old links still open and are indexed when visited", "indexed", n, "error", err.Error())
		return
	}
	env.base.logger.Info(fmt.Sprintf("share index: %d links", n))
}

// backfillShareIndexOnce writes the index row of every active link of every
// org and returns how many it wrote or found. Repeating it adds nothing.
func backfillShareIndexOnce(ctx context.Context, env *hostedEnv) (int, error) {
	orgs, err := env.db.ListOrgs(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	var firstErr error
	for _, o := range orgs {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		p, err := env.layout.Paths(o.ID)
		if err != nil {
			continue
		}
		st := sharelinks.Open(p.ProjectRoot)
		shares, err := st.List("", "")
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, sh := range shares {
			if sh.State(time.Now().UTC()) != sharelinks.StateActive {
				continue
			}
			tok, err := st.TokenV2(sh.ID)
			if err == nil {
				err = env.db.PutShareToken(ctx, tok, o.ID, sh.ID)
			}
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			n++
		}
	}
	return n, firstErr
}
