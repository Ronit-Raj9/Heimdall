package auth

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Error is an auth failure with the HTTP answer it maps to. Messages are
// written for the client and never say whether an account exists.
type Error struct {
	Status int
	Type   string // lib.ErrType* value
	Msg    string
	// RetryAfter is set on 429 answers.
	RetryAfter time.Duration
}

func (e *Error) Error() string { return e.Msg }

// Is matches errors of the same status and type, so errors.Is(err,
// ErrInvalidCredentials) works on a copy.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Status == e.Status && t.Type == e.Type && t.Msg == e.Msg
}

// The uniform auth errors.
var (
	// ErrInvalidCredentials answers every failed login: unknown email,
	// wrong password, disabled account and password-less account alike.
	ErrInvalidCredentials = &Error{Status: http.StatusUnauthorized, Type: "unauthorized", Msg: "invalid email or password"}
	// ErrUnauthenticated answers a route that needs a signed-in caller.
	ErrUnauthenticated = &Error{Status: http.StatusUnauthorized, Type: "unauthorized", Msg: "sign in required"}
	// ErrInvalidToken answers a bearer token that is unknown, revoked or
	// expired, whichever it is.
	ErrInvalidToken = &Error{Status: http.StatusUnauthorized, Type: "unauthorized", Msg: "invalid or expired token"}
	// ErrSessionRequired answers a route only a browser session may call
	// (switching org, changing the password).
	ErrSessionRequired = &Error{Status: http.StatusForbidden, Type: "forbidden", Msg: "this action needs a signed-in session, not a token"}
	// ErrSignupNotAllowed answers signup when the policy does not admit it.
	ErrSignupNotAllowed = &Error{Status: http.StatusForbidden, Type: "forbidden", Msg: "signup is by invitation only on this server"}
	// ErrSignupFailed answers a signup that can not proceed with these
	// details, including an email that already has an account.
	ErrSignupFailed = &Error{Status: http.StatusBadRequest, Type: "invalid_request", Msg: "could not create an account with these details; if you already have one, sign in"}
	// ErrInvalidInvitation answers an invitation token that is unknown,
	// expired or used, whichever it is.
	ErrInvalidInvitation = &Error{Status: http.StatusNotFound, Type: "not_found", Msg: "invitation not found, expired or already used"}
	// ErrInvitationEmail answers accepting an invitation sent to another
	// address than the signed-in user's.
	ErrInvitationEmail = &Error{Status: http.StatusForbidden, Type: "forbidden", Msg: "this invitation was sent to a different email address"}
	// ErrCSRF answers a cookie-authenticated write without the CSRF header
	// or from an origin that is not allowed.
	ErrCSRF = &Error{Status: http.StatusForbidden, Type: "forbidden", Msg: "CSRF check failed: send X-Heimdall-CSRF (from GET /api/bench/auth/me) from an allowed origin"}
	// ErrOrigin answers an unauthenticated write from a foreign origin.
	ErrOrigin = &Error{Status: http.StatusForbidden, Type: "forbidden", Msg: "origin not allowed"}
)

func errBadRequest(format string, args ...any) *Error {
	return &Error{Status: http.StatusBadRequest, Type: "invalid_request", Msg: fmt.Sprintf(format, args...)}
}

func errForbidden(format string, args ...any) *Error {
	return &Error{Status: http.StatusForbidden, Type: "forbidden", Msg: fmt.Sprintf(format, args...)}
}

func errNotFound(format string, args ...any) *Error {
	return &Error{Status: http.StatusNotFound, Type: "not_found", Msg: fmt.Sprintf(format, args...)}
}

func errConflict(format string, args ...any) *Error {
	return &Error{Status: http.StatusConflict, Type: "conflict", Msg: fmt.Sprintf(format, args...)}
}

// errUnavailable is a backend failure (database outage) that callers may
// retry; it is never a verdict on the credential.
func errUnavailable(format string, args ...any) *Error {
	return &Error{Status: http.StatusServiceUnavailable, Type: "unavailable", Msg: fmt.Sprintf(format, args...)}
}

func errRateLimited(wait time.Duration) *Error {
	if wait < time.Second {
		wait = time.Second
	}
	return &Error{Status: http.StatusTooManyRequests, Type: "rate_limited", Msg: "too many attempts; try again later", RetryAfter: wait}
}

// AsError returns err as an *Error when it is one.
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
