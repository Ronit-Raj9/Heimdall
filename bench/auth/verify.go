package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"net/url"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/db"
)

// Email verification (Wave A, audit B1 and P0-1). A password signup starts
// unverified; the server mails a one-time link (or, with no mail sender
// configured, writes it to the server log for the operator to pass on).
//
// The rule: an unverified account can sign in, join organizations through
// invitations and use them with its role (run evaluations, read results).
// Until the address is verified it can not create API tokens, invite
// anyone, create another organization, or be a server admin. GitHub-linked
// accounts count as verified (GitHub vouches for the primary address), and
// so does an address an operator marks with `bifrost-bench user verify`.

// VerifyPath is the UI page a verification link opens; the token is its
// ?token=. The page posts the token back, so a mail scanner that fetches
// the link verifies nothing.
const VerifyPath = "/verify-email"

// VerifyTTL is how long a verification link stays usable.
const VerifyTTL = 24 * time.Hour

// Verification methods stored in email_verifications.method.
const (
	VerifiedByEmail    = "email"
	VerifiedByOperator = "operator"
	VerifiedByGitHub   = "github"
)

// Delivery says where a verification link went.
const (
	DeliveryEmail = "email" // sent through the configured MailSender
	DeliveryLog   = "log"   // no sender: written to the server log
)

// VerifyResendLimit paces resends per user.
var VerifyResendLimit = RateLimit{Burst: 3, Every: 10 * time.Minute}

// MailSender sends one email with a plain text and an HTML body. The
// integrator wires a real mailer (SMTP or an API); nil means no mail is
// sent and links go to the server log.
type MailSender func(ctx context.Context, to, subject, text, html string) error

// ErrEmailUnverified answers an action that needs a verified email.
var ErrEmailUnverified = &Error{Status: 403, Type: "forbidden",
	Msg: "verify your email address first: open the link we sent you, or send a new one from the banner at the top of the page"}

// ErrInvalidVerification answers an unknown, expired or used link.
var ErrInvalidVerification = &Error{Status: 404, Type: "not_found",
	Msg: "this verification link is not valid any more (expired, used, or replaced by a newer one); send a new one after signing in"}

// VerifySent is the answer to sending a link.
type VerifySent struct {
	Email string `json:"email"`
	// Delivery is email (mailed) or log (no mail sender: the operator reads
	// the link from the server log).
	Delivery string `json:"delivery"`
	// AlreadyVerified is true when the address was verified already; no
	// link was made.
	AlreadyVerified bool      `json:"already_verified,omitempty"`
	ExpiresAt       time.Time `json:"expires_at,omitzero"`
}

// VerifyRequest is the body of POST /auth/verify-email.
type VerifyRequest struct {
	Token string `json:"token"`
}

// VerifyResult is the answer to a verified link.
type VerifyResult struct {
	Email    string `json:"email"`
	Verified bool   `json:"verified"`
}

// mailBox holds the sender, set at startup or later by the integrator.
type mailBox struct {
	mu   sync.RWMutex
	send MailSender
}

// SetMailSender installs (or, with nil, removes) the mail sender.
func (s *Service) SetMailSender(m MailSender) {
	s.mail.mu.Lock()
	s.mail.send = m
	s.mail.mu.Unlock()
}

func (s *Service) mailSender() MailSender {
	s.mail.mu.RLock()
	defer s.mail.mu.RUnlock()
	return s.mail.send
}

func (s *Service) emailDelivery() string {
	if s.mailSender() != nil {
		return DeliveryEmail
	}
	return DeliveryLog
}

// RequireVerified returns ErrEmailUnverified for a signed-in user whose
// email is not verified. Tokens, runners and local mode pass: a token was
// made by a verified user (tokens.go refuses the rest).
func RequireVerified(p *Principal) error {
	if p != nil && p.Kind == KindUser && !p.EmailVerified {
		return ErrEmailUnverified
	}
	return nil
}

// emailVerified reports whether u's current email is verified.
func (s *store) emailVerified(ctx context.Context, q querier, u *User) (bool, error) {
	if u.GitHubID != "" {
		return true, nil
	}
	var email string
	err := s.row(ctx, q, `SELECT email FROM email_verifications WHERE user_id = ?`, u.ID).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return email == u.Email, nil
}

// markVerified records that userID verified email.
func (s *store) markVerified(ctx context.Context, q querier, userID, email, method string) error {
	if _, err := s.exec(ctx, q, `DELETE FROM email_verifications WHERE user_id = ?`, userID); err != nil {
		return err
	}
	_, err := s.exec(ctx, q, `INSERT INTO email_verifications (user_id, email, method, verified_at) VALUES (?, ?, ?, ?)`,
		userID, email, method, s.ms())
	return err
}

// MarkEmailVerified marks the current email of the user with that email as
// verified by the operator (`bifrost-bench user verify`), audited as
// system. It returns the user.
func MarkEmailVerified(ctx context.Context, d *db.DB, email string) (*User, error) {
	st := &store{d: d, now: time.Now}
	var u *User
	err := d.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		u, err = st.userByEmail(ctx, tx, email)
		if errors.Is(err, db.ErrNotFound) {
			return fmt.Errorf("no user with email %s", NormalizeEmail(email))
		}
		if err != nil {
			return err
		}
		if err := st.markVerified(ctx, tx, u.ID, u.Email, VerifiedByOperator); err != nil {
			return err
		}
		_, err = st.exec(ctx, tx, `UPDATE email_verify_tokens SET used_at = ? WHERE user_id = ? AND used_at IS NULL`, st.ms(), u.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if _, err := d.AppendAudit(ctx, db.AuditEntry{ActorType: db.ActorSystem, ActorID: "cli", Action: "auth.email_verified",
		TargetType: "user", TargetID: u.ID, Details: db.AuditDetails(map[string]any{"via": "cli", "method": VerifiedByOperator})}); err != nil {
		return u, fmt.Errorf("%w: %s verified but its audit row was not written: %v", ErrAuditFailed, u.ID, err)
	}
	return u, nil
}

// SendVerification makes a fresh verification link for the signed-in user
// and mails it (or logs it). Rate limited per user.
func (s *Service) SendVerification(ctx context.Context, p *Principal, ip string) (*VerifySent, error) {
	if p == nil || p.Kind != KindUser || p.UserID == "" {
		return nil, ErrSessionRequired
	}
	u, err := s.st.userByID(ctx, s.st.d.SQL(), p.UserID)
	if err != nil {
		return nil, err
	}
	ok, err := s.st.emailVerified(ctx, s.st.d.SQL(), u)
	if err != nil {
		return nil, err
	}
	if ok {
		return &VerifySent{Email: u.Email, Delivery: s.emailDelivery(), AlreadyVerified: true}, nil
	}
	if allowed, wait := s.verifySend.Allow(u.ID); !allowed {
		return nil, errRateLimited(wait)
	}
	return s.issueVerification(ctx, u, ip)
}

// issueVerification replaces the user's pending link with a new one and
// delivers it. A failed send is an error the caller sees; the link is
// still logged so the operator can pass it on.
func (s *Service) issueVerification(ctx context.Context, u *User, ip string) (*VerifySent, error) {
	raw, err := randomSecret()
	if err != nil {
		return nil, err
	}
	now := time.UnixMilli(s.now().UnixMilli()).UTC()
	exp := now.Add(VerifyTTL)
	id := db.NewID("evt")
	err = s.st.d.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := s.st.exec(ctx, tx, `DELETE FROM email_verify_tokens WHERE user_id = ? AND used_at IS NULL`, u.ID); err != nil {
			return err
		}
		_, err := s.st.exec(ctx, tx, `INSERT INTO email_verify_tokens (id, user_id, email, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
			id, u.ID, u.Email, HashSecret(raw), now.UnixMilli(), exp.UnixMilli())
		return err
	})
	if err != nil {
		return nil, err
	}
	link := trimSlash(s.cfg.PublicURL) + VerifyPath + "?token=" + url.QueryEscape(raw)
	out := &VerifySent{Email: u.Email, Delivery: DeliveryLog, ExpiresAt: exp}
	send := s.mailSender()
	var sendErr error
	if send != nil {
		subject, text, body := verificationMail(u.Email, link)
		sendErr = send(ctx, u.Email, subject, text, body)
		if sendErr == nil {
			out.Delivery = DeliveryEmail
		}
	}
	if out.Delivery == DeliveryLog {
		// No mailer (or it failed): the operator passes the link on. It is a
		// one-time secret, so it goes to the server log only, never to the
		// response.
		reason := "no mail sender is configured"
		if sendErr != nil {
			reason = "sending failed: " + sendErr.Error()
		}
		s.logger.Info(fmt.Sprintf("auth: email verification link for %s (%s; valid until %s): %s",
			u.Email, reason, exp.Format(time.RFC3339), link))
	}
	s.record(userEvent(u, "", "auth.verify_email_sent", ip, map[string]any{"delivery": out.Delivery, "expires_at": exp.UnixMilli()}))
	if sendErr != nil {
		s.logger.Warn(fmt.Sprintf("auth: verification mail to %s failed: %v", u.Email, sendErr))
		return out, errUnavailable("the verification email could not be sent; ask your server admin for the link in the server log, or try again later")
	}
	return out, nil
}

// VerifyEmail uses a verification link. It works once, only while the
// account's email is still the one the link was made for, and needs no
// session (the link may be opened on another device).
func (s *Service) VerifyEmail(ctx context.Context, req VerifyRequest, ip string) (*VerifyResult, error) {
	if ok, wait := s.probeIP.Allow(ip); !ok {
		return nil, errRateLimited(wait)
	}
	if req.Token == "" || len(req.Token) > 128 {
		return nil, ErrInvalidVerification
	}
	var u *User
	err := s.st.d.Tx(ctx, func(tx *sql.Tx) error {
		var id, userID, email string
		var exp int64
		err := s.st.row(ctx, tx, `SELECT id, user_id, email, expires_at FROM email_verify_tokens WHERE token_hash = ? AND used_at IS NULL`,
			HashSecret(req.Token)).Scan(&id, &userID, &email, &exp)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidVerification
		}
		if err != nil {
			return err
		}
		if exp <= s.st.ms() {
			return ErrInvalidVerification
		}
		cur, err := s.st.lockUser(ctx, tx, userID)
		if errors.Is(err, db.ErrNotFound) {
			return ErrInvalidVerification
		}
		if err != nil {
			return err
		}
		if cur.Disabled || cur.Email != email {
			return ErrInvalidVerification
		}
		res, err := s.st.exec(ctx, tx, `UPDATE email_verify_tokens SET used_at = ? WHERE id = ? AND used_at IS NULL`, s.st.ms(), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrInvalidVerification
		}
		u = cur
		return s.st.markVerified(ctx, tx, cur.ID, cur.Email, VerifiedByEmail)
	})
	if err != nil {
		if errors.Is(err, ErrInvalidVerification) {
			s.record(AuditEvent{ActorType: "anonymous", Action: "auth.verify_email_failed", TargetType: "token", IP: ip})
		}
		return nil, err
	}
	s.record(userEvent(u, "", "auth.email_verified", ip, map[string]any{"method": VerifiedByEmail}))
	return &VerifyResult{Email: u.Email, Verified: true}, nil
}

// verificationMail is the subject and bodies of the verification email.
func verificationMail(email, link string) (subject, text, body string) {
	subject = "Confirm your email for Heimdall"
	text = fmt.Sprintf("Confirm that %s is your email address by opening this link:\n\n%s\n\n"+
		"The link works once and expires in 24 hours. If you did not create a Heimdall account, ignore this email.\n", email, link)
	body = fmt.Sprintf(`<p>Confirm that <strong>%s</strong> is your email address.</p>`+
		`<p><a href="%s">Confirm email address</a></p>`+
		`<p>The link works once and expires in 24 hours. If you did not create a Heimdall account, ignore this email.</p>`,
		html.EscapeString(email), html.EscapeString(link))
	return subject, text, body
}

// resolveIdentity sets p's EmailVerified and server admin flags for u.
func (s *Service) resolveIdentity(ctx context.Context, p *Principal, u *User) error {
	verified, err := s.st.emailVerified(ctx, s.st.d.SQL(), u)
	if err != nil {
		return err
	}
	admin, err := s.serverAdminFor(ctx, u, verified)
	if err != nil {
		return err
	}
	p.EmailVerified, p.serverAdmin = verified, admin
	return nil
}
