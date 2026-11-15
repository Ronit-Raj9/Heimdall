package utils

import "testing"

func TestRedactSecrets(t *testing.T) {
	got := RedactSecrets("Authorization: Bearer abc token=xyz password:secret safe")
	if got != "Authorization=[redacted] token=[redacted] password=[redacted] safe" {
		t.Fatalf("redacted = %q", got)
	}
}
