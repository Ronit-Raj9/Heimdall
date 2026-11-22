package mail

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeSMTP is a minimal SMTP server: EHLO with AUTH PLAIN, MAIL, RCPT, DATA.
type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	auth string // decoded AUTH PLAIN payload
	from string
	rcpt []string
	data string
	done chan struct{}
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, done: make(chan struct{})}
	t.Cleanup(func() { _ = ln.Close() })
	go f.serve()
	return f
}

func (f *fakeSMTP) serve() {
	c, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer c.Close()
	defer close(f.done)
	r := bufio.NewReader(c)
	w := func(s string) { _, _ = io.WriteString(c, s+"\r\n") }
	w("220 localhost fake")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
			w("250-localhost")
			w("250 AUTH PLAIN")
		case strings.HasPrefix(up, "AUTH PLAIN"):
			parts := strings.Fields(line)
			if len(parts) == 3 {
				b, _ := base64.StdEncoding.DecodeString(parts[2])
				f.mu.Lock()
				f.auth = string(b)
				f.mu.Unlock()
			}
			w("235 ok")
		case strings.HasPrefix(up, "MAIL FROM:"):
			f.mu.Lock()
			f.from = line[len("MAIL FROM:"):]
			f.mu.Unlock()
			w("250 ok")
		case strings.HasPrefix(up, "RCPT TO:"):
			f.mu.Lock()
			f.rcpt = append(f.rcpt, line[len("RCPT TO:"):])
			f.mu.Unlock()
			w("250 ok")
		case up == "DATA":
			w("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			w("250 queued")
		case up == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func TestSMTPSendsMultipart(t *testing.T) {
	f := startFakeSMTP(t)
	s, err := NewSMTP("smtp://alerts:s3cret@"+f.ln.Addr().String(), "Heimdall <alerts@example.com>")
	if err != nil {
		t.Fatal(err)
	}
	tpl := Template{Heading: "codex v7 regressed", Intro: "Score fell <5 points>.", Sections: []Section{{Title: "Scores", Rows: []Row{{Label: "Overall", Value: "61%", Tone: ToneBad}}}},
		Action: "Open the report card", Link: "https://h.example.com/evaluations/ev_1", Footer: "You get this because you turned on email alerts."}
	if err := s.Send(context.Background(), tpl.Message([]string{"dev@example.com"}, "Regression: codex v7")); err != nil {
		t.Fatal(err)
	}
	<-f.done
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.auth != "\x00alerts\x00s3cret" {
		t.Fatalf("auth = %q", f.auth)
	}
	if f.from != "<alerts@example.com>" || len(f.rcpt) != 1 || f.rcpt[0] != "<dev@example.com>" {
		t.Fatalf("envelope from=%q rcpt=%v", f.from, f.rcpt)
	}
	for _, want := range []string{"Subject: Regression: codex v7", "multipart/alternative", "text/plain", "text/html", "From: Heimdall <alerts@example.com>"} {
		if !strings.Contains(f.data, want) {
			t.Errorf("message lacks %q:\n%s", want, f.data)
		}
	}
	dec, _ := io.ReadAll(quotedprintable.NewReader(strings.NewReader(f.data)))
	if !strings.Contains(string(dec), "Score fell &lt;5 points&gt;.") {
		t.Errorf("HTML part does not escape the intro:\n%s", dec)
	}
	if !strings.Contains(string(dec), "Open the report card: https://h.example.com/evaluations/ev_1") {
		t.Errorf("text part lacks the link:\n%s", dec)
	}
}

func TestSMTPURL(t *testing.T) {
	s, err := NewSMTP("smtps://u:p@mail.example.com", "a@example.com")
	if err != nil || !s.Implicit || s.Host != "mail.example.com:465" {
		t.Fatalf("smtps: %+v %v", s, err)
	}
	s, err = NewSMTP("smtp://mail.example.com", "a@example.com")
	if err != nil || s.Implicit || s.Host != "mail.example.com:587" || s.Username != "" {
		t.Fatalf("smtp: %+v %v", s, err)
	}
	if _, err := NewSMTP("http://x", "a@example.com"); err == nil {
		t.Fatal("http scheme accepted")
	}
}

func TestResendAndPostmark(t *testing.T) {
	var got map[string]any
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Clone()
		got = nil
		_ = json.NewDecoder(r.Body).Decode(&got)
		if r.URL.Path == "/fail" || strings.HasSuffix(r.URL.Path, "/email") && got["To"] == "bad@example.com" {
			w.WriteHeader(422)
			_, _ = io.WriteString(w, `{"ErrorCode":300,"Message":"Invalid To address"}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"1"}`)
	}))
	defer srv.Close()
	m := Message{To: []string{"dev@example.com"}, Subject: "Hi", Text: "body", HTML: "<p>body</p>"}

	r := &Resend{APIKey: "re_key", From: "a@example.com", BaseURL: srv.URL}
	if err := r.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if hdr.Get("Authorization") != "Bearer re_key" || got["subject"] != "Hi" || got["html"] != "<p>body</p>" || got["from"] != "a@example.com" {
		t.Fatalf("resend request: %v %v", hdr, got)
	}

	p := &Postmark{Token: "pm_tok", From: "a@example.com", BaseURL: srv.URL}
	if err := p.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if hdr.Get("X-Postmark-Server-Token") != "pm_tok" || got["TextBody"] != "body" || got["MessageStream"] != "outbound" || got["To"] != "dev@example.com" {
		t.Fatalf("postmark request: %v %v", hdr, got)
	}
	m.To = []string{"bad@example.com"}
	err := p.Send(context.Background(), m)
	if err == nil || !strings.Contains(err.Error(), "HTTP 422: Invalid To address") || strings.Contains(err.Error(), "pm_tok") {
		t.Fatalf("postmark error = %v", err)
	}
}

func TestFromEnv(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	s, err := fromLookup(get, nil)
	if err != nil || ProviderOf(s) != ProviderLog || Delivers(s) {
		t.Fatalf("empty env: %T %v", s, err)
	}
	env[EnvPostmarkToken] = "tok"
	if _, err := fromLookup(get, nil); err == nil || !strings.Contains(err.Error(), EnvFrom) {
		t.Fatalf("no from: %v", err)
	}
	env[EnvFrom] = "Heimdall <alerts@example.com>"
	if s, _ := fromLookup(get, nil); ProviderOf(s) != ProviderPostmark {
		t.Fatalf("postmark: %T", s)
	}
	env[EnvResendKey] = "re"
	if s, _ := fromLookup(get, nil); ProviderOf(s) != ProviderResend {
		t.Fatalf("resend wins over postmark: %T", s)
	}
	env[EnvSMTPURL] = "smtp://h:25"
	if s, _ := fromLookup(get, nil); ProviderOf(s) != ProviderSMTP || !Delivers(s) {
		t.Fatalf("smtp wins: %T", s)
	}
}

type captureLogger struct{ msgs []string }

func (c *captureLogger) Info(msg string, args ...any) { c.msgs = append(c.msgs, msg) }

func TestLogSenderAndFunc(t *testing.T) {
	lg := &captureLogger{}
	l := NewLog(lg)
	send := Func(l)
	if err := send(context.Background(), "dev@example.com", "Verify your email", "Click the link", "<p>Click</p>"); err != nil {
		t.Fatal(err)
	}
	if r := l.Recent(); len(r) != 1 || r[0].Subject != "Verify your email" || r[0].To[0] != "dev@example.com" {
		t.Fatalf("recent = %+v", r)
	}
	if len(lg.msgs) != 1 {
		t.Fatalf("logged %v", lg.msgs)
	}
	if err := send(context.Background(), "not an address", "s", "t", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad address: %v", err)
	}
	if err := send(context.Background(), "a@example.com", "two\nlines", "t", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("header injection accepted: %v", err)
	}
	if err := Func(nil)(context.Background(), "a@example.com", "s", "t", ""); err == nil {
		t.Fatal("nil sender accepted")
	}
}
