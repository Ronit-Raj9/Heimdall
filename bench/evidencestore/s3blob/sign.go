package s3blob

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	sigAlgorithm   = "AWS4-HMAC-SHA256"
	sigService     = "s3"
	amzDateFormat  = "20060102T150405Z"
	amzShortFormat = "20060102"
	// emptySHA256 is the hex sha256 of zero bytes, the signed payload of
	// every request without a body.
	emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// unsignedHeaders are left out of the signature: the transport or a proxy
// may add, drop or rewrite them after signing.
var unsignedHeaders = map[string]bool{
	"authorization":     true,
	"user-agent":        true,
	"accept-encoding":   true,
	"content-length":    true,
	"connection":        true,
	"expect":            true,
	"transfer-encoding": true,
	"x-amzn-trace-id":   true,
}

// signer computes AWS Signature Version 4 for S3. It holds the credentials
// so that nothing else in the package touches the secret.
type signer struct {
	accessKey    string
	secretKey    string
	sessionToken string
	region       string
}

// sign stamps req with x-amz-date, x-amz-content-sha256 (the real hex
// sha256 of the body, never UNSIGNED-PAYLOAD) and, with temporary
// credentials, x-amz-security-token, then adds the Authorization header.
//
// It also rewrites req.URL.RawPath and req.URL.RawQuery to the canonical
// encodings it signed, so the bytes on the wire are exactly the bytes in the
// canonical request. S3 encodes a path once (no double encoding) and keeps
// '/', which differs from Go's default path escaping for characters such as
// '$', '+', '=' and ':'; letting net/http escape the path on its own would
// make the server compute a different signature.
func (s signer) sign(req *http.Request, payloadHash string, t time.Time) {
	t = t.UTC()
	req.Header.Set("X-Amz-Date", t.Format(amzDateFormat))
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if s.sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", s.sessionToken)
	}
	req.URL.RawPath = canonicalURI(req.URL.Path)
	req.URL.RawQuery = canonicalQuery(req.URL.Query())

	creq, signed := canonicalRequest(req, payloadHash)
	scope := credentialScope(t, s.region)
	sts := stringToSign(t, scope, creq)
	sig := hex.EncodeToString(hmacSHA256(signingKey(s.secretKey, t, s.region), sts))
	req.Header.Set("Authorization", sigAlgorithm+" Credential="+s.accessKey+"/"+scope+
		", SignedHeaders="+signed+", Signature="+sig)
}

// canonicalRequest returns the SigV4 canonical request for req and its
// semicolon-joined signed header list. The URI and query come from the
// already canonical RawPath and RawQuery that sign sets.
func canonicalRequest(req *http.Request, payloadHash string) (string, string) {
	headers, signed := canonicalHeaders(req)
	uri := req.URL.RawPath
	if uri == "" {
		uri = canonicalURI(req.URL.Path)
	}
	return strings.Join([]string{
		req.Method,
		uri,
		req.URL.RawQuery,
		headers,
		signed,
		payloadHash,
	}, "\n"), signed
}

// canonicalHeaders lowercases names, trims values and collapses inner runs
// of spaces, joins repeated headers with commas, sorts by name and always
// includes host.
func canonicalHeaders(req *http.Request) (string, string) {
	vals := map[string]string{}
	for name, vs := range req.Header {
		lower := strings.ToLower(name)
		if unsignedHeaders[lower] {
			continue
		}
		cleaned := make([]string, len(vs))
		for i, v := range vs {
			cleaned[i] = collapseSpaces(v)
		}
		vals[lower] = strings.Join(cleaned, ",")
	}
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	vals["host"] = host
	names := make([]string, 0, len(vals))
	for n := range vals {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte(':')
		b.WriteString(vals[n])
		b.WriteByte('\n')
	}
	return b.String(), strings.Join(names, ";")
}

// collapseSpaces trims a header value and turns each inner run of spaces
// into one space, as SigV4 requires.
func collapseSpaces(v string) string {
	return strings.Join(strings.Fields(v), " ")
}

// canonicalURI encodes a decoded path the S3 way: every byte except the
// unreserved characters A-Z a-z 0-9 - _ . ~ and the '/' separator becomes
// %XX with uppercase hex, once. An empty path is "/".
func canonicalURI(p string) string {
	if p == "" {
		return "/"
	}
	return uriEncode(p, false)
}

// canonicalQuery encodes every name and value (including '/'), sorts by
// name then value, and writes "name=" for a parameter without a value, so
// "?uploads" signs as "uploads=".
func canonicalQuery(q url.Values) string {
	pairs := make([]string, 0, len(q))
	for k, vs := range q {
		ek := uriEncode(k, true)
		if len(vs) == 0 {
			pairs = append(pairs, ek+"=")
			continue
		}
		for _, v := range vs {
			pairs = append(pairs, ek+"="+uriEncode(v, true))
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

// uriEncode percent-encodes s byte by byte, keeping the unreserved set and,
// unless encodeSlash, the '/' character.
func uriEncode(s string, encodeSlash bool) string {
	const hexUpper = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexUpper[c>>4])
			b.WriteByte(hexUpper[c&0x0f])
		}
	}
	return b.String()
}

// credentialScope is date/region/s3/aws4_request.
func credentialScope(t time.Time, region string) string {
	return t.UTC().Format(amzShortFormat) + "/" + region + "/" + sigService + "/aws4_request"
}

// stringToSign hashes the canonical request into the SigV4 string to sign.
func stringToSign(t time.Time, scope, creq string) string {
	h := sha256.Sum256([]byte(creq))
	return sigAlgorithm + "\n" + t.UTC().Format(amzDateFormat) + "\n" + scope + "\n" + hex.EncodeToString(h[:])
}

// signingKey derives the per-day, per-region SigV4 key from the secret.
func signingKey(secret string, t time.Time, region string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), t.UTC().Format(amzShortFormat))
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, sigService)
	return hmacSHA256(k, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}
