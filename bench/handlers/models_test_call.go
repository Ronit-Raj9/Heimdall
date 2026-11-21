package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/keystore"
)

// maxTestBody caps how much of a provider's answer the test call reads.
const maxTestBody = 4 << 20

// testRequest builds the cheapest authenticated call that proves a key
// works: a list-models call where the provider has one (no tokens billed).
func testRequest(ctx context.Context, k *keystore.Key, value string) (*http.Request, error) {
	base := k.BaseURL
	if base == "" {
		for _, p := range modelProviders() {
			if p.ID == k.Provider {
				base = p.DefaultBaseURL
			}
		}
	}
	if base == "" {
		return nil, errors.New("this key has no base URL; edit it and add one")
	}
	base = strings.TrimRight(base, "/")
	get := func(path string) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	}
	var (
		req *http.Request
		err error
	)
	switch k.Provider {
	case "anthropic":
		if req, err = get("/models?limit=100"); err == nil {
			req.Header.Set("x-api-key", value)
			req.Header.Set("anthropic-version", "2023-06-01")
		}
	case "gemini":
		if req, err = get("/models?pageSize=200"); err == nil {
			req.Header.Set("x-goog-api-key", value)
		}
	case "openrouter":
		// /models is public on OpenRouter; /key checks the key itself.
		if req, err = get("/key"); err == nil {
			req.Header.Set("Authorization", "Bearer "+value)
		}
	case "sarvam":
		// Sarvam has no list call; language identification of one word is
		// its smallest authenticated request: POST https://api.sarvam.ai/text-lid
		// with the api-subscription-key header (docs.sarvam.ai, API
		// reference "Identify Language", checked 21 Nov 2026). Its REST
		// endpoints sit at the root, not under the OpenAI-style /v1 that a
		// pasted chat base URL carries, so that suffix is dropped.
		base = strings.TrimSuffix(base, "/v1")
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, base+"/text-lid", strings.NewReader(`{"input":"hello"}`))
		if err == nil {
			req.Header.Set("api-subscription-key", value)
			req.Header.Set("Content-Type", "application/json")
		}
	default: // openai, groq, openai-compatible
		if req, err = get("/models"); err == nil {
			req.Header.Set("Authorization", "Bearer "+value)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("bad base URL %q: %v", base, err)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// testProviderKey runs the test call. In hosted mode the client refuses
// private, loopback and link-local addresses, so a base URL cannot reach
// the server's own network.
func testProviderKey(ctx context.Context, hosted bool, k *keystore.Key, value string) ModelKeyTestResult {
	start := time.Now()
	res := ModelKeyTestResult{Status: keystore.StatusError}
	req, err := testRequest(ctx, k, value)
	if err != nil {
		res.Message = err.Error()
		return res
	}
	client := testHTTPClient(hosted)
	resp, err := client.Do(req)
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Message = "could not reach " + req.URL.Host + ": " + shortNetError(err) + ". Check the base URL and that the server can reach the internet."
		return res
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxTestBody))
	res.HTTPStatus = resp.StatusCode
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		res.OK, res.Status = true, keystore.StatusOK
		res.Models = parseModelIDs(body)
		res.Message = "key works"
		if len(res.Models) > 0 {
			res.Message = fmt.Sprintf("key works: %d models available", len(res.Models))
		}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		res.Status = keystore.StatusInvalid
		res.Message = fmt.Sprintf("%s refused the key (HTTP %d): check you pasted the whole key and that it is active", req.URL.Host, resp.StatusCode)
		if m := providerMessage(body); m != "" {
			res.Message += ". Provider said: " + m
		}
	case k.Provider == "sarvam" && resp.StatusCode >= 400 && sarvamAuthError(body):
		// Sarvam names a bad key in the error code (invalid_api_key_error,
		// authentication_error) whatever the HTTP status.
		res.Status = keystore.StatusInvalid
		res.Message = fmt.Sprintf("Sarvam AI refused the key (HTTP %d): check the subscription key at dashboard.sarvam.ai", resp.StatusCode)
		if m := providerMessage(body); m != "" {
			res.Message += ". Provider said: " + m
		}
	case resp.StatusCode == http.StatusBadRequest && k.Provider == "gemini" && strings.Contains(string(body), "API_KEY_INVALID"):
		res.Status = keystore.StatusInvalid
		res.Message = "Google refused the key: API key not valid"
	case resp.StatusCode == http.StatusTooManyRequests:
		// Rate limited, but the key authenticated.
		res.OK, res.Status = true, keystore.StatusOK
		res.Message = "key accepted, but the provider is rate limiting it right now (HTTP 429)"
	case resp.StatusCode == http.StatusNotFound:
		res.Message = fmt.Sprintf("%s has no %s (HTTP 404): check the base URL (it usually ends in /v1)", req.URL.Host, req.URL.Path)
	default:
		res.Message = fmt.Sprintf("%s answered HTTP %d", req.URL.Host, resp.StatusCode)
		if m := providerMessage(body); m != "" {
			res.Message += ": " + m
		}
	}
	return res
}

func shortNetError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	s := err.Error()
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// providerMessage pulls a short error message out of a provider's JSON.
// sarvamAuthError reports whether a Sarvam error body ({"error":{"code":...}})
// names an authentication failure.
func sarvamAuthError(body []byte) bool {
	var v struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &v) != nil {
		return false
	}
	return v.Error.Code == "invalid_api_key_error" || v.Error.Code == "authentication_error"
}

func providerMessage(body []byte) string {
	var v struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Detail  any             `json:"detail"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	msg := v.Message
	if len(v.Error) > 0 {
		var e struct {
			Message string `json:"message"`
		}
		var s string
		if json.Unmarshal(v.Error, &e) == nil && e.Message != "" {
			msg = e.Message
		} else if json.Unmarshal(v.Error, &s) == nil {
			msg = s
		}
	}
	if len(msg) > 300 {
		msg = msg[:300] + "..."
	}
	return msg
}

// parseModelIDs reads model ids from an OpenAI/Anthropic ({data:[{id}]}) or
// Gemini ({models:[{name:"models/x"}]}) list, sorted, at most 200.
func parseModelIDs(body []byte) []string {
	var v struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.Unmarshal(body, &v) != nil {
		return nil
	}
	var out []string
	for _, d := range v.Data {
		if d.ID != "" {
			out = append(out, d.ID)
		}
	}
	for _, m := range v.Models {
		if m.Name != "" {
			out = append(out, strings.TrimPrefix(m.Name, "models/"))
		}
	}
	sort.Strings(out)
	if len(out) > 200 {
		out = out[:200]
	}
	return out
}

func testHTTPClient(hosted bool) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if hosted {
		d.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || blockedIP(ip) {
				return fmt.Errorf("address %s is not allowed (private or local network)", host)
			}
			return nil
		}
	}
	tr := &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second}
	if !hosted {
		tr.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{Timeout: 20 * time.Second, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse // never follow a redirect with the key attached
	}}
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() || cgnat.Contains(ip)
}

// checkHostedBaseURL refuses base URLs a hosted server must not call: plain
// http, and hosts that are literal private or local addresses (names that
// resolve to them are refused when the test call dials).
func checkHostedBaseURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return errors.New("base URL must be an https URL such as https://api.example.com/v1")
	}
	if u.Scheme != "https" {
		return errors.New("base URL must use https on a hosted server")
	}
	h := u.Hostname()
	if strings.EqualFold(h, "localhost") || strings.HasSuffix(strings.ToLower(h), ".localhost") || strings.HasSuffix(strings.ToLower(h), ".internal") {
		return errors.New("base URL must be a public address on a hosted server")
	}
	if ip := net.ParseIP(h); ip != nil && blockedIP(ip) {
		return errors.New("base URL must be a public address on a hosted server")
	}
	return nil
}
