package sandbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

// The package's other tests talk to local fakes on 127.0.0.1.
func init() { AllowPrivateEndpoints = true }

func guardOn(t *testing.T) {
	t.Helper()
	AllowPrivateEndpoints = false
	t.Cleanup(func() { AllowPrivateEndpoints = true })
}

func TestNewRefusesInternalEndpoints(t *testing.T) {
	guardOn(t)
	for _, ep := range []string{
		"http://api.daytona.example.com",          // not https
		"https://bifrost:8080",                    // compose service name
		"https://127.0.0.1:9100",                  // loopback
		"https://169.254.169.254/latest",          // metadata
		"https://10.1.2.3",                        // private
		"https://100.64.0.1",                      // carrier-grade NAT
		"https://[fd00:ec2::254]",                 // ULA metadata
		"https://registry.local",                  // internal suffix
		"https://user:pw@api.daytona.example.com", // user info
	} {
		if _, err := New(PlatformDaytona, Credentials{Token: "tok-123456", Endpoint: ep}); err == nil {
			t.Errorf("endpoint %s: accepted", ep)
		}
	}
	if _, err := New(PlatformDaytona, Credentials{Token: "tok-123456", Endpoint: "https://daytona.example.com/api"}); err != nil {
		t.Fatalf("public https endpoint refused: %v", err)
	}
}

func TestDefaultClientRefusesPrivateAtConnect(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	guardOn(t)
	// Built with the guard off would be the only way a loopback URL gets
	// here; the transport must still refuse it at connect time.
	d := NewDaytona(Credentials{Token: "tok-123456", Endpoint: srv.URL}, nil)
	err := d.Check(context.Background())
	if err == nil || !errors.Is(err, utils.ErrBlockedAddress) {
		t.Fatalf("want blocked address, got %v", err)
	}
	// Plain http is refused before dialing.
	d = NewDaytona(Credentials{Token: "tok-123456", Endpoint: "http://example.com"}, nil)
	if err := d.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "only https") {
		t.Fatalf("want https refusal, got %v", err)
	}
}

func TestHTTPErrorNeverEchoesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("internal-secret-page registry delete ok"))
	}))
	defer srv.Close()
	d := NewDaytona(Credentials{Token: "tok-123456", Endpoint: srv.URL}, nil)
	err := d.Check(context.Background())
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), "internal-secret-page") {
		t.Fatalf("error echoes the answer body: %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("error lost the status: %v", err)
	}
}
