package platformimport

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Credentials used only while fetching a definition. The imported agent
// config names the org secrets; no value ever leaves this package.

// sigV4 signs req with AWS Signature Version 4 (header form, signed
// payload hash).
func sigV4(req *http.Request, body []byte, accessKey, secretKey, sessionToken, region, service string, now time.Time) {
	signV4(req, body, true, accessKey, secretKey, sessionToken, region, service, now)
}

// signV4 is sigV4 with the x-amz-content-sha256 header optional (the AWS
// test suite signs without it).
func signV4(req *http.Request, body []byte, payloadHeader bool, accessKey, secretKey, sessionToken, region, service string, now time.Time) {
	t := now.UTC()
	amzDate := t.Format("20060102T150405Z")
	day := t.Format("20060102")
	sum := sha256.Sum256(body)
	payload := hex.EncodeToString(sum[:])
	req.Header.Set("X-Amz-Date", amzDate)
	if payloadHeader {
		req.Header.Set("X-Amz-Content-Sha256", payload)
	}
	if sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", sessionToken)
	}
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	hdr := map[string]string{"host": host}
	for name, vs := range req.Header {
		l := strings.ToLower(name)
		if l == "authorization" || l == "user-agent" || l == "accept-encoding" || l == "content-length" {
			continue
		}
		hdr[l] = strings.Join(strings.Fields(strings.Join(vs, ",")), " ")
	}
	names := make([]string, 0, len(hdr))
	for n := range hdr {
		names = append(names, n)
	}
	sort.Strings(names)
	var ch strings.Builder
	for _, n := range names {
		ch.WriteString(n + ":" + hdr[n] + "\n")
	}
	signed := strings.Join(names, ";")
	// Non-S3 services: each path segment is URI-encoded twice (once on the
	// wire, once for the canonical request).
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	segs := strings.Split(path, "/")
	wire := make([]string, len(segs))
	for i, s := range segs {
		if u, err := url.PathUnescape(s); err == nil {
			s = u
		}
		wire[i] = awsEncode(s)
		segs[i] = awsEncode(wire[i])
	}
	req.URL.RawPath = strings.Join(wire, "/")
	canonPath := strings.Join(segs, "/")
	q := req.URL.Query()
	pairs := make([]string, 0, len(q))
	for k, vs := range q {
		for _, v := range vs {
			pairs = append(pairs, awsEncode(k)+"="+awsEncode(v))
		}
	}
	sort.Strings(pairs)
	req.URL.RawQuery = strings.Join(pairs, "&")
	creq := strings.Join([]string{req.Method, canonPath, strings.Join(pairs, "&"), ch.String(), signed, payload}, "\n")
	scope := day + "/" + region + "/" + service + "/aws4_request"
	h := sha256.Sum256([]byte(creq))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(h[:])
	k := hmacSum([]byte("AWS4"+secretKey), day)
	k = hmacSum(k, region)
	k = hmacSum(k, service)
	k = hmacSum(k, "aws4_request")
	sig := hex.EncodeToString(hmacSum(k, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+accessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

func hmacSum(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// awsEncode percent-encodes every byte outside A-Z a-z 0-9 - _ . ~.
func awsEncode(s string) string {
	const hexUpper = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexUpper[c>>4])
		b.WriteByte(hexUpper[c&0x0f])
	}
	return b.String()
}

// clientCredentials runs an OAuth 2 client credentials grant and returns
// the access token and, when the server sends one, instance_url
// (Salesforce).
func (c *Client) clientCredentials(ctx context.Context, tokenURL, clientID, clientSecret, scope, what string) (string, string, error) {
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {clientID}, "client_secret": {clientSecret}}
	if scope != "" {
		form.Set("scope", scope)
	}
	doc, err := c.getJSON(ctx, call{method: http.MethodPost, url: tokenURL, form: form, what: what + " token endpoint",
		secrets: []string{clientID, clientSecret}})
	if err != nil {
		return "", "", err
	}
	tok := str(doc, "access_token")
	if tok == "" {
		return "", "", fmt.Errorf("%s token endpoint answered without an access_token", what)
	}
	return tok, str(doc, "instance_url"), nil
}

// googleKey is the part of a service account JSON key the importer reads.
type googleKey struct {
	Type         string `json:"type"`
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id"`
	ProjectID    string `json:"project_id"`
	TokenURI     string `json:"token_uri"`
}

func parseGoogleKey(keyJSON string) (googleKey, *rsa.PrivateKey, error) {
	var key googleKey
	if err := json.Unmarshal([]byte(keyJSON), &key); err != nil || key.ClientEmail == "" || key.PrivateKey == "" {
		return key, nil, errors.New("the service account secret is not a Google service account JSON key (client_email and private_key)")
	}
	block, _ := pem.Decode([]byte(key.PrivateKey))
	if block == nil {
		return key, nil, errors.New("the service account private_key is not PEM")
	}
	var priv *rsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		priv, _ = k.(*rsa.PrivateKey)
	} else if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		priv = k
	}
	if priv == nil {
		return key, nil, errors.New("the service account private_key is not an RSA key")
	}
	return key, priv, nil
}

// GoogleTokenURL is the OAuth token endpoint of a service account key.
const GoogleTokenURL = "https://oauth2.googleapis.com/token"

// CloudPlatformScope is the scope the Vertex AI API needs.
const CloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// googleAccessToken exchanges a service account key for an OAuth access
// token with scope (JWT bearer grant, RFC 7523).
func (c *Client) googleAccessToken(ctx context.Context, keyJSON, scope string) (string, googleKey, error) {
	key, priv, err := parseGoogleKey(keyJSON)
	if err != nil {
		return "", key, err
	}
	head := map[string]string{"alg": "RS256", "typ": "JWT"}
	if key.PrivateKeyID != "" {
		head["kid"] = key.PrivateKeyID
	}
	iat := c.now().Unix()
	claims := map[string]any{"iss": key.ClientEmail, "sub": key.ClientEmail, "aud": GoogleTokenURL,
		"iat": iat, "exp": iat + 3600, "scope": scope}
	hb, _ := json.Marshal(head)
	cb, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(hb) + "." + enc.EncodeToString(cb)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(nil, priv, crypto.SHA256, sum[:])
	if err != nil {
		return "", key, fmt.Errorf("sign the service account assertion: %v", err)
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {signing + "." + enc.EncodeToString(sig)}}
	doc, err := c.getJSON(ctx, call{method: http.MethodPost, url: GoogleTokenURL, form: form, what: "the Google token endpoint"})
	if err != nil {
		return "", key, err
	}
	tok := str(doc, "access_token")
	if tok == "" {
		return "", key, errors.New("the Google token endpoint answered without an access_token")
	}
	return tok, key, nil
}
