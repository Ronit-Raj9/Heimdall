package datapolicy

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/pii"
)

func TestStoreDefaultsSetAndAccess(t *testing.T) {
	root := t.TempDir()
	s := Open(root)
	p, err := s.Get("support")
	if err != nil || p.Access.Mode != AccessOrg || p.PII.Mode != pii.ModeWarn {
		t.Fatalf("default %+v %v", p, err)
	}
	if !p.Allows(Viewer{UserID: "u1"}) {
		t.Fatal("org access refused a member")
	}
	if _, err := s.Set("support", Policy{Access: Access{Mode: "secret"}}, "a", time.Now()); err == nil {
		t.Fatal("bad access mode accepted")
	}
	got, err := s.Set("support", Policy{PII: pii.Policy{Mode: pii.ModeRedact}, Access: Access{Mode: AccessRestricted, Users: []string{"u2", " u1 ", "u2"}}}, "admin@x", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Access.Users) != 2 || got.Access.Users[0] != "u1" {
		t.Fatalf("users not normalized: %+v", got.Access.Users)
	}
	again, _ := s.Get("support")
	if !again.Allows(Viewer{UserID: "u1"}) || again.Allows(Viewer{UserID: "u3"}) || !again.Allows(Viewer{Manager: true}) || again.Allows(Viewer{}) {
		t.Fatalf("restricted access wrong: %+v", again.Access)
	}
	r, _ := s.Restricted()
	if _, ok := r["support"]; !ok || len(r) != 1 {
		t.Fatalf("restricted list %v", r)
	}
	fi, err := os.Stat(filepath.Join(root, ".heimdall", "benchmark-policies.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("policy file %v %v", fi, err)
	}
}

func TestKeyIsStableAndPrivate(t *testing.T) {
	root := t.TempDir()
	s := Open(root)
	k1, err := s.Key()
	if err != nil || len(k1) != 32 {
		t.Fatal(err)
	}
	k2, _ := Open(root).Key()
	if string(k1) != string(k2) {
		t.Fatal("key changed")
	}
	fi, _ := os.Stat(filepath.Join(root, ".heimdall", "pii.key"))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", fi.Mode())
	}
	_, _ = s.Set("b", Policy{PII: pii.Policy{Mode: pii.ModeRedact}}, "", time.Now())
	r, _, err := s.Redactor("b")
	if err != nil || len(r.Key) != 32 {
		t.Fatalf("redactor key %v", err)
	}
}
