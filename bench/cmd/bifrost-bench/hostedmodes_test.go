package main

import (
	"context"
	"encoding/json"
	"flag"
	"sort"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/jobs"
)

// TestHostedOfflineKeepsExperimentRun: hosted -offline must still register
// experiment.run, or start and resume answer "unknown job kind" (audit 8-C-03).
func TestHostedOfflineKeepsExperimentRun(t *testing.T) {
	f := newHostedFixture(t)
	env := newEnv(context.Background(), f.env.base, f.d, true)
	h := hostedHub(env)
	_, err := h.runner.Enqueue(context.Background(), jobs.NewJob{OrgID: f.acme.ID, Kind: kindExperimentRun, Params: json.RawMessage(`{}`)})
	if err != nil && strings.Contains(err.Error(), "unknown job kind") {
		t.Fatalf("hosted offline: %v", err)
	}
}

// TestHostedRefusesScheduleFlags: hosted serve runs no schedule daemon, so
// the schedule flags fail startup instead of being dropped (audit 8-C-04).
func TestHostedRefusesScheduleFlags(t *testing.T) {
	for _, args := range [][]string{{"-schedules"}, {"-schedules-file", "s.yaml"}} {
		fs := flag.NewFlagSet("serve", flag.ContinueOnError)
		x := addServeExtras(fs)
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		if err := x.checkHosted(); err == nil {
			t.Errorf("%v: checkHosted accepted schedule flags", args)
		}
	}
	x := addServeExtras(flag.NewFlagSet("serve", flag.ContinueOnError))
	if err := x.checkHosted(); err != nil {
		t.Errorf("no flags: %v", err)
	}
}

// TestHostedExtensionFeaturesWired: the extension reads that work over an
// org's stored data no longer answer 503 "not available" in hosted mode
// (audit 8-C-01).
func TestHostedExtensionFeaturesWired(t *testing.T) {
	f := newHostedFixture(t)
	for _, path := range []string{
		"/api/bench/suites/coding-core/export?format=harbor",
		"/api/bench/experiments/" + f.aExp + "/export?format=csv",
		"/api/bench/experiments/" + f.aExp + "/compliance",
		"/api/bench/suites/coding-core/calibration",
		"/api/bench/monitors/certifications",
		"/api/bench/inbox",
		"/api/bench/canaries",
		"/api/bench/livescore",
	} {
		r := f.do("GET", path, "alice", nil, nil)
		if r.status == 503 || r.status == 500 {
			t.Errorf("GET %s: %d %s", path, r.status, trim(r.body))
		}
	}
}

// TestHostedOpenAPIListsPlatformRoutes: the hosted document lists the
// platform routes beside the org routes and describes hosted credentials,
// not the local write and worker tokens (audit 8-C-05).
func TestHostedOpenAPIListsPlatformRoutes(t *testing.T) {
	f := newHostedFixture(t)
	r := f.do("GET", "/api/bench/openapi.json", "alice", nil, nil)
	if r.status != 200 {
		t.Fatalf("openapi: %d %s", r.status, trim(r.body))
	}
	var doc struct {
		Paths      map[string]any `json:"paths"`
		Components struct {
			SecuritySchemes map[string]any `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal([]byte(r.body), &doc); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/bench/auth/me", "/api/bench/orgs", "/api/bench/jobs", "/api/bench/runners", "/api/bench/suites"} {
		if _, ok := doc.Paths[p]; !ok {
			t.Errorf("hosted OpenAPI lacks %s", p)
		}
	}
	for _, s := range []string{"session", "apiToken", "runnerToken"} {
		if _, ok := doc.Components.SecuritySchemes[s]; !ok {
			t.Errorf("hosted OpenAPI lacks security scheme %s", s)
		}
	}
	if _, ok := doc.Components.SecuritySchemes["writeToken"]; ok {
		t.Error("hosted OpenAPI describes the local write token")
	}
}

// TestHostedOpenAPISecurityFollowsPolicy covers QC2-20-03: hosted
// operations describe the authentication the tenancy dispatcher enforces.
// Account-only writes take the session (with CSRF), org reads take a
// session without CSRF or an API token, and routes the hosted policy
// denies are not advertised.
func TestHostedOpenAPISecurityFollowsPolicy(t *testing.T) {
	f := newHostedFixture(t)
	r := f.do("GET", "/api/bench/openapi.json", "alice", nil, nil)
	if r.status != 200 {
		t.Fatalf("openapi: %d %s", r.status, trim(r.body))
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Security []map[string][]string `json:"security"`
		} `json:"paths"`
	}
	if err := json.Unmarshal([]byte(r.body), &doc); err != nil {
		t.Fatal(err)
	}
	schemes := func(path, method string) []string {
		op, ok := doc.Paths[path][method]
		if !ok {
			t.Fatalf("%s %s missing", method, path)
		}
		var out []string
		for _, req := range op.Security {
			var names []string
			for k := range req {
				names = append(names, k)
			}
			sort.Strings(names)
			out = append(out, strings.Join(names, "+"))
		}
		return out
	}
	for _, tt := range []struct {
		path, method string
		want         []string
	}{
		{"/api/bench/auth/password", "post", []string{"csrf+session"}},
		{"/api/bench/auth/switch-org", "post", []string{"csrf+session"}},
		{"/api/bench/orgs", "post", []string{"csrf+session"}},
		{"/api/bench/runners", "get", []string{"session", "apiToken"}},
		{"/api/bench/auth/login", "post", nil},
	} {
		if got := schemes(tt.path, tt.method); strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("%s %s security %v, want %v", tt.method, tt.path, got, tt.want)
		}
	}
	if _, ok := doc.Paths["/api/bench/canaries/{name}/run"]["post"]; ok {
		t.Error("hosted OpenAPI advertises the denied canary run")
	}
	if _, ok := doc.Paths["/api/bench/livescore/pull"]["post"]; ok {
		t.Error("hosted OpenAPI advertises the denied live-score pull")
	}
}
