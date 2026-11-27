package handlers

import (
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
)

// TestWithHasPassword: a user with no password hash (GitHub only) gets
// has_password false; a token or a nil me gets none.
func TestWithHasPassword(t *testing.T) {
	gh := withHasPassword(&auth.Me{Kind: string(auth.KindUser), User: &auth.User{ID: "usr_1"}})
	if gh.HasPassword == nil || *gh.HasPassword {
		t.Fatalf("github-only user = %+v", gh.HasPassword)
	}
	pw := withHasPassword(&auth.Me{Kind: string(auth.KindUser), User: &auth.User{ID: "usr_2", PasswordHash: "x"}})
	if pw.HasPassword == nil || !*pw.HasPassword {
		t.Fatalf("password user = %+v", pw.HasPassword)
	}
	if tok := withHasPassword(&auth.Me{Kind: "token"}); tok.HasPassword != nil {
		t.Fatal("token got has_password")
	}
	if none := withHasPassword(nil); none.HasPassword != nil {
		t.Fatal("nil me got has_password")
	}
}
