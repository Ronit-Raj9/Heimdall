package auth

import (
	"os"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestAPITokenFormat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		tok, prefix, err := NewAPIToken()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(tok, "hm_"+prefix+"_") || len(prefix) != 8 || len(tok) != apiTokenFullLen {
			t.Fatalf("token %q prefix %q", tok, prefix)
		}
		got, ok := ParseAPIToken(tok)
		if !ok || got != prefix {
			t.Fatalf("ParseAPIToken(%q) = %q, %v", tok, got, ok)
		}
		if seen[tok] {
			t.Fatal("duplicate token")
		}
		seen[tok] = true
	}
	tok, _, _ := NewAPIToken()
	for _, bad := range []string{
		"", "hm_", "hmr_abcdefgh_" + tok[12:], strings.ToUpper(tok), tok + "x", tok[:len(tok)-1],
		"hm_ABCDEFGH_" + tok[12:], "hm_abcdefgh-" + tok[12:], "hm_abcdefgh_" + strings.Repeat("!", secretB64Len),
	} {
		if _, ok := ParseAPIToken(bad); ok {
			t.Errorf("ParseAPIToken(%q) accepted", bad)
		}
	}
}

func TestHashSecret(t *testing.T) {
	a, b := HashSecret("x"), HashSecret("x")
	if a != b || len(a) != 64 || a == HashSecret("y") {
		t.Fatalf("HashSecret not a stable sha256 hex: %s", a)
	}
	if !equalHashes(a, b) || equalHashes(a, HashSecret("y")) {
		t.Fatal("equalHashes wrong")
	}
	s1, _ := randomSecret()
	s2, _ := randomSecret()
	if len(s1) != secretB64Len || s1 == s2 {
		t.Fatalf("randomSecret = %q, %q", s1, s2)
	}
}

func TestConfigFrom(t *testing.T) {
	base := func() *lib.Config {
		return &lib.Config{RunsRoot: "runs", Server: lib.ServerConfig{Listen: "127.0.0.1:0", Mode: schemas.ModeLive,
			CORSOrigins: []string{"http://localhost:5173"}}}
	}
	c := base()
	got, err := ConfigFrom(c, "wt")
	if err != nil || got.Hosted || got.WriteToken != "wt" {
		t.Fatalf("local ConfigFrom = %+v, %v", got, err)
	}
	c.Auth = lib.AuthConfig{Mode: "hosted", PublicURL: "https://Heimdall.Example.com:443/", Signup: "open", TrustedProxies: []string{"10.0.0.0/8", "127.0.0.1"}}
	got, err = ConfigFrom(c, "")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Hosted || got.PublicURL != "https://Heimdall.Example.com:443" || got.Signup != SignupOpen || !got.SecureCookies() {
		t.Fatalf("hosted ConfigFrom = %+v", got)
	}
	want := []string{"https://heimdall.example.com", "http://localhost:5173"}
	if strings.Join(got.AllowedOrigins, " ") != strings.Join(want, " ") {
		t.Fatalf("AllowedOrigins = %v, want %v", got.AllowedOrigins, want)
	}
	if got.SessionTTL.Hours() != 168 || got.SessionIdle.Hours() != 24 || len(got.TrustedProxies) != 2 || got.GitHub != nil {
		t.Fatalf("defaults = %+v", got)
	}
	c.Auth.GitHub = lib.GitHubAuthConfig{ClientIDEnv: "HEIMDALL_TEST_GH_ID", ClientSecretEnv: "HEIMDALL_TEST_GH_SECRET"}
	os.Unsetenv("HEIMDALL_TEST_GH_ID")
	os.Unsetenv("HEIMDALL_TEST_GH_SECRET")
	// Named but unset or empty: GitHub is off, the server still starts.
	got, err = ConfigFrom(c, "")
	if err != nil || got.GitHub != nil || !strings.Contains(got.GitHubOff, "HEIMDALL_TEST_GH_ID") {
		t.Fatalf("unset GitHub env = %+v, %v; want GitHub off without error", got.GitHub, err)
	}
	t.Setenv("HEIMDALL_TEST_GH_ID", "id")
	t.Setenv("HEIMDALL_TEST_GH_SECRET", "")
	if got, err = ConfigFrom(c, ""); err != nil || got.GitHub != nil || got.GitHubOff == "" {
		t.Fatalf("empty secret = %+v, %v; want GitHub off", got.GitHub, err)
	}
	t.Setenv("HEIMDALL_TEST_GH_ID", "id")
	t.Setenv("HEIMDALL_TEST_GH_SECRET", "secret")
	got, err = ConfigFrom(c, "")
	if err != nil || got.GitHub == nil || got.GitHub.ClientID != "id" {
		t.Fatalf("GitHub ConfigFrom = %+v, %v", got.GitHub, err)
	}
}

func TestAuthConfigValidate(t *testing.T) {
	bad := []lib.AuthConfig{
		{Mode: "cloud"},
		{Mode: "hosted"},
		{Mode: "hosted", PublicURL: "ftp://x.example"},
		{Mode: "hosted", PublicURL: "https://x.example/app"},
		{Mode: "hosted", PublicURL: "https://x.example", Signup: "maybe"},
		{Mode: "hosted", PublicURL: "https://x.example", SessionTTLH: -1},
		{Mode: "hosted", PublicURL: "https://x.example", TrustedProxies: []string{"not-an-ip"}},
	}
	for _, a := range bad {
		if err := a.Validate(); err == nil {
			t.Errorf("Validate(%+v) accepted", a)
		}
	}
	good := []lib.AuthConfig{{}, {Mode: "local"}, {Mode: "hosted", PublicURL: "http://localhost:8080"}, {Mode: "hosted", PublicURL: "https://x.example/", Signup: "closed"}}
	for _, a := range good {
		if err := a.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v", a, err)
		}
	}
}

func TestOriginOf(t *testing.T) {
	cases := map[string]string{
		"https://A.example":          "https://a.example",
		"https://a.example:443/path": "https://a.example",
		"http://a.example:80":        "http://a.example",
		"http://localhost:5173/x?y":  "http://localhost:5173",
		"http://[::1]:8080":          "http://[::1]:8080",
		"null":                       "",
		"javascript:alert(1)":        "",
		"https://user@a.example":     "",
		"":                           "",
	}
	for in, want := range cases {
		if got := originOf(in); got != want {
			t.Errorf("originOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeReturnTo(t *testing.T) {
	cases := map[string]string{
		"/experiments/e1?tab=runs":     "/experiments/e1?tab=runs",
		"/":                            "/",
		"":                             "/",
		"//evil.example/x":             "/",
		"/\\evil.example":              "/",
		"https://evil.example/":        "/",
		"evil.example":                 "/",
		"/ok\r\nSet-Cookie: x":         "/",
		"javascript:alert(1)":          "/",
		"/" + strings.Repeat("a", 600): "/",
	}
	for in, want := range cases {
		if got := safeReturnTo(in); got != want {
			t.Errorf("safeReturnTo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Ada's organization": "ada-s-organization",
		"  Acme  Corp!! ":    "acme-corp",
		"x":                  "org-x",
		"local":              "org-local",
		"--":                 "org",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Slugify(strings.Repeat("abc ", 40)); len(got) > 40 {
		t.Errorf("slug too long: %q", got)
	}
}
