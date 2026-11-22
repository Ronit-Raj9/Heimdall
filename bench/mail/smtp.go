package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

// SMTP sends through an SMTP server. smtp:// upgrades with STARTTLS when the
// server offers it and refuses to send a password over a plain connection
// (except to localhost); smtps:// connects with TLS from the start.
type SMTP struct {
	Host     string // host:port
	Username string
	Password string
	From     string // the From header (may carry a display name)
	Implicit bool   // smtps://
	Timeout  time.Duration
	// TLSConfig overrides the TLS settings (tests); nil verifies Host.
	TLSConfig *tls.Config
}

// NewSMTP parses smtp://user:pass@host:port or smtps://... into a sender.
// The default port is 587 for smtp and 465 for smtps.
func NewSMTP(raw, from string) (*SMTP, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%s must look like smtp://user:password@host:587 or smtps://user:password@host:465", EnvSMTPURL)
	}
	s := &SMTP{From: from, Timeout: 30 * time.Second}
	switch u.Scheme {
	case "smtp":
	case "smtps":
		s.Implicit = true
	default:
		return nil, fmt.Errorf("%s scheme must be smtp or smtps, not %q", EnvSMTPURL, u.Scheme)
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = "587"
		if s.Implicit {
			port = "465"
		}
	}
	s.Host = net.JoinHostPort(host, port)
	if u.User != nil {
		s.Username = u.User.Username()
		s.Password, _ = u.User.Password()
	}
	return s, nil
}

// Provider implements Describer.
func (*SMTP) Provider() string { return ProviderSMTP }

// Send implements Sender.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	if err := m.Validate(); err != nil {
		return err
	}
	fromAddr, err := mail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("mail from %q: %w", s.From, err)
	}
	body, err := buildMIME(s.From, m, time.Now())
	if err != nil {
		return err
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	host, _, _ := net.SplitHostPort(s.Host)
	tc := s.TLSConfig
	if tc == nil {
		tc = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	d := &net.Dialer{}
	var conn net.Conn
	if s.Implicit {
		conn, err = (&tls.Dialer{NetDialer: d, Config: tc}).DialContext(ctx, "tcp", s.Host)
	} else {
		conn, err = d.DialContext(ctx, "tcp", s.Host)
	}
	if err != nil {
		return fmt.Errorf("smtp connect %s: %w", s.Host, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp %s: %w", s.Host, err)
	}
	defer c.Close()
	if !s.Implicit {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tc); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		}
	}
	if s.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return fmt.Errorf("smtp %s does not offer AUTH but a username is set", s.Host)
		}
		// PlainAuth itself refuses an unencrypted connection to a host
		// other than localhost.
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(fromAddr.Address); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	for _, to := range m.To {
		if err := c.Rcpt(to); err != nil {
			return fmt.Errorf("smtp RCPT TO %s: %w", to, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp DATA end: %w", err)
	}
	return c.Quit()
}

// buildMIME renders the message as RFC 5322 with a text part and, when set,
// an HTML alternative, both quoted-printable.
func buildMIME(from string, m Message, now time.Time) ([]byte, error) {
	var b bytes.Buffer
	hdr := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	hdr("From", from)
	hdr("To", strings.Join(m.To, ", "))
	hdr("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	hdr("Date", now.UTC().Format(time.RFC1123Z))
	hdr("Message-ID", "<"+randID()+"@heimdall>")
	hdr("MIME-Version", "1.0")
	hdr("Auto-Submitted", "auto-generated")
	part := func(ctype, content string) error {
		fmt.Fprintf(&b, "Content-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", ctype)
		w := quotedprintable.NewWriter(&b)
		if _, err := w.Write([]byte(content)); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		b.WriteString("\r\n")
		return nil
	}
	if m.HTML == "" {
		if err := part("text/plain", m.Text); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	}
	boundary := "hm-" + randID()
	hdr("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	for _, p := range []struct{ ctype, content string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		b.WriteString("--" + boundary + "\r\n")
		if err := part(p.ctype, p.content); err != nil {
			return nil, err
		}
	}
	b.WriteString("--" + boundary + "--\r\n")
	return b.Bytes(), nil
}

func randID() string {
	var x [12]byte
	_, _ = rand.Read(x[:])
	return hex.EncodeToString(x[:])
}
