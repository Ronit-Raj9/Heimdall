package server

import (
	"context"
	"errors"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

type closingArea struct{ closed int }

func (a *closingArea) RegisterRoutes(*router.Router, ...lib.Middleware) {}
func (a *closingArea) Close()                                           { a.closed++ }

type noOrgs struct{}

func (noOrgs) OrgByID(context.Context, string) (tenancy.Org, error) {
	return tenancy.Org{}, tenancy.ErrOrgNotFound
}
func (noOrgs) OrgBySlug(context.Context, string) (tenancy.Org, error) {
	return tenancy.Org{}, tenancy.ErrOrgNotFound
}
func (noOrgs) MemberRole(context.Context, string, string) (auth.Role, error) {
	return "", errors.New("no member")
}

// A platform area with live streams (the jobs handler) is closed with the
// hosted server, before HTTP shutdown waits for requests (audit 4-C-16).
func TestHostedClosesPlatformAreas(t *testing.T) {
	cfg := testConfig()
	cfg.Auth = lib.AuthConfig{Mode: lib.AuthModeHosted, PublicURL: "https://heimdall.test"}
	reg := tenancy.NewRegistry(tenancy.Layout{Hosted: true, StorageRoot: t.TempDir()}, nil, tenancy.Options{})
	area := &closingArea{}
	s, err := NewHosted(cfg, Hosted{Auth: func(h fasthttp.RequestHandler) fasthttp.RequestHandler { return h }, Directory: noOrgs{},
		Registry: reg, Platform: []RouteRegistrar{area}})
	if err != nil {
		t.Fatal(err)
	}
	s.closeHandlers()
	if area.closed != 1 {
		t.Fatalf("platform area closed %d times", area.closed)
	}
}
