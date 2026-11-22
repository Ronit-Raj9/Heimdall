package utils

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBlockedIP(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.0.0.1", "172.16.5.4", "192.168.1.1", "169.254.169.254", "100.64.0.1",
		"100.127.255.254", "0.0.0.0", "0.1.2.3", "255.255.255.255", "240.0.0.1", "198.18.0.1", "::1", "::",
		"fd00:ec2::254", "fe80::1", "::ffff:10.0.0.1", "::ffff:127.0.0.1", "64:ff9b::a00:1", "224.0.0.1"}
	for _, s := range blocked {
		if !BlockedIP(net.ParseIP(s)) {
			t.Errorf("%s: not blocked", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "8.8.8.8", "100.128.0.1", "2606:4700:4700::1111", "::ffff:1.1.1.1"} {
		if BlockedIP(net.ParseIP(s)) {
			t.Errorf("%s: blocked", s)
		}
	}
	if !BlockedIP(nil) {
		t.Error("nil: not blocked")
	}
}

func TestCheckPublicURL(t *testing.T) {
	bad := []string{"", "http://example.com", "ftp://example.com", "https://bifrost:8080", "https://localhost",
		"https://a.localhost", "https://127.0.0.1", "https://[::1]", "https://x.internal", "https://u:p@example.com",
		"https://169.254.169.254", "https://svc.default.svc", "/relative"}
	for _, s := range bad {
		if CheckPublicURL(s, false) == nil {
			t.Errorf("%q: accepted", s)
		}
	}
	for _, s := range []string{"https://example.com", "https://api.daytona.io/v1", "https://1.1.1.1:8443"} {
		if err := CheckPublicURL(s, false); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	if err := CheckPublicURL("http://example.com", true); err != nil {
		t.Errorf("http with allowHTTP: %v", err)
	}
}

func TestGuardedTransportRefusesLoopbackAtConnect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	c := &http.Client{Transport: GuardedTransport(), Timeout: 5 * time.Second}
	_, err := c.Get(srv.URL)
	if err == nil || !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("want blocked, got %v", err)
	}
	if _, err := GuardedDial(context.Background(), "tcp", srv.Listener.Addr().String(), time.Second); err == nil ||
		!strings.Contains(err.Error(), "private or local") {
		t.Fatalf("GuardedDial: want blocked, got %v", err)
	}
}
