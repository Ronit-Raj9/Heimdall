package handlers

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/migration"
	"github.com/valyala/fasthttp"
)

// fakeW6Policy is fakeW6 that also takes the policy query; it knows the
// rule sets "default" and "none".
type fakeW6Policy struct {
	fakeW6
	lastPolicy string
}

func (f *fakeW6Policy) MigrationWithPolicy(ctx context.Context, expID, gradeSource, policy string, q migration.Query) (*migration.Report, error) {
	f.lastPolicy = policy
	if policy != "default" && policy != "none" {
		return nil, fmt.Errorf("%w: %s", ErrNoRuleSet, policy)
	}
	return f.Migration(ctx, expID, gradeSource, q)
}

func getMigrationStatus(t *testing.T, src MigrationSource, query string) (int, string) {
	t.Helper()
	r := router.New()
	NewMigrationHandler(testMode, src).RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
	var ctx fasthttp.RequestCtx
	ctx.Request.SetRequestURI("http://127.0.0.1" + APIPrefix + "/experiments/e_test/migration?from=old-model&to=new-model&" + query)
	ctx.Request.Header.SetMethod("GET")
	r.Handler(&ctx)
	return ctx.Response.StatusCode(), string(ctx.Response.Body())
}

func TestMigrationPolicyQueryRoute(t *testing.T) {
	src := &fakeW6Policy{}
	if status, body := getMigrationStatus(t, src, "policy=none"); status != 200 || src.lastPolicy != "none" {
		t.Fatalf("policy none: %d %s (policy %q)", status, body, src.lastPolicy)
	}
	for _, name := range []string{"absent", "../policies/default", "policies/default.yaml", ".hidden"} {
		status, body := getMigrationStatus(t, src, "policy="+url.QueryEscape(name))
		if status != 400 || !strings.Contains(body, "No release rule set named "+name+".") {
			t.Errorf("policy %q: %d %s, want 400 No release rule set named", name, status, body)
		}
	}
	// A source that cannot take the query refuses it instead of ignoring it.
	if status, _ := getMigrationStatus(t, &fakeW6{}, "policy=default"); status != 400 {
		t.Errorf("source without the policy query: %d, want 400", status)
	}
	if status, body := getMigrationStatus(t, &fakeW6{}, ""); status != 200 {
		t.Errorf("no policy query: %d %s", status, body)
	}
}
