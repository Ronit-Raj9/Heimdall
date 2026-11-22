package imagepin

// Pull tokens for the library registry (Docker registry token
// authentication, https://distribution.github.io/distribution/spec/auth/token/).
// The registry (deploy/hosted/registry/config-library.yml) trusts tokens
// signed by the key in TokenKeyFile, whose certificate it holds as its
// rootcertbundle; the Heimdall server signs them (handlers/registrytoken.go)
// after it checked who asks: an org's API or runner token gets pull access
// to the library, the sync identity gets pull and push. Tokens are ES256
// JWTs carrying the certificate in x5c, which both registry 2.8 and 3
// verify against the bundle.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Files of a token key folder (images token-key writes them).
const (
	TokenKeyFile  = "token.key" // EC P-256 private key, PEM, mode 0600: the server only
	TokenCertFile = "token.crt" // its self-signed certificate: the registry's rootcertbundle
)

// DefaultService and DefaultIssuer are the registry's auth.token service and
// issuer unless the operator names others.
const (
	DefaultService = "heimdall-library"
	DefaultIssuer  = "heimdall"
)

// TokenTTL is how long a pull token is valid. A pull that outlives it keeps
// going: the registry checks the token per request, and docker asks again.
const TokenTTL = 5 * time.Minute

// GenerateTokenKey writes a new key and certificate into dir (created 0700).
// It refuses to overwrite an existing key: replacing it invalidates the
// registry's bundle until the registry restarts with the new certificate.
func GenerateTokenKey(dir, commonName string) error {
	if commonName == "" {
		commonName = DefaultIssuer + " registry token signer"
	}
	if _, err := os.Stat(filepath.Join(dir, TokenKeyFile)); err == nil {
		return fmt.Errorf("%s already exists; remove it first to rotate the key (then restart the registry)", filepath.Join(dir, TokenKeyFile))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, TokenKeyFile), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, TokenCertFile), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

// Issuer signs registry tokens.
type Issuer struct {
	Issuer, Service string
	TTL             time.Duration
	Now             func() time.Time

	key  *ecdsa.PrivateKey
	cert []byte // DER
}

// LoadIssuer reads the key and certificate from dir.
func LoadIssuer(dir, issuer, service string) (*Issuer, error) {
	kp, err := os.ReadFile(filepath.Join(dir, TokenKeyFile))
	if err != nil {
		return nil, fmt.Errorf("registry token key: %w", err)
	}
	cp, err := os.ReadFile(filepath.Join(dir, TokenCertFile))
	if err != nil {
		return nil, fmt.Errorf("registry token certificate: %w", err)
	}
	kb, _ := pem.Decode(kp)
	cb, _ := pem.Decode(cp)
	if kb == nil || cb == nil {
		return nil, errors.New("registry token key or certificate: not PEM")
	}
	key, err := parseECKey(kb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("registry token key: %w", err)
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("registry token certificate: %w", err)
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		return nil, errors.New("registry token certificate does not match the key")
	}
	if issuer == "" {
		issuer = DefaultIssuer
	}
	if service == "" {
		service = DefaultService
	}
	return &Issuer{Issuer: issuer, Service: service, TTL: TokenTTL, key: key, cert: cb.Bytes}, nil
}

func parseECKey(der []byte) (*ecdsa.PrivateKey, error) {
	if k, err := x509.ParseECPrivateKey(der); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	ek, ok := k.(*ecdsa.PrivateKey)
	if !ok || ek.Curve != elliptic.P256() {
		return nil, errors.New("want an EC P-256 key")
	}
	return ek, nil
}

// Access is one granted (or requested) resource of a token.
type Access struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

// Claims of a registry token.
type Claims struct {
	Issuer    string   `json:"iss"`
	Subject   string   `json:"sub"`
	Audience  string   `json:"aud"`
	ExpiresAt int64    `json:"exp"`
	NotBefore int64    `json:"nbf"`
	IssuedAt  int64    `json:"iat"`
	ID        string   `json:"jti"`
	Access    []Access `json:"access"`
}

// Issue signs a token for subject with access. It returns the token, its
// lifetime in seconds and the issue time.
func (i *Issuer) Issue(subject string, access []Access) (string, int, time.Time, error) {
	now := time.Now()
	if i.Now != nil {
		now = i.Now()
	}
	ttl := i.TTL
	if ttl <= 0 {
		ttl = TokenTTL
	}
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", 0, now, err
	}
	if access == nil {
		access = []Access{}
	}
	head, err := json.Marshal(map[string]any{"typ": "JWT", "alg": "ES256", "x5c": []string{base64.StdEncoding.EncodeToString(i.cert)}})
	if err != nil {
		return "", 0, now, err
	}
	body, err := json.Marshal(Claims{Issuer: i.Issuer, Subject: subject, Audience: i.Service,
		ExpiresAt: now.Add(ttl).Unix(), NotBefore: now.Add(-10 * time.Second).Unix(), IssuedAt: now.Unix(),
		ID: hex.EncodeToString(jti), Access: access})
	if err != nil {
		return "", 0, now, err
	}
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(head) + "." + enc.EncodeToString(body)
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, i.key, sum[:])
	if err != nil {
		return "", 0, now, err
	}
	sig := make([]byte, 64) // JOSE ES256: r and s, 32 bytes each
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signing + "." + enc.EncodeToString(sig), int(ttl / time.Second), now, nil
}

// VerifyToken checks a token's signature against cert (DER) and returns its
// claims. Tests and `images token-check` use it; the registry verifies on
// its own.
func VerifyToken(token string, cert []byte) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("not a JWT")
	}
	c, err := x509.ParseCertificate(cert)
	if err != nil {
		return nil, err
	}
	pub, ok := c.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("certificate key is not EC")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return nil, errors.New("bad signature encoding")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(pub, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return nil, errors.New("signature does not verify")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var out Claims
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CertDER is the issuer's certificate, DER encoded.
func (i *Issuer) CertDER() []byte { return slices.Clone(i.cert) }

var scopeNameRe = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)

// ParseScopes parses the scope parameters of a token request:
// "repository:<name>:<action>[,<action>]", several per parameter separated
// by spaces. Scopes of another type or with a malformed name are dropped
// (a token never grants them).
func ParseScopes(params []string) []Access {
	var out []Access
	for _, p := range params {
		for _, s := range strings.Fields(p) {
			typ, rest, ok := strings.Cut(s, ":")
			if !ok || typ != "repository" {
				continue
			}
			i := strings.LastIndex(rest, ":")
			if i <= 0 {
				continue
			}
			name, actions := rest[:i], strings.Split(rest[i+1:], ",")
			if !scopeNameRe.MatchString(name) || len(name) > 255 {
				continue
			}
			out = append(out, Access{Type: typ, Name: name, Actions: actions})
		}
	}
	return out
}

// Identity is who asks for a token.
type Identity struct {
	Sync  bool   // the operator's sync identity: pull and push
	OrgID string // an org (API or runner token): pull only
}

// AccessPolicy decides what a token grants. Every image lives under
// Namespace/<suite>/...; Restricted lists the suites (licence-restricted
// ones) only some orgs may pull, with those orgs' ids. An empty list allows
// no org at all.
type AccessPolicy struct {
	Namespace  string              `yaml:"namespace"`
	Restricted map[string][]string `yaml:"restricted"`
}

// LoadAccessPolicy reads the operator's access file (YAML). An empty path
// is the default: namespace "library", nothing restricted.
func LoadAccessPolicy(path string) (*AccessPolicy, error) {
	p := &AccessPolicy{}
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("registry access file: %w", err)
		}
		dec := yaml.NewDecoder(strings.NewReader(string(raw)))
		dec.KnownFields(true)
		if err := dec.Decode(p); err != nil && err.Error() != "EOF" {
			return nil, fmt.Errorf("registry access file %s: %w", path, err)
		}
	}
	if p.Namespace == "" {
		p.Namespace = DefaultNamespace
	}
	if !scopeNameRe.MatchString(p.Namespace) {
		return nil, fmt.Errorf("registry access file: bad namespace %q", p.Namespace)
	}
	return p, nil
}

// Grant returns what id gets of the requested access: only repositories
// under the namespace; the sync identity gets pull and push, an org pull,
// and a restricted suite only to the orgs it lists. Nothing else (delete,
// the catalog, other namespaces) is ever granted.
func (p *AccessPolicy) Grant(id Identity, req []Access) []Access {
	out := []Access{}
	for _, a := range req {
		if a.Type != "repository" {
			continue
		}
		rest, ok := strings.CutPrefix(a.Name, p.Namespace+"/")
		if !ok {
			continue
		}
		suite, _, ok := strings.Cut(rest, "/")
		if !ok || suite == "" {
			continue
		}
		var acts []string
		for _, act := range a.Actions {
			switch {
			case act == "pull" && (id.Sync || p.mayPull(id.OrgID, suite)):
				acts = append(acts, act)
			case act == "push" && id.Sync:
				acts = append(acts, act)
			}
		}
		if len(acts) > 0 {
			slices.Sort(acts)
			out = append(out, Access{Type: a.Type, Name: a.Name, Actions: slices.Compact(acts)})
		}
	}
	return out
}

func (p *AccessPolicy) mayPull(org, suite string) bool {
	if org == "" {
		return false
	}
	allowed, restricted := p.Restricted[suite]
	return !restricted || slices.Contains(allowed, org)
}
