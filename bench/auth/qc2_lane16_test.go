package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestGrantKeepsConcurrentPromotion is audit qc2-16-02: an invitation grant
// racing a promotion to owner never lowers the member back to the invited
// role.
func TestGrantKeepsConcurrentPromotion(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, ownerSess := e.signup(t, "owner@example.com")
	owner := e.principal(t, ownerSess)
	u, _ := e.signup(t, "member@example.com")
	if err := e.svc.st.grantMembership(bg, e.d.SQL(), owner.OrgID, u.ID, RoleMember); err != nil {
		t.Fatal(err)
	}
	promoted := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- e.d.Tx(bg, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(bg, e.d.Rebind(`SELECT id FROM orgs WHERE id = ?`+e.d.ForUpdate()), owner.OrgID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(bg, e.d.Rebind(`UPDATE memberships SET role = 'owner' WHERE org_id = ? AND user_id = ?`), owner.OrgID, u.ID); err != nil {
				return err
			}
			close(promoted)
			<-release
			return nil
		})
	}()
	<-promoted
	granted := make(chan error, 1)
	go func() {
		granted <- e.d.Tx(bg, func(tx *sql.Tx) error { return e.svc.st.grantMembership(bg, tx, owner.OrgID, u.ID, RoleAdmin) })
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-granted; err != nil {
		t.Fatal(err)
	}
	role, err := e.svc.st.membershipRole(bg, e.d.SQL(), owner.OrgID, u.ID)
	if err != nil || role != RoleOwner {
		t.Fatalf("role after racing grant = %s, %v; want owner", role, err)
	}
}

// TestRemovedMemberTokensStayRevoked is audit qc2-16-04: tokens revoked in
// the removal transaction do not come back when the user is invited again.
func TestRemovedMemberTokensStayRevoked(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, ownerSess := e.signup(t, "owner@example.com")
	owner := e.principal(t, ownerSess)
	u, sess := e.signup(t, "member@example.com")
	if err := e.svc.st.grantMembership(bg, e.d.SQL(), owner.OrgID, u.ID, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SwitchOrg(bg, e.principal(t, sess), owner.OrgID, ""); err != nil {
		t.Fatal(err)
	}
	c, err := e.svc.CreateToken(bg, e.principal(t, sess), TokenCreateRequest{Name: "ci", Role: RoleMember}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.tokenPrincipal(bg, c.Token); err != nil {
		t.Fatalf("token before removal: %v", err)
	}
	if err := e.d.Tx(bg, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(bg, e.d.Rebind(`DELETE FROM memberships WHERE org_id = ? AND user_id = ?`), owner.OrgID, u.ID); err != nil {
			return err
		}
		return RevokeMemberTokensTx(context.Background(), e.d, tx, owner.OrgID, u.ID)
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.st.grantMembership(bg, e.d.SQL(), owner.OrgID, u.ID, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.tokenPrincipal(bg, c.Token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("token after removal and re-invite = %v, want ErrInvalidToken", err)
	}
}

// TestLoginInvalidEmailSkipsEmailBucket is audit qc2-16-06: a malformed or
// oversized login email is refused without a per-email limiter bucket.
func TestLoginInvalidEmailSkipsEmailBucket(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	huge := strings.Repeat("a", 64<<10) + "@example.com"
	for _, email := range []string{huge, "no-at-sign", "x@nodot"} {
		if _, _, err := e.svc.Login(bg, LoginRequest{Email: email, Password: goodPassword}, "10.0.0.9", "test", ""); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("login %.20q = %v", email, err)
		}
	}
	e.svc.loginEmail.mu.Lock()
	n := len(e.svc.loginEmail.buckets)
	e.svc.loginEmail.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d per-email buckets kept for invalid addresses", n)
	}
	e.signup(t, "real@example.com")
	if _, _, err := e.svc.Login(bg, LoginRequest{Email: "real@example.com", Password: goodPassword}, "10.0.0.9", "test", ""); err != nil {
		t.Fatalf("valid login: %v", err)
	}
}
