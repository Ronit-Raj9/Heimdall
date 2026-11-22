package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/statuspage"
)

// TestHostedStatusPageIsPublic: anyone reads GET /status with no session
// or org, and an incident written with the CLI shows on it.
func TestHostedStatusPageIsPublic(t *testing.T) {
	file := filepath.Join(t.TempDir(), "incidents.json")
	t.Setenv(StatusFileEnv, file)
	f := newHostedFixture(t)
	if code := runStatus([]string{"incident", "open", "-file", file, "-title", "Evaluations start late", "-impact", "major",
		"-components", "workers", "-message", "Looking into it."}); code != exitOK {
		t.Fatalf("status incident open: %d", code)
	}
	r := f.do("GET", "/api/bench/status", "", nil, nil)
	if r.status != 200 {
		t.Fatalf("anonymous GET /status: %d %s", r.status, trim(r.body))
	}
	var page statuspage.Page
	if err := json.Unmarshal([]byte(r.body), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Incidents) != 1 || page.Incidents[0].Title != "Evaluations start late" {
		t.Fatalf("incidents %+v", page.Incidents)
	}
	ids := map[string]bool{}
	for _, c := range page.Components {
		ids[c.ID] = true
	}
	for _, want := range []string{"api", "storage", "database", "workers", "queue"} {
		if !ids[want] {
			t.Errorf("component %s missing: %+v", want, page.Components)
		}
	}
	if strings.Contains(r.body, "acme") || strings.Contains(r.body, "beta") {
		t.Fatal("the public page names an org")
	}
}

func TestStatusIncidentCLI(t *testing.T) {
	file := filepath.Join(t.TempDir(), "s", "incidents.json")
	if code := runStatus([]string{"incident", "open", "-file", file, "-title", "Database upgrade", "-scheduled-for", "2030-01-01T02:00:00Z", "-message", "Planned."}); code != exitOK {
		t.Fatalf("open: %d", code)
	}
	list, err := statuspage.NewStore(file).List()
	if err != nil || len(list) != 1 || list[0].Status != statuspage.StatusScheduled || list[0].Impact != statuspage.ImpactMaintenance {
		t.Fatalf("scheduled incident: %+v %v", list, err)
	}
	id := list[0].ID
	if code := runStatus([]string{"incident", "resolve", "-file", file, "-id", id, "-message", "Done."}); code != exitOK {
		t.Fatalf("resolve: %d", code)
	}
	if list, _ = statuspage.NewStore(file).List(); list[0].ResolvedAt == nil {
		t.Fatal("not resolved")
	}
	if code := runStatus([]string{"incident", "open", "-file", file, "-title", "x"}); code == exitOK {
		t.Fatal("open without a message must fail")
	}
	if code := runStatus([]string{"incident", "remove", "-file", file, "-id", id}); code != exitOK {
		t.Fatalf("remove: %d", code)
	}
	if code := runStatus([]string{"bogus"}); code != exitUsage {
		t.Fatalf("usage: %d", code)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
}
