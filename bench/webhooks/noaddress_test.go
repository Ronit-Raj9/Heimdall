package webhooks

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// failingTransport fails every request the way the guarded dialer does
// when a public name resolves to an internal address.
type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("dial tcp 10.0.0.1:443: refused to connect to private or local address 10.0.0.1")
}

func TestWebhookDeliveryErrorHasNoAddress(t *testing.T) {
	c := &http.Client{Transport: failingTransport{}}
	d := Sender{Client: c, Attempts: 1}.Send(context.Background(), Hook{ID: "x", URL: "https://hooks.example.com/in", Secret: "s"}, NewEvent(EventPing, "", nil))
	if d.OK || d.Error == "" {
		t.Fatalf("want a failed delivery: %+v", d)
	}
	if strings.Contains(d.Error, "10.0.0.1") {
		t.Fatalf("delivery error names the address: %q", d.Error)
	}
	// The guard itself names no address either.
	err := GuardedDialer(0).Control("tcp", "172.18.0.5:443", nil)
	if err == nil || strings.Contains(err.Error(), "172.18.0.5") {
		t.Fatalf("guard error %v", err)
	}
	if GuardedDialer(0).Resolver == nil {
		t.Fatal("the guarded dialer must resolve through the public resolver")
	}
}
