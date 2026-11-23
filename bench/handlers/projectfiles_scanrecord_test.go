package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/projectfs"
)

// TestProjectFilesRefuseScanRecordWrites: the web file editor cannot write or
// delete a skill scan record (agents/<id>/skill-scans/), which admits a
// skill to runs; reading one still works and skill folders stay editable.
func TestProjectFilesRefuseScanRecordWrites(t *testing.T) {
	store, err := projectfs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := []byte(`{"decision":"blocked","tree_sha256":"abc"}`)
	if _, err := store.Write(context.Background(), projectfs.AreaAgents, "x/skill-scans/y.json", record, projectfs.WriteOpts{System: true}); err != nil {
		t.Fatal(err)
	}
	c := newProjectFilesClient(t, store)
	for _, tc := range []struct{ method, path string }{
		{"PUT", "/project/files/agents/x/skill-scans/y.json"},
		{"PUT", "/project/files/agents/x/skill-scans/new.json"},
		{"PUT", "/project/files/agents/x/skills/y/..%2F..%2Fskill-scans/y.json"},
		{"DELETE", "/project/files/agents/x/skill-scans/y.json"},
		{"DELETE", "/project/files/agents/x/skill-scans"},
	} {
		resp := c.do(t, tc.method, APIPrefix+tc.path, []byte(`{"decision":"admitted"}`), authed("application/json"))
		if resp.StatusCode() != 403 || !strings.Contains(string(resp.Body()), "Scan records are written by Heimdall only.") {
			t.Fatalf("%s %s = %d %s, want 403", tc.method, tc.path, resp.StatusCode(), resp.Body())
		}
	}
	resp := c.do(t, "GET", APIPrefix+"/project/files/agents/x/skill-scans/y.json", nil, nil)
	if resp.StatusCode() != 200 || !strings.Contains(string(resp.Body()), "blocked") {
		t.Fatalf("GET record = %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := c.do(t, "PUT", APIPrefix+"/project/files/agents/x/skills/y/SKILL.md", []byte("# y\n"), authed("text/plain")); resp.StatusCode() != 200 {
		t.Fatalf("PUT skill file = %d %s", resp.StatusCode(), resp.Body())
	}
}
