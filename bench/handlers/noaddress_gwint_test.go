package handlers

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// closedLoopbackURL is an http URL on a loopback port nothing listens on, so
// a call fails with a dial error that names 127.0.0.1.
func closedLoopbackURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return "http://" + addr + "/agent"
}

func noAddressIn(t *testing.T, what, text string) {
	t.Helper()
	if strings.Contains(text, "127.0.0.1") || !strings.Contains(text, "a private address") {
		t.Fatalf("%s = %q, want the address hidden", what, text)
	}
}

// TestAgentTestErrorHasNoAddress: Test my agent (agenttest.go) shows a dial
// error without the address the endpoint resolved to (fix wave 2, N104).
func TestAgentTestErrorHasNoAddress(t *testing.T) {
	h := &agentTestHandler{client: endpointTestClient, now: time.Now}
	res := h.run(context.Background(), &schemas.AgentConfig{Adapter: "remote_http", Remote: &schemas.RemoteAgent{URL: closedLoopbackURL(t)}})
	if res.OK || res.Error == "" {
		t.Fatalf("want a failed test: %+v", res)
	}
	noAddressIn(t, "error", res.Error)
}

// TestAgentConnectMapErrorHasNoAddress: a mapped endpoint (agentconnectmap.go,
// runMapped) shows a dial error without the address (N104).
func TestAgentConnectMapErrorHasNoAddress(t *testing.T) {
	h := &agentTestHandler{client: endpointTestClient, now: time.Now}
	h.env.Services.Hosted = false
	res := h.run(context.Background(), &schemas.AgentConfig{Adapter: "remote_http",
		Remote: &schemas.RemoteAgent{URL: closedLoopbackURL(t), Mapping: &schemas.RemoteMapping{Preset: "custom"}}})
	if res.OK || res.Error == "" {
		t.Fatalf("want a failed test: %+v", res)
	}
	noAddressIn(t, "error", res.Error)
}

// TestHostedPullErrorHasNoAddress: a hosted pull (liveinbox_pull.go) that is
// refused at dial time reports the refusal without the address (N104), and
// errors.Is still finds the guard's error.
func TestHostedPullErrorHasNoAddress(t *testing.T) {
	_, err := HostedPullClient().Get("https://10.20.30.40:1/api/logs")
	var ue *url.Error
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a url.Error", err)
	}
	if !errors.Is(err, utils.ErrBlockedAddress) {
		t.Fatalf("err = %v, want utils.ErrBlockedAddress", err)
	}
	if msg := ue.Err.Error(); strings.Contains(msg, "10.20.30.40") || !strings.Contains(msg, "a private address") {
		t.Fatalf("pull error = %q", msg)
	}
}
