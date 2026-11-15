package traceimport

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactText(t *testing.T) {
	tests := []struct {
		name, in, kind, keep string
	}{
		{"email", "mail jane.doe@example.com now", RedactEmail, "mail "},
		{"phone intl", "call +1 415 555 0134 today", RedactPhone, "call "},
		{"phone us", "call (415) 555-0134", RedactPhone, "call "},
		{"long digits", "account 123456789012 here", RedactLongNumber, "account "},
		{"card", "card 4111 1111 1111 1111 ok", RedactLongNumber, "card "},
		{"bearer", "sent Bearer abcdefghijklmnop123", RedactBearer, "sent "},
		{"header", "Authorization: Token abc123", RedactHeader, "Authorization: "},
		{"cookie header", "Cookie: sid=abc; theme=dark", RedactHeader, "Cookie: "},
		{"json header", `{"x-api-key": "k-123456"}`, RedactHeader, `{"x-api-key": "`},
		{"api key", "key " + plantedSecret, RedactAPIKey, "key "},
		{"aws", "AKIA" + "ABCDEFGHIJKLMNOP", RedactAPIKey, ""},
		{"jwt", "eyJhbGciOi.eyJzdWIiOiIx.c2lnbmF0dXJl", RedactJWT, ""},
		{"secret assignment", "DB_PASSWORD=hunter2hunter2", RedactSecret, "DB_PASSWORD="},
		{"private key", "-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----", RedactPrivateKey, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRedactor()
			out := r.Text(tc.in)
			if r.Report()[tc.kind] == 0 || !strings.Contains(out, "[REDACTED "+tc.kind+"]") || !strings.HasPrefix(out, tc.keep) {
				t.Errorf("Text(%q) = %q, report %v", tc.in, out, r.Report())
			}
		})
	}
	r := NewRedactor()
	for _, safe := range []string{"order A-1001 on 2026-11-15 at 10:30", "max_tokens: 1000", "IP 192.168.1.10", "version 1.2.3"} {
		if out := r.Text(safe); out != safe {
			t.Errorf("Text(%q) = %q, want it unchanged", safe, out)
		}
	}
	if r.Report().Total() != 0 || r.Report().String() != "nothing" {
		t.Errorf("report = %v", r.Report())
	}
}

func TestRedactJSON(t *testing.T) {
	r := NewRedactor()
	in := json.RawMessage(`{"order_id":"A-1","api_key":"` + plantedSecret + `","headers":{"Authorization":"Basic Zm9vOmJhcg=="},"note":"from jane@example.org","amount":12.5,"ssn":123456789}`)
	out := string(r.JSON(in))
	for _, leaked := range []string{plantedSecret, "Zm9vOmJhcg", "jane@example.org", "123456789"} {
		if strings.Contains(out, leaked) {
			t.Errorf("%q leaked: %s", leaked, out)
		}
	}
	if !strings.Contains(out, `"order_id":"A-1"`) || !strings.Contains(out, `"amount":12.5`) || !json.Valid([]byte(out)) {
		t.Errorf("JSON = %s", out)
	}
	if got := r.Report().String(); got != "1 email, 1 header value, 1 long digit run, 1 secret value" {
		t.Errorf("report = %q", got)
	}
	if out := string(r.JSON(json.RawMessage(`not json jane@example.org`))); strings.Contains(out, "jane@") || !json.Valid([]byte(out)) {
		t.Errorf("invalid JSON = %s", out)
	}
}

func TestRedactSessionKeepsIDs(t *testing.T) {
	tr, err := Parse(fixture(t, "otlp-support.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	s := tr.Sessions[0]
	r := NewRedactor()
	r.Session(s)
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{plantedSecret, "jane.doe@example.com", "415 555 0134", "4111 1111", "FAKEtoken0123456789abcdef"} {
		if strings.Contains(string(b), leaked) {
			t.Errorf("%q survived redaction", leaked)
		}
	}
	if s.ID != "5b8efff798038103d269b633813fc60c" {
		t.Errorf("id = %q, want the trace id kept", s.ID)
	}
	if !strings.Contains(r.Report().String(), "1 email") {
		t.Errorf("report = %v", r.Report())
	}
}
