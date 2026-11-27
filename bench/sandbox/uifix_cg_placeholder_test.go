package sandbox

import "testing"

// TestEndpointPlaceholder: every platform with an endpoint field gives an
// example value (fix wave X-60).
func TestEndpointPlaceholder(t *testing.T) {
	for _, name := range []string{PlatformDaytona, PlatformE2B, PlatformModal} {
		info, ok := Lookup(name)
		if !ok || info.EndpointLabel == "" || info.EndpointPlaceholder == "" {
			t.Fatalf("%s: %+v", name, info)
		}
	}
}
