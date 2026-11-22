package utils

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHidePrivateAddresses(t *testing.T) {
	cases := map[string]string{
		"dial tcp 10.1.2.3:443: refused":         "dial tcp a private address:443: refused",
		"resolves to 172.16.0.9":                 "resolves to a private address",
		"resolves to 172.22.0.2 now":             "resolves to a private address now",
		"at 192.168.1.10":                        "at a private address",
		"loopback 127.0.0.1:8080":                "loopback a private address:8080",
		"ipv6 [::1]:80":                          "ipv6 a private address:80",
		"unique local fd00::1 here":              "unique local a private address here",
		"cgnat 100.64.3.4":                       "cgnat a private address",
		"public 8.8.8.8 stays":                   "public 8.8.8.8 stays",
		"at 12:30:45 nothing":                    "at 12:30:45 nothing",
		"version 1.2.3 stays":                    "version 1.2.3 stays",
		"metadata 169.254.169.254 is link-local": "metadata a private address is link-local",
	}
	for in, want := range cases {
		if got := HidePrivateAddresses(in); got != want {
			t.Errorf("HidePrivateAddresses(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGuardControlErrorHasNoAddress(t *testing.T) {
	err := GuardControl("tcp", "10.0.3.4:443", nil)
	if err == nil || !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("want a blocked address error, got %v", err)
	}
	if strings.Contains(err.Error(), "10.0.3.4") {
		t.Fatalf("error names the address: %v", err)
	}
	if err := CheckPublicHost("172.22.0.2"); err == nil || strings.Contains(err.Error(), "172.22") {
		t.Fatalf("CheckPublicHost error names the address: %v", err)
	}
}

func TestPublicResolverSystem(t *testing.T) {
	t.Setenv(PublicDNSEnv, "system")
	if r := PublicResolver(); r != nil {
		t.Fatal("system must keep the default resolver")
	}
	t.Setenv(PublicDNSEnv, "")
	if got := strings.Join(PublicDNSServers(), ","); got != "1.1.1.1:53,8.8.8.8:53" {
		t.Fatalf("default servers %q", got)
	}
	t.Setenv(PublicDNSEnv, "not-an-address, 9.9.9.9:53")
	if got := strings.Join(PublicDNSServers(), ","); got != "9.9.9.9:53" {
		t.Fatalf("configured servers %q", got)
	}
	t.Setenv(PublicDNSEnv, "nope")
	if got := strings.Join(PublicDNSServers(), ","); got != "1.1.1.1:53,8.8.8.8:53" {
		t.Fatalf("no valid entry must fall back to the default, got %q", got)
	}
}

// dnsStub answers every A query with 93.184.216.34 over UDP on 127.0.0.1.
func dnsStub(t *testing.T) (addr string, hits *atomic.Int32) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	hits = &atomic.Int32{}
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			hits.Add(1)
			q := buf[:n]
			if n < 12 {
				continue
			}
			// Find the end of the question (name, type, class).
			i := 12
			for i < n && q[i] != 0 {
				i += int(q[i]) + 1
			}
			i += 5
			if i > n {
				continue
			}
			qtype := binary.BigEndian.Uint16(q[i-4 : i-2])
			resp := make([]byte, 0, i+16)
			resp = append(resp, q[0], q[1], 0x81, 0x80, 0, 1)
			if qtype == 1 {
				resp = append(resp, 0, 1)
			} else {
				resp = append(resp, 0, 0)
			}
			resp = append(resp, 0, 0, 0, 0)
			resp = append(resp, q[12:i]...)
			if qtype == 1 {
				resp = append(resp, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 93, 184, 216, 34)
			}
			_, _ = pc.WriteTo(resp, from)
		}
	}()
	return pc.LocalAddr().String(), hits
}

func TestPublicResolverUsesConfiguredServers(t *testing.T) {
	addr, hits := dnsStub(t)
	t.Setenv(PublicDNSEnv, addr)
	r := PublicResolver()
	if r == nil {
		t.Fatal("want a public resolver")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := LookupIPAddr(ctx, r, "only-the-stub-knows.example")
	if err != nil {
		t.Fatalf("lookup through the stub: %v", err)
	}
	if len(ips) == 0 || ips[0].IP.String() != "93.184.216.34" {
		t.Fatalf("answer %v", ips)
	}
	if hits.Load() == 0 {
		t.Fatal("the configured server was never asked")
	}
	if GuardedDialer(time.Second).Resolver == nil {
		t.Fatal("the guarded dialer must use the public resolver")
	}
}
