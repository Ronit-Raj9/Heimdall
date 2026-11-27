package sandbox

import (
	"strings"
	"testing"
)

// TestWorkerTokenHintsSayAccessToken (round 2, Q-80): a worker platform's
// token is an access token, never called an API key, so it is not mixed up
// with the model keys of Settings > Model keys.
func TestWorkerTokenHintsSayAccessToken(t *testing.T) {
	for _, name := range []string{PlatformDaytona, PlatformE2B} {
		info, ok := Lookup(name)
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		if !strings.Contains(info.TokenHint, "access token") || strings.Contains(strings.ToLower(info.TokenHint), "api key") {
			t.Fatalf("%s hint %q", name, info.TokenHint)
		}
	}
}
