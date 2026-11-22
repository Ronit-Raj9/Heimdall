package connect

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// failTransport fails the test when a probe is sent.
type failTransport struct{ t *testing.T }

func (f failTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("a probe was sent to %s", r.URL)
	return nil, errors.New("no probe expected")
}

// errTransport answers every probe with err.
type errTransport struct{ err error }

func (e errTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, e.err }

func noLookup(t *testing.T) func(context.Context, string) ([]net.IPAddr, error) {
	return func(_ context.Context, host string) ([]net.IPAddr, error) {
		t.Errorf("%s was looked up", host)
		return nil, errors.New("no lookup expected")
	}
}

// userText is every text of a proposal a member sees.
func userText(p *Proposal) string {
	var b strings.Builder
	for _, f := range p.Failures {
		b.WriteString(f.Title + "\n" + f.Message + "\n")
	}
	for _, pr := range p.Probes {
		b.WriteString(pr.Note + "\n")
	}
	for _, c := range p.Candidates {
		b.WriteString(c.Message + "\n" + strings.Join(c.Evidence, "\n") + "\n")
	}
	return b.String()
}

func TestDetectHostedRefusesSingleLabelWithoutLookup(t *testing.T) {
	for _, raw := range []string{"http://bifrost:8080/v1/chat/completions", "http://heimdall-postgres-1:5432", "http://172.22.0.2/", "http://localhost:9000/run"} {
		prop := Detect(context.Background(), Input{Raw: raw}, Options{Hosted: true, Client: &http.Client{Transport: failTransport{t}}, Lookup: noLookup(t)})
		if len(prop.Failures) != 1 || prop.Failures[0].Code != FailPrivateAddress {
			t.Fatalf("%s: failures %+v", raw, prop.Failures)
		}
		msg := prop.Failures[0].Message
		if !strings.Contains(msg, "is not a public internet address. The shared worker only calls public addresses.") || !strings.Contains(msg, "--map auto") {
			t.Fatalf("%s: message %q", raw, msg)
		}
		if strings.Contains(msg, "resolves to") || len(prop.Probes) != 0 || prop.Best() != nil {
			t.Fatalf("%s: %+v", raw, prop)
		}
	}
}

func TestDetectHostedPrivateMessageHasNoAddress(t *testing.T) {
	// A public-looking name whose lookup answers an internal address.
	prop := Detect(context.Background(), Input{Raw: "https://agent.example.com/run"}, Options{Hosted: true, Client: &http.Client{Transport: failTransport{t}},
		Lookup: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("10.0.3.4")}}, nil
		}})
	if len(prop.Failures) == 0 || prop.Failures[0].Code != FailPrivateAddress {
		t.Fatalf("failures %+v", prop.Failures)
	}
	if txt := userText(prop); strings.Contains(txt, "10.0.3.4") || strings.Contains(txt, "resolves to") {
		t.Fatalf("text names the address: %s", txt)
	}
	// The dial guard refuses after a public answer (a rebinding name).
	prop = Detect(context.Background(), Input{Raw: "https://agent.example.com/run"}, Options{Hosted: true,
		Client: &http.Client{Transport: errTransport{errors.New("dial tcp 172.18.0.5:443: refused to connect to private or local address 172.18.0.5")}},
		Lookup: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		}})
	if txt := userText(prop); strings.Contains(txt, "172.18.0.5") {
		t.Fatalf("text names the address: %s", txt)
	}
}

func TestVerifyHostedPrivateNoAddress(t *testing.T) {
	res := Verify(context.Background(), remoteCfg("https://agent.example.com/run"), VerifyOptions{Client: plainClient(), Hosted: true,
		Lookup: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("10.0.3.4")}}, nil
		}})
	r := res.Row(RowReach)
	if r.Failure == nil || r.Failure.Code != FailPrivateAddress {
		t.Fatalf("private: %+v", r)
	}
	if strings.Contains(r.Detail+r.Failure.Message, "10.0.3.4") {
		t.Fatalf("row names the address: %+v", r)
	}
	// A single-label name is refused before any lookup.
	res = Verify(context.Background(), remoteCfg("https://registry/run"), VerifyOptions{Client: plainClient(), Hosted: true, Lookup: noLookup(t)})
	if r := res.Row(RowReach); r.Failure == nil || r.Failure.Code != FailPrivateAddress || !strings.Contains(r.Failure.Message, "registry is not a public internet address") {
		t.Fatalf("single label: %+v", r)
	}
	// The dial guard's refusal is shown without the address.
	res = Verify(context.Background(), remoteCfg("https://agent.example.com/run"), VerifyOptions{Client: plainClient(), Hosted: true,
		Lookup: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		},
		Dial: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dial tcp 172.18.0.5:443: refused to connect to private or local address 172.18.0.5")
		}})
	if r := res.Row(RowReach); r.Failure == nil || strings.Contains(r.Detail+r.Failure.Message, "172.18.0.5") {
		t.Fatalf("dial refusal: %+v", r)
	}
}

func TestDetectFallback405NotSupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()
	prop := Detect(context.Background(), Input{Raw: srv.URL + "/nope"}, Options{Client: plainClient()})
	if prop.Best() != nil {
		t.Fatalf("an endpoint that never answered is supported: %+v", prop.Best())
	}
	if len(prop.Candidates) != 1 || prop.Candidates[0].Confidence != 0 || prop.Candidates[0].Proven {
		t.Fatalf("candidates %+v", prop.Candidates)
	}
}

func TestDetectFallbackDialErrorNotSupported(t *testing.T) {
	prop := Detect(context.Background(), Input{Raw: "https://93.184.216.34/run"}, Options{Client: &http.Client{Transport: errTransport{errors.New("connection refused")}}})
	if len(prop.Candidates) == 0 {
		t.Fatalf("want the fallback candidate: %+v", prop)
	}
	if prop.Best() != nil {
		t.Fatalf("an endpoint that never answered is supported: %+v", prop.Best())
	}
	for _, c := range prop.Candidates {
		if c.Confidence != 0 || c.Proven {
			t.Fatalf("candidate %+v", c)
		}
	}
}

func TestDetectFallback401KeepsSecretQuestion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(401)
	}))
	defer srv.Close()
	prop := Detect(context.Background(), Input{Raw: srv.URL + "/chat"}, Options{Client: plainClient()})
	b := prop.Best()
	if b == nil || !hasQuestion(b, "auth_secret") || b.Proven || b.Confidence != 0 {
		t.Fatalf("want a supported, unproven candidate with the secret question: %+v", b)
	}
}

func TestDetectProvenWhenTheCodeCameBack(t *testing.T) {
	fa := &fakeAgent{}
	srv := httptest.NewServer(http.HandlerFunc(fa.handle))
	defer srv.Close()
	prop := Detect(context.Background(), Input{Raw: srv.URL + "/run"}, Options{Client: plainClient()})
	if b := prop.Best(); b == nil || !b.Proven || !b.NonceFound {
		t.Fatalf("own shape not proven: %+v", b)
	}
}

func TestDetectVoicePlatformPointsToTheForm(t *testing.T) {
	prop := Detect(context.Background(), Input{Platform: "vapi", ID: "asst-1"}, Options{Client: &http.Client{Transport: failTransport{t}}})
	if len(prop.Candidates) != 1 {
		t.Fatalf("candidates %+v", prop.Candidates)
	}
	want := "Fill in the Vapi fields under Or pick a platform, or use Import from a platform on the New agent page."
	if got := prop.Candidates[0].Message; got != want {
		t.Fatalf("message %q, want %q", got, want)
	}
}
