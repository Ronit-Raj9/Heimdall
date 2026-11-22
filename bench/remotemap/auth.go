package remotemap

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// GoogleTokenURL is where a Google service account key is exchanged for an
// ID token.
const GoogleTokenURL = "https://oauth2.googleapis.com/token"

// tokenEarly is how long before its expiry a cached token is fetched again.
const tokenEarly = 60 * time.Second

var (
	tenantRe  = regexp.MustCompile(`^[A-Za-z0-9.-]{1,128}$`)
	regionRe  = regexp.MustCompile(`^[a-z0-9-]{2,32}$`)
	serviceRe = regexp.MustCompile(`^[a-z0-9-]{2,64}$`)
)

// TokenURL is the token endpoint an auth block fetches from, "" for a type
// that fetches none (SigV4, mutual TLS only).
func TokenURL(a *schemas.RemoteAuth) string {
	if a == nil {
		return ""
	}
	switch a.Type {
	case schemas.RemoteAuthOAuth2:
		return a.TokenURL
	case schemas.RemoteAuthEntra:
		if a.TokenURL != "" {
			return a.TokenURL
		}
		return "https://login.microsoftonline.com/" + a.TenantID + "/oauth2/v2.0/token"
	case schemas.RemoteAuthGCPIDToken:
		if a.TokenURL != "" {
			return a.TokenURL
		}
		return GoogleTokenURL
	}
	return ""
}

// CheckAuth refuses an auth block with an unknown type or a missing field.
// It is pure.
func CheckAuth(a *schemas.RemoteAuth) error {
	if a == nil {
		return nil
	}
	need := func(v, label string) error {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("remote.auth %s needs %s", a.Type, label)
		}
		return nil
	}
	var errs []error
	switch a.Type {
	case "":
		if a.ClientCertSecret == "" {
			return errors.New("remote.auth needs a type (oauth2_client_credentials, azure_entra, aws_sigv4, gcp_id_token) or a client certificate")
		}
	case schemas.RemoteAuthOAuth2:
		errs = append(errs, need(a.TokenURL, "token_url"), need(a.ClientIDSecret, "client_id_secret"), need(a.ClientSecretSecret, "client_secret_secret"))
	case schemas.RemoteAuthEntra:
		if a.TokenURL == "" {
			errs = append(errs, need(a.TenantID, "tenant_id"))
			if a.TenantID != "" && !tenantRe.MatchString(a.TenantID) {
				errs = append(errs, fmt.Errorf("remote.auth tenant_id %q is not a tenant id or domain", a.TenantID))
			}
		}
		errs = append(errs, need(a.ClientIDSecret, "client_id_secret"), need(a.ClientSecretSecret, "client_secret_secret"), need(a.Scope, "scope (for example api://<app id>/.default)"))
	case schemas.RemoteAuthSigV4:
		errs = append(errs, need(a.AccessKeySecret, "access_key_secret"), need(a.SecretKeySecret, "secret_key_secret"), need(a.Region, "region"), need(a.Service, "service"))
		if a.Region != "" && !regionRe.MatchString(a.Region) {
			errs = append(errs, fmt.Errorf("remote.auth region %q is not an AWS region", a.Region))
		}
		if a.Service != "" && !serviceRe.MatchString(a.Service) {
			errs = append(errs, fmt.Errorf("remote.auth service %q is not an AWS signing name", a.Service))
		}
	case schemas.RemoteAuthGCPIDToken:
		errs = append(errs, need(a.ServiceAccountSecret, "service_account_secret"))
	default:
		return fmt.Errorf("remote.auth type %q: want oauth2_client_credentials, azure_entra, aws_sigv4 or gcp_id_token", a.Type)
	}
	if (a.ClientCertSecret == "") != (a.ClientKeySecret == "") {
		errs = append(errs, errors.New("remote.auth mutual TLS needs both client_cert_secret and client_key_secret"))
	}
	if a.CACertSecret != "" && a.ClientCertSecret == "" {
		errs = append(errs, errors.New("remote.auth ca_cert_secret needs a client certificate"))
	}
	if u := TokenURL(a); u != "" {
		p, err := url.Parse(u)
		if err != nil || p.Scheme != "https" || p.Hostname() == "" || p.User != nil {
			errs = append(errs, fmt.Errorf("remote.auth token URL %q must be an https URL", u))
		}
	}
	return errors.Join(errs...)
}

// Authorizer adds auth to each request of one run (or one test). Tokens are
// fetched on first use and again shortly before they expire.
type Authorizer struct {
	spec    *schemas.RemoteAuth
	secrets map[string]string
	client  *http.Client
	now     func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
	// Fetches counts token fetches (tests, the endpoint test trace).
	Fetches int
}

// NewAuthorizer checks that every secret the block names has a value.
// client fetches tokens; now is the clock (nil: time.Now).
func NewAuthorizer(spec *schemas.RemoteAuth, secrets map[string]string, client *http.Client, now func() time.Time) (*Authorizer, error) {
	if err := CheckAuth(spec); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	a := &Authorizer{spec: spec, secrets: secrets, client: client, now: now}
	for _, n := range (&schemas.RemoteAgent{Auth: spec}).SecretNames() {
		if secrets[n] == "" {
			return nil, fmt.Errorf("secret %s is not set: add it on Settings > Secrets", n)
		}
	}
	return a, nil
}

// TLSConfig is the client certificate config, nil without mutual TLS.
func (a *Authorizer) TLSConfig() (*tls.Config, error) {
	if a == nil || a.spec == nil || a.spec.ClientCertSecret == "" {
		return nil, nil
	}
	cert, err := tls.X509KeyPair([]byte(a.secrets[a.spec.ClientCertSecret]), []byte(a.secrets[a.spec.ClientKeySecret]))
	if err != nil {
		return nil, fmt.Errorf("client certificate: %v (want PEM certificate and key secrets)", err)
	}
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	if ca := a.spec.CACertSecret; ca != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(a.secrets[ca])) {
			return nil, errors.New("CA certificate secret holds no PEM certificate")
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}

// Apply authorizes req, whose body is body. header is where a bearer token
// goes ("" or Authorization: "Bearer <token>"; another name: the raw
// token).
func (a *Authorizer) Apply(ctx context.Context, req *http.Request, body []byte, header string) error {
	if a == nil || a.spec == nil {
		return nil
	}
	switch a.spec.Type {
	case schemas.RemoteAuthSigV4:
		return SignV4(req, body, SigV4Keys{
			AccessKey: a.secrets[a.spec.AccessKeySecret], SecretKey: a.secrets[a.spec.SecretKeySecret],
			SessionToken: a.secrets[a.spec.SessionTokenSecret], Region: a.spec.Region, Service: a.spec.Service,
		}, a.now())
	case schemas.RemoteAuthOAuth2, schemas.RemoteAuthEntra, schemas.RemoteAuthGCPIDToken:
		tok, err := a.Token(ctx)
		if err != nil {
			return err
		}
		if header == "" || strings.EqualFold(header, "Authorization") {
			req.Header.Set("Authorization", "Bearer "+tok)
		} else {
			req.Header.Set(header, tok)
		}
	}
	return nil
}

// Token returns a cached token or fetches a new one.
func (a *Authorizer) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && a.now().Add(tokenEarly).Before(a.expires) {
		return a.token, nil
	}
	tok, ttl, err := a.fetch(ctx)
	if err != nil {
		return "", err
	}
	a.Fetches++
	a.token, a.expires = tok, a.now().Add(ttl)
	return tok, nil
}

func (a *Authorizer) fetch(ctx context.Context) (string, time.Duration, error) {
	form := url.Values{}
	field := "access_token"
	switch a.spec.Type {
	case schemas.RemoteAuthGCPIDToken:
		jwt, err := googleAssertion(a.secrets[a.spec.ServiceAccountSecret], a.spec.Audience, TokenURL(a.spec), a.now())
		if err != nil {
			return "", 0, err
		}
		form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
		form.Set("assertion", jwt)
		field = "id_token"
	default:
		form.Set("grant_type", "client_credentials")
		form.Set("client_id", a.secrets[a.spec.ClientIDSecret])
		form.Set("client_secret", a.secrets[a.spec.ClientSecretSecret])
		if a.spec.Scope != "" {
			form.Set("scope", a.spec.Scope)
		}
		if a.spec.Audience != "" {
			form.Set("audience", a.spec.Audience)
		}
	}
	tokenURL := TokenURL(a.spec)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("token endpoint %s did not answer: %v", hostOf(tokenURL), scrubErr(err, tokenURL))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	if resp.StatusCode/100 != 2 {
		why, _ := doc["error_description"].(string)
		if why == "" {
			why, _ = doc["error"].(string)
		}
		return "", 0, fmt.Errorf("token endpoint %s answered %d %s", hostOf(tokenURL), resp.StatusCode, truncate(why, 200))
	}
	tok, _ := doc[field].(string)
	if tok == "" {
		return "", 0, fmt.Errorf("token endpoint %s answered without %s", hostOf(tokenURL), field)
	}
	ttl := time.Hour
	switch v := doc["expires_in"].(type) {
	case float64:
		ttl = time.Duration(v) * time.Second
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			ttl = time.Duration(n) * time.Second
		}
	}
	return tok, ttl, nil
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return "?"
}

// scrubErr drops a URL's query from an error text.
func scrubErr(err error, raw string) string {
	s := err.Error()
	if u, perr := url.Parse(raw); perr == nil && u.RawQuery != "" {
		s = strings.ReplaceAll(s, "?"+u.RawQuery, "")
	}
	return s
}

// googleAssertion signs the JWT a service account key exchanges for an ID
// token with audience aud.
func googleAssertion(keyJSON, aud, tokenURL string, now time.Time) (string, error) {
	var key struct {
		Type         string `json:"type"`
		ClientEmail  string `json:"client_email"`
		PrivateKey   string `json:"private_key"`
		PrivateKeyID string `json:"private_key_id"`
	}
	if err := json.Unmarshal([]byte(keyJSON), &key); err != nil || key.ClientEmail == "" || key.PrivateKey == "" {
		return "", errors.New("the service account secret is not a Google service account JSON key (client_email and private_key)")
	}
	if aud == "" {
		return "", errors.New("gcp_id_token needs an audience (the service URL)")
	}
	block, _ := pem.Decode([]byte(key.PrivateKey))
	if block == nil {
		return "", errors.New("the service account private_key is not PEM")
	}
	var priv *rsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		priv, _ = k.(*rsa.PrivateKey)
	} else if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		priv = k
	}
	if priv == nil {
		return "", errors.New("the service account private_key is not an RSA key")
	}
	head := map[string]string{"alg": "RS256", "typ": "JWT"}
	if key.PrivateKeyID != "" {
		head["kid"] = key.PrivateKeyID
	}
	iat := now.Unix()
	claims := map[string]any{"iss": key.ClientEmail, "sub": key.ClientEmail, "aud": tokenURL,
		"iat": iat, "exp": iat + 3600, "target_audience": aud}
	hb, _ := json.Marshal(head)
	cb, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(hb) + "." + enc.EncodeToString(cb)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(nil, priv, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign the service account assertion: %v", err)
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

// SigV4Keys are the AWS credentials and scope of a signature.
type SigV4Keys struct {
	AccessKey, SecretKey, SessionToken string
	Region, Service                    string
}

// SignV4 signs req with AWS Signature Version 4. The signed headers are
// content-type (when set), host, x-amz-date and, with a session token,
// x-amz-security-token; the payload hash covers body.
func SignV4(req *http.Request, body []byte, k SigV4Keys, now time.Time) error {
	if k.AccessKey == "" || k.SecretKey == "" || k.Region == "" || k.Service == "" {
		return errors.New("SigV4 needs an access key, a secret key, a region and a service")
	}
	t := now.UTC()
	amzDate := t.Format("20060102T150405Z")
	day := t.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	if k.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", k.SessionToken)
	}
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	headers := map[string]string{"host": host, "x-amz-date": amzDate}
	if ct := req.Header.Get("Content-Type"); ct != "" {
		headers["content-type"] = ct
	}
	if k.SessionToken != "" {
		headers["x-amz-security-token"] = k.SessionToken
	}
	names := sortedKeys(headers)
	var ch strings.Builder
	for _, n := range names {
		ch.WriteString(n + ":" + strings.Join(strings.Fields(headers[n]), " ") + "\n")
	}
	signed := strings.Join(names, ";")
	payload := sha256.Sum256(body)
	canonical := strings.Join([]string{req.Method, sigv4Path(req.URL, k.Service), sigv4Query(req.URL),
		ch.String(), signed, hex.EncodeToString(payload[:])}, "\n")
	scope := day + "/" + k.Region + "/" + k.Service + "/aws4_request"
	cs := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(cs[:])
	mac := func(key []byte, s string) []byte {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(s))
		return h.Sum(nil)
	}
	key := mac(mac(mac(mac([]byte("AWS4"+k.SecretKey), day), k.Region), k.Service), "aws4_request")
	sig := hex.EncodeToString(mac(key, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+k.AccessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
	return nil
}

// sigv4Escape is the RFC 3986 encoding SigV4 wants.
func sigv4Escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// sigv4Path is the canonical URI: each segment of the escaped path is
// encoded again (every service but S3 signs the path encoded twice).
func sigv4Path(u *url.URL, service string) string {
	p := u.EscapedPath()
	if p == "" {
		return "/"
	}
	if service == "s3" {
		return p
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = sigv4Escape(s)
	}
	return strings.Join(segs, "/")
}

func sigv4Query(u *url.URL) string {
	q := u.Query()
	var pairs []string
	for k, vs := range q {
		for _, v := range vs {
			pairs = append(pairs, sigv4Escape(k)+"="+sigv4Escape(v))
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}
