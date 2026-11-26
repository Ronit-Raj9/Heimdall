package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSkillImportLatestWithFilesRefusedPlainly: the latest version with
// more files than SKILL.md is refused (its zip cannot be read through the
// JSON-only source connection) with a plain sentence in error.message,
// the field the page's error text reads, and nothing is written.
func TestSkillImportLatestWithFilesRefusedPlainly(t *testing.T) {
	r, root := gatewayReposTestRouter(t, fakeBifrostRepos(skillRepoAnswers()))
	st, out := gatewayReposCall(r, "POST", APIPrefix+"/agents/codex/skills/from-bifrost", `{"skill_id": "s1", "version": "4"}`)
	var resp struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	const want = "This version has 1 more files, and Heimdall cannot download them from your Bifrost yet, so it can import only versions that hold SKILL.md alone."
	if st != 400 || resp.Error.Message != want {
		t.Fatalf("%d %s", st, out)
	}
	for _, bad := range []string{"zip", "/api/", "download.zip"} {
		if strings.Contains(resp.Error.Message, bad) {
			t.Fatalf("message names %q: %s", bad, resp.Error.Message)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "codex", "skills-pending", "review-v4")); err == nil {
		t.Fatal("a refused version was written")
	}
}
