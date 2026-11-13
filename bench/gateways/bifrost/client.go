package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	maxResponseBytes = 256 << 20 // detail rows carry whole conversations
	errorBodyBytes   = 300
)

// apiError is a non-2xx answer from Bifrost. It never carries the setup token.
type apiError struct {
	Method string
	Path   string
	Status int
	Body   string
}

// Error implements error.
func (e *apiError) Error() string {
	return fmt.Sprintf("bifrost %s %s: status %d: %s", e.Method, e.Path, e.Status, e.Body)
}

// call sends one management request with the setup token. in, when not nil,
// is sent as JSON; out, when not nil, receives the decoded JSON answer.
func (g *Gateway) call(ctx context.Context, method, path string, in, out any) error {
	raw, err := g.callRaw(ctx, method, path, in)
	if err != nil {
		return err
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("bifrost %s %s: decode answer: %w", method, path, err)
	}
	return nil
}

// callRaw is call returning the undecoded answer body.
func (g *Gateway) callRaw(ctx context.Context, method, path string, in any) ([]byte, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("bifrost %s %s: encode body: %w", method, path, err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.cfg.URL+path, body)
	if err != nil {
		return nil, fmt.Errorf("bifrost %s %s: %w", method, path, err)
	}
	req.Header.Set(setupTokenHeader, g.cfg.SetupToken)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bifrost %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("bifrost %s %s: read answer: %w", method, path, err)
	}
	if len(raw) > maxResponseBytes {
		return nil, fmt.Errorf("bifrost %s %s: answer larger than %d bytes", method, path, maxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &apiError{Method: method, Path: path, Status: resp.StatusCode, Body: snippet(raw)}
	}
	return raw, nil
}

// isStatus reports whether err is a Bifrost answer with the given status.
func isStatus(err error, status int) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Status == status
}

// snippet shortens an error body for messages and logs.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > errorBodyBytes {
		s = s[:errorBodyBytes] + "..."
	}
	return s
}
