package sharelinks

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestShareTokenV2HasNoOrgID (fix wave 3, N293): the opaque token holds no
// organization id, is stable for one link, differs between links and opens
// its own link only.
func TestShareTokenV2HasNoOrgID(t *testing.T) {
	st := Open(t.TempDir())
	a, err := st.Create(NewShare{Kind: KindRunTrace, Target: "run_1"}, "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.Create(NewShare{Kind: KindRunTrace, Target: "run_2"}, "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	ta, err := st.TokenV2(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	tb, _ := st.TokenV2(b.ID)
	again, _ := st.TokenV2(a.ID)
	if !strings.HasPrefix(ta, TokenV2Prefix) || len(ta) != len(TokenV2Prefix)+43 || ta != again || ta == tb {
		t.Fatalf("tokens %q %q %q", ta, again, tb)
	}
	if strings.Contains(ta, "org_") || strings.Contains(ta, a.ID) || strings.Contains(ta, ".") || !IsTokenV2(ta) || IsTokenV2("org_a."+a.ID+".sig") {
		t.Fatalf("v2 token %q names the org or the link", ta)
	}
	if got, err := st.Verify("org_a", ta); err != nil || got.ID != a.ID {
		t.Fatalf("verify v2: %+v %v", got, err)
	}
	if got, err := st.Verify("org_a", tb); err != nil || got.ID != b.ID {
		t.Fatalf("verify v2 b: %+v %v", got, err)
	}
	forged := ta[:len(ta)-3] + "AAA"
	if _, err := st.Verify("org_a", forged); !errors.Is(err, ErrBadToken) {
		t.Fatalf("forged v2: %v", err)
	}
	// Another store (another org's key) does not accept it.
	other := Open(t.TempDir())
	if _, err := other.Create(NewShare{Kind: KindRunTrace, Target: "run_1"}, "usr_9"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Verify("org_b", ta); !errors.Is(err, ErrBadToken) {
		t.Fatalf("v2 token in another org: %v", err)
	}
}

// TestShareRevokedV2Is410 (store part): a revoked or expired link answers
// ErrRevoked or ErrExpired for its v2 token exactly as for the old form.
func TestShareRevokedV2Is410(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	st := Open(t.TempDir()).WithClock(func() time.Time { return now })
	sh, err := st.Create(NewShare{Kind: KindRunTrace, Target: "run_1", ExpiresInDays: days(1)}, "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := st.TokenV2(sh.ID)
	later := st.WithClock(func() time.Time { return now.Add(25 * time.Hour) })
	if _, err := later.Verify("org_a", tok); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired v2: %v", err)
	}
	if _, err := st.Revoke(sh.ID, "usr_2"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Verify("org_a", tok); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked v2: %v", err)
	}
}
