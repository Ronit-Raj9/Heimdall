package imagepub

import (
	"os"
	"path/filepath"
	"testing"
)

// An agent file in the checkout that the publisher's user cannot read (a
// private file written by its owner with mode 0600) is skipped: the listing
// still names every readable platform agent.
func TestPlatformImagesSkipsUnreadableAgentFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every file")
	}
	checkout := t.TempDir()
	writeFile(t, filepath.Join(checkout, "agents", "codex.yaml"), "id: codex\nbuild:\n  tag: bench/agent-codex:1\n")
	private := filepath.Join(checkout, "agents", "private.yaml")
	writeFile(t, private, "id: private\nbuild:\n  tag: bench/agent-private:1\n")
	if err := os.Chmod(private, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(private, 0o600) })

	set, err := PlatformImages(checkout, "")
	if err != nil {
		t.Fatalf("an unreadable agent file failed the whole listing: %v", err)
	}
	if _, ok := set["bench/agent-codex:1"]; !ok {
		t.Fatalf("the readable agent is missing: %v", set)
	}
	if _, ok := set["bench/agent-private:1"]; ok {
		t.Fatalf("the unreadable agent was listed: %v", set)
	}
}
