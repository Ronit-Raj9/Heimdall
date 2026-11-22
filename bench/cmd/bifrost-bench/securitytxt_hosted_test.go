package main

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
)

// TestHostedSecurityTxtServedPastThe404Rule: on the whole hosted server
// /.well-known/security.txt answers 200 with the RFC 9116 text, anonymous,
// for GET and HEAD, and is never the app shell that unknown first path
// segments get (N150).
func TestHostedSecurityTxtServedPastThe404Rule(t *testing.T) {
	t.Setenv(SecurityContactEnv, "mailto:security@heimdall.test")
	f := newHostedFixture(t)
	r := f.do("GET", handlers.SecurityTxtPath, "", nil, nil)
	if r.status != 200 || !strings.Contains(r.body, "Contact: mailto:security@heimdall.test") || !strings.Contains(r.body, "Expires: ") {
		t.Fatalf("GET %s: %d %s", handlers.SecurityTxtPath, r.status, trim(r.body))
	}
	if strings.Contains(r.body, "<html") {
		t.Fatalf("GET %s served the app shell: %s", handlers.SecurityTxtPath, trim(r.body))
	}
	if h := f.do("HEAD", handlers.SecurityTxtPath, "", nil, nil); h.status != 200 {
		t.Fatalf("HEAD %s: %d", handlers.SecurityTxtPath, h.status)
	}
	if u := f.do("GET", "/.well-known/zz-no-such-file", "", nil, nil); u.status != 404 {
		t.Fatalf("GET an unknown /.well-known file: %d, want 404", u.status)
	}
}
