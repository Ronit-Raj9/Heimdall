package runners

import (
	"strings"
	"testing"
)

// TestInstallCommandUsesSavedTokenFile covers N225: the start line and the
// install line read the token from the one file the setup step saves.
func TestInstallCommandUsesSavedTokenFile(t *testing.T) {
	s := New(nil, Options{PublicURL: "https://heimdall.example.com"})
	want := "~/.heimdall-runner-token"
	if !strings.Contains(s.StartCommand("gpu", "box"), "-token-file "+want+" ") {
		t.Fatalf("start %q", s.StartCommand("gpu", "box"))
	}
	if !strings.Contains(s.InstallCommand("gpu", "box"), "--token-file "+want+" ") {
		t.Fatalf("install %q", s.InstallCommand("gpu", "box"))
	}
}
