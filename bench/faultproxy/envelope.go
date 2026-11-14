package faultproxy

import (
	"net/http"
	"strconv"

	"github.com/maximhq/bifrost/bench/utils"
)

// Error categories the proxy answers with. Each format renders them in its
// provider's own envelope, so Bifrost parses an injected fault exactly like a
// real provider error.
const (
	errAuth     = "auth"
	errInvalid  = "invalid"
	errTooLarge = "too_large"
	errNotFound = "not_found"
	errUpstream = "upstream"
)

// writeFault writes an injected failure in the provider's envelope. The run's
// nonce rides in the message: the only place the agent can ever see it. The
// call number after it makes each injected message unique, so a Bifrost
// error row can be joined to exactly one ledger entry.
func (s *Server) writeFault(w http.ResponseWriter, p ProviderSpec, d decision) {
	msg := faultMessage(p.Format, d.Status)
	if d.Nonce != "" {
		msg += " ref " + d.Nonce + " call " + strconv.Itoa(d.Call)
	}
	s.writeStatusError(w, p.Format, d.Status, "", msg, d.RetryAfterS)
}

// writeError writes a proxy-made error of category cat in the format's
// envelope.
func (s *Server) writeError(w http.ResponseWriter, format string, status int, cat, msg string) {
	s.writeStatusError(w, format, status, cat, msg, 0)
}

// writeStatusError renders one error envelope:
//
//	anthropic {"type":"error","error":{"type":...,"message":...},"request_id":...}
//	openai    {"error":{"message":...,"type":...,"code":...}}
//	gemini    {"error":{"code":503,"message":...,"status":"UNAVAILABLE"}}
func (s *Server) writeStatusError(w http.ResponseWriter, format string, status int, cat, msg string, retryAfterS int) {
	reqID := utils.NewID("req", 24)
	if retryAfterS > 0 {
		w.Header().Set("retry-after", strconv.Itoa(retryAfterS))
	}
	switch format {
	case FormatOpenAI:
		w.Header().Set("x-request-id", reqID)
		typ, code := openAIErrorType(status, cat)
		s.writeJSON(w, status, map[string]any{"error": map[string]any{"message": msg, "type": typ, "code": code}})
	case FormatGemini:
		s.writeJSON(w, status, map[string]any{"error": map[string]any{"code": status, "message": msg, "status": geminiStatus(status)}})
	default:
		w.Header().Set("request-id", reqID)
		s.writeJSON(w, status, map[string]any{
			"type":       "error",
			"error":      map[string]string{"type": anthropicErrorType(status, cat), "message": msg},
			"request_id": reqID,
		})
	}
}

// faultMessage is the provider's usual text for an injected status.
func faultMessage(format string, status int) string {
	switch format {
	case FormatOpenAI:
		switch {
		case status == http.StatusTooManyRequests:
			return "Rate limit reached for requests."
		case status == http.StatusServiceUnavailable:
			return "The server is overloaded or not ready yet."
		case status >= 500:
			return "The server had an error while processing your request."
		}
		return "The request could not be processed."
	case FormatGemini:
		if status == http.StatusServiceUnavailable {
			return "The model is overloaded. Please try again later."
		}
		return "An internal error has occurred."
	default:
		if status == http.StatusServiceUnavailable || status == 529 {
			return "Overloaded: the model is temporarily unavailable."
		}
		return "Internal server error."
	}
}

func anthropicErrorType(status int, cat string) string {
	switch {
	case cat == errAuth:
		return "authentication_error"
	case cat == errInvalid:
		return "invalid_request_error"
	case cat == errTooLarge:
		return "request_too_large"
	case cat == errNotFound:
		return "not_found_error"
	case status == http.StatusServiceUnavailable || status == 529:
		return "overloaded_error"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	default:
		return "api_error"
	}
}

func openAIErrorType(status int, cat string) (string, any) {
	switch {
	case cat == errAuth:
		return "invalid_request_error", "invalid_api_key"
	case cat == errInvalid || cat == errTooLarge || cat == errNotFound:
		return "invalid_request_error", nil
	case status == http.StatusTooManyRequests:
		return "requests", "rate_limit_exceeded"
	case status >= 500:
		return "server_error", nil
	default:
		return "invalid_request_error", nil
	}
}

func geminiStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusRequestEntityTooLarge:
		return "INVALID_ARGUMENT"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	case http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	default:
		if status >= 500 {
			return "INTERNAL"
		}
		return "FAILED_PRECONDITION"
	}
}
