package main

import (
	"context"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/inboxtoken"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// Gap G02: the hosted inbox webhook. Per-org ingest tokens live in the
// database (sha256 only); a push to /api/bench/orgs/{org}/inbox/ingest
// lands in that org's inbox folder, as the local webhook writes its own.
func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		var tokens inboxtoken.Store
		tokensErr := "no database"
		if env.db != nil {
			tokens, tokensErr = inboxtoken.Open(env.db), ""
		}
		return handlers.NewFeedbackHandler(handlers.NewInboxTokensHandler(env.base.cfg.Server.Mode, tokens, tokensErr, env.dir, hostedOrgInbox(env))), nil
	})
}

// hostedOrgInbox opens one org's failure inbox from its project folder. The
// push is recorded under the org's webhook source when inbox/sources.yaml
// names one, else under "webhook".
func hostedOrgInbox(env *hostedEnv) handlers.InboxOrgInbox {
	return func(_ context.Context, org tenancy.Org) (*traceinbox.Inbox, *traceinbox.Source, error) {
		p, err := env.layout.Paths(org.ID)
		if err != nil {
			return nil, nil, err
		}
		if err := tenancy.EnsureProject(p, seedBenchmarks(env.base.cfg)); err != nil {
			return nil, nil, err
		}
		return orgInboxAt(p)
	}
}

// orgInboxAt opens the inbox under an org's project folder.
func orgInboxAt(p tenancy.OrgPaths) (*traceinbox.Inbox, *traceinbox.Source, error) {
	dir := filepath.Join(p.ProjectRoot, traceinbox.Dir)
	cfg, err := traceinbox.LoadConfig(filepath.Join(dir, traceinbox.SourcesFile))
	if err != nil {
		return nil, nil, err
	}
	src, ok := cfg.Webhook()
	if !ok {
		src = &traceinbox.Source{Name: "webhook", Kind: traceinbox.KindWebhook}
	}
	ib := traceinbox.New(dir, cfg, p.BenchmarksRoot())
	ib.ProjectRoot = p.ProjectRoot
	return ib, src, nil
}
