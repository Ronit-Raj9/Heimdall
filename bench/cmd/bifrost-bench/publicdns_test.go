package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestLogPublicDNS(t *testing.T) {
	t.Setenv("HEIMDALL_PUBLIC_DNS", "")
	old := publicDNSLookup
	t.Cleanup(func() { publicDNSLookup = old })

	publicDNSLookup = func(context.Context, string) error { return nil }
	if got := logPublicDNS(context.Background(), schemas.NoopLogger{}); got != "public DNS ok (1.1.1.1:53,8.8.8.8:53)" {
		t.Fatalf("ok line %q", got)
	}
	publicDNSLookup = func(context.Context, string) error { return errors.New("i/o timeout") }
	got := logPublicDNS(context.Background(), schemas.NoopLogger{})
	if !strings.HasPrefix(got, "public DNS unreachable (1.1.1.1:53,8.8.8.8:53)") || !strings.Contains(got, "HEIMDALL_PUBLIC_DNS=system") {
		t.Fatalf("unreachable line %q", got)
	}
}
