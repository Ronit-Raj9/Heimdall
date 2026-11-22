// Package mail sends Heimdall's email: evaluation alerts, the weekly digest
// and, through Func, the account emails other packages send (verification,
// invitations, password resets).
//
// FromEnv picks the transport from the environment:
//
//	HEIMDALL_SMTP_URL        smtp://user:pass@host:587 (STARTTLS) or smtps://user:pass@host:465 (TLS)
//	HEIMDALL_RESEND_API_KEY  send through Resend (https://resend.com)
//	HEIMDALL_POSTMARK_TOKEN  send through Postmark (a server API token)
//	HEIMDALL_MAIL_FROM       the From address, e.g. "Heimdall <alerts@example.com>" (required by every real transport)
//
// The first one set wins, in that order. With none set, FromEnv returns a
// log-only sender: nothing leaves the server, every message is written to
// the server log and kept in memory (Recent) so an operator can see what
// would have been sent.
package mail

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"strings"
	"sync"
	"time"
)

// Message is one email. HTML is optional; Text is always sent.
type Message struct {
	To      []string
	Subject string
	Text    string
	HTML    string
}

// Sender delivers a message.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Limits.
const (
	MaxRecipients   = 50
	MaxSubjectBytes = 200
	MaxBodyBytes    = 512 << 10
)

// ErrInvalid wraps every refusal of a malformed message.
var ErrInvalid = errors.New("invalid email")

// Validate checks a message before any transport sees it: at least one
// recipient, each a plain address, a one-line subject and a text body.
func (m Message) Validate() error {
	if len(m.To) == 0 || len(m.To) > MaxRecipients {
		return fmt.Errorf("%w: 1 to %d recipients", ErrInvalid, MaxRecipients)
	}
	for _, to := range m.To {
		if err := ValidAddress(to); err != nil {
			return err
		}
	}
	if strings.TrimSpace(m.Subject) == "" || len(m.Subject) > MaxSubjectBytes || strings.ContainsAny(m.Subject, "\r\n") {
		return fmt.Errorf("%w: the subject must be one line of 1 to %d bytes", ErrInvalid, MaxSubjectBytes)
	}
	if strings.TrimSpace(m.Text) == "" {
		return fmt.Errorf("%w: the text body is empty", ErrInvalid)
	}
	if len(m.Text)+len(m.HTML) > MaxBodyBytes {
		return fmt.Errorf("%w: the body is larger than %d bytes", ErrInvalid, MaxBodyBytes)
	}
	return nil
}

// ValidAddress accepts one bare address (no display name, no line breaks).
func ValidAddress(s string) error {
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || strings.ContainsAny(s, "\r\n<>") {
		return fmt.Errorf("%w: %q is not an email address", ErrInvalid, s)
	}
	return nil
}

// Func adapts a Sender to the callback shape other packages take, so they
// need not import this one (auth's verification emails use it).
func Func(s Sender) func(ctx context.Context, to, subject, text, html string) error {
	return func(ctx context.Context, to, subject, text, html string) error {
		if s == nil {
			return errors.New("no email sender is configured")
		}
		return s.Send(ctx, Message{To: []string{to}, Subject: subject, Text: text, HTML: html})
	}
}

// Logger is the subset of schemas.Logger the log sender writes to.
type Logger interface {
	Info(msg string, args ...any)
}

// Env names.
const (
	EnvSMTPURL       = "HEIMDALL_SMTP_URL"
	EnvFrom          = "HEIMDALL_MAIL_FROM"
	EnvResendKey     = "HEIMDALL_RESEND_API_KEY"
	EnvPostmarkToken = "HEIMDALL_POSTMARK_TOKEN"
)

// Provider names, as Describe reports them.
const (
	ProviderSMTP     = "smtp"
	ProviderResend   = "resend"
	ProviderPostmark = "postmark"
	ProviderLog      = "log"
)

// Describer reports which transport a sender uses.
type Describer interface {
	Provider() string
}

// ProviderOf is the transport of s ("log" when it cannot say).
func ProviderOf(s Sender) string {
	if d, ok := s.(Describer); ok {
		return d.Provider()
	}
	return ProviderLog
}

// Delivers reports whether s sends email out of the server (false for the
// log-only sender).
func Delivers(s Sender) bool { return s != nil && ProviderOf(s) != ProviderLog }

// FromEnv builds the sender the environment configures (see the package
// doc). A transport named without HEIMDALL_MAIL_FROM, or a malformed SMTP
// URL, is an error: the operator meant to send real email.
func FromEnv(logger Logger) (Sender, error) {
	return fromLookup(os.Getenv, logger)
}

func fromLookup(get func(string) string, logger Logger) (Sender, error) {
	from := strings.TrimSpace(get(EnvFrom))
	need := func(p string) error {
		if from == "" {
			return fmt.Errorf("%s is set but %s is empty: set the From address, e.g. \"Heimdall <alerts@example.com>\"", p, EnvFrom)
		}
		if _, err := mail.ParseAddress(from); err != nil {
			return fmt.Errorf("%s %q is not an email address: %v", EnvFrom, from, err)
		}
		return nil
	}
	if raw := strings.TrimSpace(get(EnvSMTPURL)); raw != "" {
		if err := need(EnvSMTPURL); err != nil {
			return nil, err
		}
		return NewSMTP(raw, from)
	}
	if key := strings.TrimSpace(get(EnvResendKey)); key != "" {
		if err := need(EnvResendKey); err != nil {
			return nil, err
		}
		return &Resend{APIKey: key, From: from}, nil
	}
	if tok := strings.TrimSpace(get(EnvPostmarkToken)); tok != "" {
		if err := need(EnvPostmarkToken); err != nil {
			return nil, err
		}
		return &Postmark{Token: tok, From: from}, nil
	}
	return NewLog(logger), nil
}

// Logged is one message the log sender recorded.
type Logged struct {
	At      time.Time `json:"at"`
	To      []string  `json:"to"`
	Subject string    `json:"subject"`
	Text    string    `json:"text"`
}

// LogSender sends nothing: it writes each message to the logger and keeps
// the newest ones in memory for the operator.
type LogSender struct {
	logger Logger
	mu     sync.Mutex
	recent []Logged
}

// logKeep is how many messages a LogSender keeps.
const logKeep = 100

// NewLog returns a log-only sender (logger may be nil).
func NewLog(logger Logger) *LogSender { return &LogSender{logger: logger} }

// Provider implements Describer.
func (*LogSender) Provider() string { return ProviderLog }

// Send implements Sender.
func (l *LogSender) Send(_ context.Context, m Message) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if l.logger != nil {
		l.logger.Info("email not sent (no mail transport configured; set HEIMDALL_SMTP_URL, HEIMDALL_RESEND_API_KEY or HEIMDALL_POSTMARK_TOKEN)",
			"to", strings.Join(m.To, ","), "subject", m.Subject)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recent = append(l.recent, Logged{At: time.Now().UTC(), To: append([]string(nil), m.To...), Subject: m.Subject, Text: m.Text})
	if len(l.recent) > logKeep {
		l.recent = l.recent[len(l.recent)-logKeep:]
	}
	return nil
}

// Recent returns the recorded messages, oldest first.
func (l *LogSender) Recent() []Logged {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Logged(nil), l.recent...)
}
