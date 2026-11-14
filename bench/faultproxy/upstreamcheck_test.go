package faultproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// checkSpec is a minimal OpenAI-format provider at base whose key is in env.
func checkSpec(name, base, env string) ProviderSpec {
	return ProviderSpec{Name: name, BaseURL: base, KeyEnv: env, Auth: AuthBearer, Format: FormatOpenAI,
		Message: []string{"POST /v1/chat/completions"}}
}

// closedAddr returns a loopback address nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// silentAddr returns a loopback address that accepts TCP connections and
// never answers, so a TLS handshake against it hangs until its deadline.
func silentAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var conns []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns = append(conns, c)
		}
	}()
	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Errorf("close silent listener: %v", err)
		}
		<-done
		for _, c := range conns {
			if err := c.Close(); err != nil {
				t.Logf("close silent conn: %v", err)
			}
		}
	})
	return ln.Addr().String()
}

// countingServer counts the HTTP requests it receives; the check must send
// none.
func countingServer(t *testing.T, tlsOn bool) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTeapot)
	})
	srv := httptest.NewServer(h)
	if tlsOn {
		srv.Close()
		srv = httptest.NewTLSServer(h)
	}
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestCheckUpstreamsEndpoint(t *testing.T) {
	tlsUp, tlsHits := countingServer(t, true)
	plainUp, plainHits := countingServer(t, false)
	refused := closedAddr(t)
	keys := map[string]string{"TLS_KEY": "sk-tls-secret", "PLAIN_KEY": "sk-plain-secret", "REFUSED_KEY": "sk-refused-secret"}
	specs := MergeProviders(DefaultProviders(), []ProviderSpec{
		checkSpec("tlsok", tlsUp.URL+"/v1", "TLS_KEY"),
		checkSpec("plainok", plainUp.URL, "PLAIN_KEY"),
		checkSpec("refused", "https://"+refused, "REFUSED_KEY"),
		checkSpec("nokey", "https://"+refused, "NO_KEY"),
	})
	env := newTestEnvConfig(t, Config{Providers: specs, Getenv: func(k string) string { return keys[k] }})
	env.srv.client.Transport.(*http.Transport).TLSClientConfig = tlsUp.Client().Transport.(*http.Transport).TLSClientConfig

	rep, err := env.client.CheckUpstreams(context.Background())
	if err != nil {
		t.Fatalf("CheckUpstreams: %v", err)
	}
	if rep.CheckedAt.IsZero() {
		t.Error("report has no checked_at")
	}
	tlsHost := strings.TrimPrefix(tlsUp.URL, "https://")
	plainHost := strings.TrimPrefix(plainUp.URL, "http://")
	want := []struct {
		provider, host string
		ok             bool
		errPart        string
	}{
		// Table order; anthropic and openai are scripted, ollama is keyless,
		// nokey has no key: none of them is listed.
		{"tlsok", tlsHost, true, ""},
		{"plainok", plainHost, true, ""},
		{"refused", refused, false, "connection refused"},
	}
	if len(rep.Upstreams) != len(want) {
		t.Fatalf("upstreams = %+v, want %d entries", rep.Upstreams, len(want))
	}
	for i, w := range want {
		t.Run(w.provider, func(t *testing.T) {
			got := rep.Upstreams[i]
			if got.Provider != w.provider || got.Host != w.host || got.OK != w.ok || got.Skipped != "" {
				t.Fatalf("check = %+v, want provider %s host %s ok %v", got, w.provider, w.host, w.ok)
			}
			if w.ok && (got.Error != "" || got.LatencyMS <= 0) {
				t.Fatalf("ok check has error %q or latency %v", got.Error, got.LatencyMS)
			}
			if !w.ok && !strings.Contains(got.Error, w.errPart) {
				t.Fatalf("error %q, want it to contain %q", got.Error, w.errPart)
			}
		})
	}
	if tlsHits.Load() != 0 || plainHits.Load() != 0 {
		t.Fatalf("the check sent HTTP requests: tls %d, plain %d", tlsHits.Load(), plainHits.Load())
	}

	req, err := http.NewRequest(http.MethodGet, env.ctrl.URL+"/upstreams", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.srv.ControlToken())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(resp.Body)
	if cerr := resp.Body.Close(); cerr != nil {
		t.Fatal(cerr)
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if strings.Contains(string(raw), k) {
			t.Fatalf("response carries a provider key: %s", raw)
		}
	}
}

func TestUpstreamDialerCheck(t *testing.T) {
	tlsUp, hits := countingServer(t, true)
	trusted := tlsUp.Client().Transport.(*http.Transport).TLSClientConfig
	failDNS := &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("dns server unreachable")
	}}
	viaProxy := func(*http.Request) (*url.URL, error) { return url.Parse("http://user:pw@corp-proxy:3128") }
	badProxy := func(*http.Request) (*url.URL, error) { return nil, errors.New("bad HTTPS_PROXY") }
	noProxy := func(*http.Request) (*url.URL, error) { return nil, nil }
	tests := []struct {
		name     string
		dialer   upstreamDialer
		base     string
		wantOK   bool
		wantHost string
		errPart  string
		skipPart string
	}{
		{name: "trusted tls", dialer: upstreamDialer{tls: trusted, proxy: noProxy}, base: tlsUp.URL, wantOK: true,
			wantHost: strings.TrimPrefix(tlsUp.URL, "https://")},
		{name: "untrusted certificate", dialer: upstreamDialer{}, base: tlsUp.URL,
			wantHost: strings.TrimPrefix(tlsUp.URL, "https://"), errPart: "certificate"},
		{name: "dns failure", dialer: upstreamDialer{resolver: failDNS}, base: "https://api.openai.test/v1",
			wantHost: "api.openai.test:443", errPart: "lookup api.openai.test"},
		{name: "default http port", dialer: upstreamDialer{resolver: failDNS}, base: "http://upstream.test",
			wantHost: "upstream.test:80", errPart: "lookup upstream.test"},
		{name: "through an http proxy", dialer: upstreamDialer{proxy: viaProxy}, base: "https://api.openai.com",
			wantHost: "api.openai.com:443", skipPart: "corp-proxy:3128"},
		{name: "broken proxy setting", dialer: upstreamDialer{proxy: badProxy}, base: "https://api.openai.com",
			wantHost: "api.openai.com:443", errPart: "bad HTTPS_PROXY"},
		{name: "handshake timeout", dialer: upstreamDialer{timeout: 200 * time.Millisecond}, base: "https://" + silentAddr(t),
			errPart: "deadline exceeded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := tt.dialer
			if d.timeout == 0 {
				d.timeout = 5 * time.Second
			}
			d.logger = schemas.NoopLogger{}
			got := d.check(context.Background(), "p", tt.base)
			if got.Provider != "p" || got.OK != tt.wantOK || (tt.wantHost != "" && got.Host != tt.wantHost) {
				t.Fatalf("check = %+v, want ok %v host %s", got, tt.wantOK, tt.wantHost)
			}
			if !strings.Contains(got.Error, tt.errPart) || (tt.errPart == "") != (got.Error == "") {
				t.Fatalf("error %q, want it to contain %q", got.Error, tt.errPart)
			}
			if !strings.Contains(got.Skipped, tt.skipPart) || (tt.skipPart == "") != (got.Skipped == "") {
				t.Fatalf("skipped %q, want it to contain %q", got.Skipped, tt.skipPart)
			}
			if strings.Contains(got.Skipped, "pw") {
				t.Fatalf("skip reason carries proxy credentials: %q", got.Skipped)
			}
		})
	}
	if hits.Load() != 0 {
		t.Fatalf("the check sent %d HTTP requests", hits.Load())
	}
}

func TestCheckUpstreamsConcurrent(t *testing.T) {
	keys := map[string]string{"A_KEY": "a", "B_KEY": "b", "C_KEY": "c"}
	specs := []ProviderSpec{
		checkSpec("a", "https://"+silentAddr(t), "A_KEY"),
		checkSpec("b", "https://"+silentAddr(t), "B_KEY"),
		checkSpec("c", "https://"+silentAddr(t), "C_KEY"),
	}
	env := newTestEnvConfig(t, Config{Providers: specs, Getenv: func(k string) string { return keys[k] },
		Slots: []SlotConfig{{Name: "a-1", Auth: "a-1", Provider: "a"}}})
	const timeout = 300 * time.Millisecond
	start := time.Now()
	rep := env.srv.checkUpstreams(context.Background(), upstreamDialer{timeout: timeout, logger: schemas.NoopLogger{}})
	elapsed := time.Since(start)
	if len(rep.Upstreams) != 3 {
		t.Fatalf("upstreams = %+v", rep.Upstreams)
	}
	for _, c := range rep.Upstreams {
		if c.OK || c.Error == "" {
			t.Fatalf("hanging upstream reported %+v", c)
		}
	}
	if elapsed >= 3*timeout {
		t.Fatalf("three hanging upstreams took %v; checks are not concurrent or not bounded by %v", elapsed, timeout)
	}
}

func TestHostPort(t *testing.T) {
	tests := []struct{ raw, want string }{
		{"https://api.openai.com", "api.openai.com:443"},
		{"https://generativelanguage.googleapis.com/v1beta", "generativelanguage.googleapis.com:443"},
		{"http://ollama:11434", "ollama:11434"},
		{"http://upstream", "upstream:80"},
		{"https://[::1]:8443/x", "[::1]:8443"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			u, err := url.Parse(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got := hostPort(u); got != tt.want {
				t.Fatalf("hostPort(%s) = %s, want %s", tt.raw, got, tt.want)
			}
		})
	}
}
