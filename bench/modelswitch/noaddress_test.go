package modelswitch

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

// TestGatewayRefusalHasNoAddress: the apply client's refusal of a private
// address reaches the member without the address (fix wave 2, N104), and
// errors.Is still sees ErrGatewayRefused.
func TestGatewayRefusalHasNoAddress(t *testing.T) {
	_, err := GatewayClient(true).Get("https://10.1.2.3:1/api/governance/routing-rules")
	var ue *url.Error
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a url.Error", err)
	}
	if !errors.Is(err, ErrGatewayRefused) {
		t.Fatalf("err = %v, want ErrGatewayRefused", err)
	}
	if msg := ue.Err.Error(); strings.Contains(msg, "10.1.2.3") || !strings.Contains(msg, "a private address") {
		t.Fatalf("refusal text = %q", msg)
	}
}
