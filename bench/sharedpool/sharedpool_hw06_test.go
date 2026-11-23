package sharedpool

import (
	"encoding/json"
	"strings"
	"testing"
)

// Fix wave 3 (N216 fix 2): the platform's image registry login travels with
// the claim only; the assignment list carries its path and a stamp that
// changes when the login does.
func TestPlatformAssignmentRegistryOnlyInClaim(t *testing.T) {
	p := &PlatformAssignment{Name: "modal", Token: "ak-1:as-2", Registry: &RegistryLogin{Registry: "ghcr.io/acme/heimdall", Username: "bot", Token: "ghp_SECRET"}}
	pub := p.publicView()
	raw, _ := json.Marshal(pub)
	if strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "as-2") {
		t.Fatalf("assignment carries a token: %s", raw)
	}
	if pub.Registry == nil || pub.Registry.Registry != "ghcr.io/acme/heimdall" || pub.Registry.Username != "bot" {
		t.Fatalf("assignment registry = %+v", pub.Registry)
	}
	rotated := *p
	rotated.Registry = &RegistryLogin{Registry: "ghcr.io/acme/heimdall", Username: "bot", Token: "ghp_OTHER"}
	if rotated.publicView().Stamp == pub.Stamp {
		t.Fatal("the stamp did not change with the registry token")
	}
	none := &PlatformAssignment{Name: "modal", Token: "ak-1:as-2"}
	if none.publicView().Stamp == pub.Stamp || none.publicView().Registry != nil {
		t.Fatalf("no registry = %+v", none.publicView())
	}
}
