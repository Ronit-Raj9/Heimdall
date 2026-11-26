package lib

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/bench/plaintext"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Error types used in the error body. Clients branch on these, never on the
// message text.
const (
	ErrTypeBadRequest       = "invalid_request"
	ErrTypeUnauthorized     = "unauthorized"
	ErrTypeForbidden        = "forbidden"
	ErrTypeNotFound         = "not_found"
	ErrTypeMethodNotAllowed = "method_not_allowed"
	ErrTypeConflict         = "conflict"
	ErrTypeTooLarge         = "request_too_large"
	ErrTypeUnsupportedMedia = "unsupported_media_type"
	ErrTypeUnavailable      = "unavailable"
	ErrTypeInternal         = "internal_error"
)

// ModeHeader carries the mode on responses that are not JSON, such as a diff.
const ModeHeader = "X-Bench-Mode"

// ErrorBody is the one error shape of the bench API.
type ErrorBody struct {
	Mode  schemas.Mode `json:"mode"`
	Error ErrorField   `json:"error"`
}

// ErrorField describes what went wrong.
type ErrorField struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	// Link is an app path that fixes the error (X01: "/models" to add a
	// model key); empty for most errors.
	Link string `json:"link,omitempty"`
}

// SendJSON writes payload as JSON with status and merges a top-level "mode"
// field in beside the payload's own fields: every response is the payload
// object plus mode, never a nested envelope. A payload that already leads with
// its own mode field (schemas.Catalog) is sent as is. A payload that is not a
// JSON object cannot carry mode and is a programming error, answered 500.
func SendJSON(ctx *fasthttp.RequestCtx, mode schemas.Mode, status int, payload any) {
	body, err := sonic.ConfigStd.Marshal(payload)
	if err != nil {
		SendError(ctx, mode, fasthttp.StatusInternalServerError, ErrTypeInternal, "encode response: "+err.Error())
		return
	}
	merged, err := WithMode(body, mode)
	if err != nil {
		SendError(ctx, mode, fasthttp.StatusInternalServerError, ErrTypeInternal, "encode response: "+err.Error())
		return
	}
	writeJSON(ctx, mode, status, merged)
}

// WithMode splices "mode" in as the first field of a JSON object.
func WithMode(object []byte, mode schemas.Mode) ([]byte, error) {
	object = bytes.TrimSpace(object)
	if len(object) < 2 || object[0] != '{' || object[len(object)-1] != '}' {
		return nil, errors.New("payload is not a JSON object")
	}
	if bytes.HasPrefix(object, []byte(`{"mode":`)) {
		return object, nil
	}
	modeJSON, err := json.Marshal(mode)
	if err != nil {
		return nil, fmt.Errorf("encode mode: %w", err)
	}
	out := make([]byte, 0, len(object)+len(modeJSON)+10)
	out = append(out, `{"mode":`...)
	out = append(out, modeJSON...)
	rest := bytes.TrimSpace(object[1:])
	if len(rest) > 1 { // more than the closing brace
		out = append(out, ',')
	}
	return append(out, rest...), nil
}

// SendError writes the error shape {"mode", "error": {"message", "type"}}.
func SendError(ctx *fasthttp.RequestCtx, mode schemas.Mode, status int, errType, message string) {
	message = plaintext.Hosted(message) // wave 4, F1.3: no server path, flag or org id reaches a hosted browser
	// encoding/json cannot fail on this fixed struct of strings, and keeping
	// the error path free of sonic keeps it usable when sonic itself failed.
	body, err := json.Marshal(ErrorBody{Mode: mode, Error: ErrorField{Message: message, Type: errType}})
	if err != nil {
		ctx.Error("internal error", fasthttp.StatusInternalServerError)
		return
	}
	writeJSON(ctx, mode, status, body)
}

// SendErrorLink is SendError with a link to the app page that fixes the
// error (ErrorField.Link).
func SendErrorLink(ctx *fasthttp.RequestCtx, mode schemas.Mode, status int, errType, message, link string) {
	message = plaintext.Hosted(message) // wave 4, F1.3
	body, err := json.Marshal(ErrorBody{Mode: mode, Error: ErrorField{Message: message, Type: errType, Link: link}})
	if err != nil {
		ctx.Error("internal error", fasthttp.StatusInternalServerError)
		return
	}
	writeJSON(ctx, mode, status, body)
}

// writeJSON sets the headers and body of a JSON response.
func writeJSON(ctx *fasthttp.RequestCtx, mode schemas.Mode, status int, body []byte) {
	ctx.SetStatusCode(status)
	ctx.SetContentType("application/json; charset=utf-8")
	ctx.Response.Header.Set(ModeHeader, string(mode))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.SetBody(append(body, '\n'))
}
