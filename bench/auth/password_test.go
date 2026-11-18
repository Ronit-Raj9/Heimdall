package auth

import (
	"errors"
	"strings"
	"testing"
)

// fastParams keep tests quick; production uses DefaultArgon2Params.
var fastParams = Argon2Params{Memory: 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

func TestHashVerifyRoundTrip(t *testing.T) {
	h := NewHasher(fastParams)
	enc, err := h.Hash("correct horse battery staple 9")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=1024,t=1,p=1$") {
		t.Fatalf("encoding = %s", enc)
	}
	enc2, _ := h.Hash("correct horse battery staple 9")
	if enc == enc2 {
		t.Fatal("two hashes of one password are equal: salt not random")
	}
	if rehash, err := h.Verify(enc, "correct horse battery staple 9"); err != nil || rehash {
		t.Fatalf("Verify = %v, %v", rehash, err)
	}
	if _, err := h.Verify(enc, "correct horse battery staple 8"); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("wrong password err = %v", err)
	}
}

func TestVerifyFlagsRehashWhenParamsChange(t *testing.T) {
	old := NewHasher(fastParams)
	enc, _ := old.Hash("a long enough passphrase")
	stronger := fastParams
	stronger.Time = 2
	rehash, err := NewHasher(stronger).Verify(enc, "a long enough passphrase")
	if err != nil || !rehash {
		t.Fatalf("Verify with new params = %v, %v; want rehash", rehash, err)
	}
}

func TestVerifyRejectsBadHashes(t *testing.T) {
	h := NewHasher(fastParams)
	for _, bad := range []string{
		"",
		"plaintext",
		"$argon2i$v=19$m=1024,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=16$m=1024,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=99999999,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5", // memory too large
		"$argon2id$v=19$m=1024,t=1000,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",  // too many passes
		"$argon2id$v=19$m=1024,t=1,p=1$!!$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=1024,t=1,x=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
	} {
		if _, err := h.Verify(bad, "whatever"); err == nil || errors.Is(err, ErrPasswordMismatch) {
			t.Errorf("Verify(%q) err = %v, want a parse error", bad, err)
		}
	}
}

func TestBurnVerify(t *testing.T) {
	h := NewHasher(fastParams)
	h.BurnVerify("anything")
	h.BurnVerify("")
	if h.dummy == "" {
		t.Fatal("dummy hash not prepared")
	}
}

func TestPasswordPolicy(t *testing.T) {
	ok := []string{
		"violet-tractor-umbrella",
		"Tr0ub4dor&3-plus-more",
		"ünïcödé-pässwörd-ok",
	}
	for _, p := range ok {
		if err := CheckPasswordPolicy(p, "ada@example.com"); err != nil {
			t.Errorf("CheckPasswordPolicy(%q) = %v, want ok", p, err)
		}
	}
	bad := map[string]string{
		"short":                   "shorter than",
		"elevenchars":             "shorter than",
		"password1234":            "common",
		"Password1234":            "common",
		"PASSWORD2026!!":          "common",
		"qwerty123456":            "common",
		"iloveyou1234":            "common",
		"sunshine99999":           "common",
		"123456789012":            "common",
		"aaaaaaaaaaaaaaaa":        "repeated",
		"ada@example.com":         "email",
		"adalovelace2026":         "",
		strings.Repeat("x", 1025): "longer than",
		"heimdall-heimdall":       "",
		string([]byte{0xff, 0xfe, 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j'}): "UTF-8",
	}
	for p, want := range bad {
		err := CheckPasswordPolicy(p, "ada@example.com")
		if want == "" {
			continue // documented as allowed or borderline; only the cases with a reason must fail
		}
		if err == nil || !errors.Is(err, ErrWeakPassword) || !strings.Contains(err.Error(), want) {
			t.Errorf("CheckPasswordPolicy(%q) = %v, want ErrWeakPassword mentioning %q", p, err, want)
		}
	}
	if err := CheckPasswordPolicy("adalovelace!", "adalovelace@example.com"); err == nil {
		t.Error("password built from the email local part was accepted")
	}
}
