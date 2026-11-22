package registrycreds

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestNormalize(t *testing.T) {
	ok := []struct {
		in       Input
		host, us string
	}{
		{Input{Kind: "dockerhub", Username: "acme", SecretName: "HUB_TOKEN"}, "docker.io", "acme"},
		{Input{Kind: "ghcr", Username: "octo", SecretName: "GHCR_TOKEN"}, "ghcr.io", "octo"},
		{Input{Kind: "ecr", Host: "123456789012.dkr.ecr.eu-west-1.amazonaws.com", Username: "AKIAABCDEFGHIJKLMNOP", SecretName: "ECR_SECRET_KEY"}, "123456789012.dkr.ecr.eu-west-1.amazonaws.com", "AKIAABCDEFGHIJKLMNOP"},
		{Input{Kind: "gcr", Host: "europe-docker.pkg.dev", SecretName: "GCP_SA_JSON"}, "europe-docker.pkg.dev", GCRUsername},
		{Input{Kind: "acr", Host: "AcmeReg.azurecr.io", Username: "00000000-app", SecretName: "ACR_PASSWORD"}, "acmereg.azurecr.io", "00000000-app"},
		{Input{Kind: "other", Host: "registry.example.com:5000", Username: "bot", SecretName: "REG_PASS"}, "registry.example.com:5000", "bot"},
	}
	for _, c := range ok {
		got, err := Normalize(c.in)
		if err != nil || got.Host != c.host || got.Username != c.us {
			t.Errorf("%+v: %+v %v", c.in, got, err)
		}
	}
	if got, _ := Normalize(ok[2].in); got.Region != "eu-west-1" {
		t.Errorf("ecr region %q", got.Region)
	}
	bad := []Input{
		{Kind: "nope", Username: "a", SecretName: "A"},
		{Kind: "ghcr", Host: "docker.io", Username: "a", SecretName: "A"},
		{Kind: "ecr", Host: "example.com", Username: "AKIAABCDEFGHIJKLMNOP", SecretName: "A"},
		{Kind: "ecr", Host: "123456789012.dkr.ecr.eu-west-1.amazonaws.com", Username: "AKIAABCDEFGHIJKLMNOP", SecretName: "A", Region: "us-east-1"},
		{Kind: "acr", Host: "acme.example.com", Username: "a", SecretName: "A"},
		{Kind: "ghcr", Username: "", SecretName: "A"},
		{Kind: "ghcr", Username: "a", SecretName: "lower"},
		{Kind: "other", Host: "https://x y", Username: "a", SecretName: "A"},
	}
	for _, in := range bad {
		if _, err := Normalize(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v accepted: %v", in, err)
		}
	}
}

func TestImageHostAndConfig(t *testing.T) {
	for ref, want := range map[string]string{
		"python:3.12":                                      "docker.io",
		"acme/agent@sha256:abc":                            "docker.io",
		"ghcr.io/acme/agent:1":                             "ghcr.io",
		"localhost:5000/x":                                 "localhost:5000",
		"index.docker.io/acme/agent":                       "docker.io",
		"EU.GCR.IO/proj/img":                               "eu.gcr.io",
		"123456789012.dkr.ecr.us-east-1.amazonaws.com/a:b": "123456789012.dkr.ecr.us-east-1.amazonaws.com",
	} {
		if got := schemas.ImageRegistryHost(ref); got != want {
			t.Errorf("%s: %s, want %s", ref, got, want)
		}
	}
	a := schemas.RegistryAuth{Host: "docker.io", Username: "u", Password: "p@ss"}
	var cfg struct {
		Auths map[string]struct{ Auth string } `json:"auths"`
	}
	if err := json.Unmarshal(DockerConfig(a), &cfg); err != nil {
		t.Fatal(err)
	}
	got := cfg.Auths["https://index.docker.io/v1/"].Auth
	if dec, _ := base64.StdEncoding.DecodeString(got); string(dec) != "u:p@ss" {
		t.Fatalf("auth %q", got)
	}
	if s := a.String() + " " + a.GoString(); strings.Contains(s, "p@ss") {
		t.Fatalf("String leaks the password: %s", s)
	}
}

func TestResolveAndECR(t *testing.T) {
	var target, authz string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target, authz = r.Header.Get("X-Amz-Target"), r.Header.Get("Authorization")
		tok := base64.StdEncoding.EncodeToString([]byte("AWS:ecr-pass"))
		_, _ = w.Write([]byte(`{"authorizationData":[{"authorizationToken":"` + tok + `","expiresAt":` + "4102444800" + `}]}`))
	}))
	defer srv.Close()
	ctx := context.Background()
	ks, err := keystore.Open(nil, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	add := func(in Input) {
		in, err := Normalize(in)
		if err != nil {
			t.Fatal(err)
		}
		p, _ := Put(in)
		if _, err := ks.Create(ctx, "o1", keystore.KindRegistry, p, "u"); err != nil {
			t.Fatal(err)
		}
	}
	add(Input{Kind: "ghcr", Username: "octo", SecretName: "GHCR_TOKEN"})
	add(Input{Kind: "ecr", Host: "123456789012.dkr.ecr.us-east-1.amazonaws.com", Username: "AKIAABCDEFGHIJKLMNOP", SecretName: "ECR_SECRET_KEY"})
	add(Input{Kind: "acr", Host: "acme1.azurecr.io", Username: "app", SecretName: "MISSING"})
	_, _ = ks.Create(ctx, "o1", keystore.KindSecret, keystore.Put{Provider: "secret", Label: "GHCR_TOKEN", Value: "ghp_x"}, "u")
	_, _ = ks.Create(ctx, "o1", keystore.KindSecret, keystore.Put{Provider: "secret", Label: "ECR_SECRET_KEY", Value: "awsSecretKey"}, "u")
	r := &Resolver{ECR: ECRClient{Endpoint: func(string) string { return srv.URL + "/" }}}
	auths, problems, err := r.Resolve(ctx, ks, "o1")
	if err != nil {
		t.Fatal(err)
	}
	if len(auths) != 2 || len(problems) != 1 || problems[0].Host != "acme1.azurecr.io" {
		t.Fatalf("auths %v problems %+v", auths, problems)
	}
	ecr, ok := For("123456789012.dkr.ecr.us-east-1.amazonaws.com/agent@sha256:"+strings.Repeat("a", 64), auths)
	if !ok || ecr.Username != "AWS" || ecr.Password != "ecr-pass" {
		t.Fatalf("ecr auth %v", ecr)
	}
	if target != "AmazonEC2ContainerRegistry_V20150921.GetAuthorizationToken" ||
		!strings.HasPrefix(authz, "AWS4-HMAC-SHA256 Credential=AKIAABCDEFGHIJKLMNOP/") || strings.Contains(authz, "awsSecretKey") {
		t.Fatalf("ECR request: target %q authz %q", target, authz)
	}
	if other, _, _ := r.Resolve(ctx, ks, "o2"); len(other) != 0 {
		t.Fatal("org isolation")
	}
}

// TestSignV4 checks the signature against the AWS documented example key.
func TestSignV4(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://api.ecr.us-east-1.amazonaws.com/", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AmazonEC2ContainerRegistry_V20150921.GetAuthorizationToken")
	signV4(req, []byte("{}"), "us-east-1", "ecr", "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC))
	a := req.Header.Get("Authorization")
	if !strings.Contains(a, "Credential=AKIDEXAMPLE/20150830/us-east-1/ecr/aws4_request") ||
		!strings.Contains(a, "SignedHeaders=content-type;host;x-amz-date;x-amz-target") || req.Header.Get("X-Amz-Date") != "20150830T123600Z" {
		t.Fatalf("authorization %q", a)
	}
}

type fakePuller struct {
	dir  string
	cfg  []byte
	args []string
}

func (f *fakePuller) MustRunEnv(_ context.Context, env []string, args ...string) (*utils.DockerResult, error) {
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "DOCKER_CONFIG="); ok {
			f.dir = v
			f.cfg, _ = os.ReadFile(filepath.Join(v, "config.json"))
		}
	}
	f.args = args
	return &utils.DockerResult{}, nil
}

func TestPullUsesTemporaryConfig(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	f := &fakePuller{}
	auths := []schemas.RegistryAuth{{Host: "ghcr.io", Username: "octo", Password: "ghp_x"}}
	if err := Pull(context.Background(), f, "ghcr.io/acme/agent:1", auths); err != nil {
		t.Fatal(err)
	}
	if f.dir == "" || !strings.Contains(string(f.cfg), "ghcr.io") || strings.Join(f.args, " ") != "pull ghcr.io/acme/agent:1" {
		t.Fatalf("pull: dir %q cfg %s args %v", f.dir, f.cfg, f.args)
	}
	if _, err := os.Stat(f.dir); !os.IsNotExist(err) {
		t.Fatalf("temporary Docker config left behind: %v", err)
	}
	if err := Pull(context.Background(), f, "quay.io/x/y", auths); err == nil {
		t.Fatal("pull without a matching login")
	}
}
