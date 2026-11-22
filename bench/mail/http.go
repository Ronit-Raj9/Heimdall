package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// httpClient is the client the HTTP providers use when theirs is nil.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// Resend sends through the Resend email API (POST /emails).
type Resend struct {
	APIKey  string
	From    string
	BaseURL string // default https://api.resend.com
	Client  *http.Client
}

// Provider implements Describer.
func (*Resend) Provider() string { return ProviderResend }

// Send implements Sender.
func (r *Resend) Send(ctx context.Context, m Message) error {
	if err := m.Validate(); err != nil {
		return err
	}
	body := map[string]any{"from": r.From, "to": m.To, "subject": m.Subject, "text": m.Text}
	if m.HTML != "" {
		body["html"] = m.HTML
	}
	base := r.BaseURL
	if base == "" {
		base = "https://api.resend.com"
	}
	return postJSON(ctx, r.Client, strings.TrimRight(base, "/")+"/emails", body, map[string]string{"Authorization": "Bearer " + r.APIKey}, "resend")
}

// Postmark sends through the Postmark email API (POST /email) with a
// server API token.
type Postmark struct {
	Token   string
	From    string
	Stream  string // message stream, default "outbound"
	BaseURL string // default https://api.postmarkapp.com
	Client  *http.Client
}

// Provider implements Describer.
func (*Postmark) Provider() string { return ProviderPostmark }

// Send implements Sender.
func (p *Postmark) Send(ctx context.Context, m Message) error {
	if err := m.Validate(); err != nil {
		return err
	}
	stream := p.Stream
	if stream == "" {
		stream = "outbound"
	}
	body := map[string]any{"From": p.From, "To": strings.Join(m.To, ","), "Subject": m.Subject, "TextBody": m.Text, "MessageStream": stream}
	if m.HTML != "" {
		body["HtmlBody"] = m.HTML
	}
	base := p.BaseURL
	if base == "" {
		base = "https://api.postmarkapp.com"
	}
	return postJSON(ctx, p.Client, strings.TrimRight(base, "/")+"/email", body,
		map[string]string{"X-Postmark-Server-Token": p.Token, "Accept": "application/json"}, "postmark")
}

func postJSON(ctx context.Context, c *http.Client, url string, body any, headers map[string]string, name string) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Heimdall-Mail/1")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c == nil {
		c = httpClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s answered HTTP %d: %s", name, resp.StatusCode, providerError(msg))
	}
	return nil
}

// providerError is the provider's message field when it sent JSON, never
// the request (which holds the key).
func providerError(b []byte) string {
	var v struct {
		Message string `json:"message"`
		PM      string `json:"Message"`
	}
	if json.Unmarshal(b, &v) == nil {
		if v.Message != "" {
			return v.Message
		}
		if v.PM != "" {
			return v.PM
		}
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
