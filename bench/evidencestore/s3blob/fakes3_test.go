package s3blob

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeS3 is an in-memory S3 endpoint for unit tests. It checks every
// request on its own terms, without sharing code with the client: it
// recomputes the SigV4 signature from the bytes received on the wire, and
// it checks Content-MD5, x-amz-content-sha256 and x-amz-checksum-sha256
// against the body it received.
type fakeS3 struct {
	t      testing.TB
	srv    *httptest.Server
	bucket string
	domain string // virtual-host requests arrive as bucket.domain[:port]
	access string
	secret string
	region string
	token  string

	mu         sync.Mutex
	exists     bool
	objects    map[string]fakeObject
	uploads    map[string]*fakeUpload
	nextID     int
	requests   []fakeRequest
	createBody []byte
	pageSize   int
	// hook runs after recording and before any check; returning true means
	// it wrote the response.
	hook func(w http.ResponseWriter, r *http.Request, body []byte, n int) bool
}

type fakeObject struct {
	data []byte
	meta http.Header
	etag string
	mod  time.Time
}

type fakeUpload struct {
	key      string
	meta     http.Header
	checksum bool
	parts    map[int][]byte
}

type fakeRequest struct {
	host   string
	method string
	path   string // raw path as received
	query  string // raw query as received
	header http.Header
	body   []byte
}

func newFakeS3(t testing.TB) *fakeS3 {
	f := &fakeS3{
		t:        t,
		bucket:   "evidence",
		domain:   "s3.fake.test",
		access:   "AKIDFAKE",
		secret:   "fake/secret+key=",
		region:   "us-east-1",
		exists:   true,
		objects:  map[string]fakeObject{},
		uploads:  map[string]*fakeUpload{},
		pageSize: 1000,
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// newFakeS3TLS is newFakeS3 over HTTPS. The httptest certificate covers
// example.com and *.example.com, so both addressing styles verify.
func newFakeS3TLS(t testing.TB) *fakeS3 {
	f := newFakeS3(t)
	f.srv.Close()
	f.domain = "example.com"
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// client returns a Client for f with path-style (or virtual-host style)
// addressing; mutate adjusts the config first. Virtual-host requests go
// to the fake through a dialer that sends every host to its listener.
func (f *fakeS3) client(t testing.TB, virtual bool, mutate func(*Config)) *Client {
	t.Helper()
	_, port, err := net.SplitHostPort(f.srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Endpoint:     "http://" + f.domain + ":" + port,
		Region:       f.region,
		Bucket:       f.bucket,
		AccessKey:    f.access,
		SecretKey:    f.secret,
		SessionToken: f.token,
		Addressing:   AddressingPath,
		HTTPClient:   &http.Client{Transport: fakeTransport(f.srv.Listener.Addr().String())},
		sleep:        func(ctx context.Context, d time.Duration) error { return nil },
	}
	if f.srv.TLS != nil {
		cfg.Endpoint = "https://" + f.domain + ":" + port
		tr := fakeTransport(f.srv.Listener.Addr().String())
		tr.TLSClientConfig = f.srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
		cfg.HTTPClient = &http.Client{Transport: tr}
	}
	if virtual {
		cfg.Addressing = AddressingVirtual
	}
	if mutate != nil {
		mutate(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func fakeTransport(addr string) *http.Transport {
	d := &net.Dialer{Timeout: 5 * time.Second}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return d.DialContext(ctx, network, addr)
		},
	}
}

func (f *fakeS3) reqs() []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeRequest(nil), f.requests...)
}

// count returns how many recorded requests have the method and a raw query
// containing sub.
func (f *fakeS3) count(method, sub string) int {
	n := 0
	for _, r := range f.reqs() {
		if r.method == method && strings.Contains(r.query, sub) {
			n++
		}
	}
	return n
}

func (f *fakeS3) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		fakeErr(w, 400, "IncompleteBody", err.Error())
		return
	}
	rawPath, rawQuery, _ := strings.Cut(r.RequestURI, "?")
	f.mu.Lock()
	f.requests = append(f.requests, fakeRequest{r.Host, r.Method, rawPath, rawQuery, r.Header.Clone(), body})
	n := len(f.requests)
	hook := f.hook
	f.mu.Unlock()
	if hook != nil && hook(w, r, body, n) {
		return
	}
	f.process(w, r, body)
}

// process checks and executes one request whose body was already read.
func (f *fakeS3) process(w http.ResponseWriter, r *http.Request, body []byte) {
	rawPath, rawQuery, _ := strings.Cut(r.RequestURI, "?")
	if code, msg := f.verify(r, rawPath, rawQuery, body); code != "" {
		status := 400
		if code == "SignatureDoesNotMatch" || code == "RequestTimeTooSkewed" || code == "AccessDenied" {
			status = 403
		}
		fakeErr(w, status, code, msg)
		return
	}
	key, ok := f.route(r, rawPath)
	if !ok {
		fakeErr(w, 404, "NoSuchBucket", "The specified bucket does not exist")
		return
	}
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		fakeErr(w, 400, "InvalidArgument", err.Error())
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if key == "" {
		f.serveBucket(w, r, q, body)
		return
	}
	f.serveObject(w, r, key, q, body)
}

// verify is the fake's independent request check: strict encoding of path
// and query on the wire, SigV4 recomputed from the received bytes, and the
// body digests.
func (f *fakeS3) verify(r *http.Request, rawPath, rawQuery string, body []byte) (string, string) {
	if !strictEncoded(rawPath, true) {
		return "SignatureDoesNotMatch", "path not canonically encoded: " + rawPath
	}
	var pairs []string
	if rawQuery != "" {
		for _, p := range strings.Split(rawQuery, "&") {
			k, v, ok := strings.Cut(p, "=")
			if !ok || !strictEncoded(k, false) || !strictEncoded(v, false) {
				return "SignatureDoesNotMatch", "query not canonically encoded: " + rawQuery
			}
			pairs = append(pairs, p)
		}
	}
	sort.Strings(pairs)
	auth := r.Header.Get("Authorization")
	cred, signedHeaders, sig, ok := parseAuth(auth)
	if !ok {
		return "AccessDenied", "bad authorization header"
	}
	credParts := strings.Split(cred, "/")
	if len(credParts) != 5 || credParts[0] != f.access || credParts[2] != f.region || credParts[3] != "s3" || credParts[4] != "aws4_request" {
		return "InvalidAccessKeyId", "bad credential scope"
	}
	amzDate := r.Header.Get("X-Amz-Date")
	ts, err := time.Parse("20060102T150405Z", amzDate)
	if err != nil || credParts[1] != amzDate[:8] {
		return "AccessDenied", "bad x-amz-date"
	}
	if d := time.Since(ts); d > 15*time.Minute || d < -15*time.Minute {
		return "RequestTimeTooSkewed", "The difference between the request time and the current time is too large."
	}
	payload := r.Header.Get("X-Amz-Content-Sha256")
	bodySum := sha256.Sum256(body)
	if payload != hex.EncodeToString(bodySum[:]) {
		return "XAmzContentSHA256Mismatch", "The provided 'x-amz-content-sha256' header does not match what was computed."
	}
	signed := strings.Split(signedHeaders, ";")
	need := []string{"host", "x-amz-date", "x-amz-content-sha256"}
	if f.token != "" {
		need = append(need, "x-amz-security-token")
		if r.Header.Get("X-Amz-Security-Token") != f.token {
			return "InvalidToken", "bad session token"
		}
	}
	for _, h := range []string{"content-md5", "content-type", "x-amz-meta-sha256", "x-amz-checksum-sha256"} {
		if r.Header.Get(h) != "" {
			need = append(need, h)
		}
	}
	for _, h := range need {
		if !contains(signed, h) {
			return "AccessDenied", "header " + h + " not signed"
		}
	}
	var ch strings.Builder
	for _, h := range signed {
		v := r.Host
		if h != "host" {
			v = strings.Join(strings.Fields(strings.Join(r.Header.Values(h), ",")), " ")
		}
		ch.WriteString(h + ":" + v + "\n")
	}
	creq := r.Method + "\n" + rawPath + "\n" + strings.Join(pairs, "&") + "\n" + ch.String() + "\n" + signedHeaders + "\n" + payload
	if want := fakeSignature(f.secret, ts, f.region, creq); want != sig {
		return "SignatureDoesNotMatch", "The request signature we calculated does not match the signature you provided."
	}
	if m := r.Header.Get("Content-Md5"); m != "" {
		got := md5.Sum(body)
		if m != base64.StdEncoding.EncodeToString(got[:]) {
			return "BadDigest", "The Content-MD5 you specified did not match what we received."
		}
	}
	if c := r.Header.Get("X-Amz-Checksum-Sha256"); c != "" && c != base64.StdEncoding.EncodeToString(bodySum[:]) {
		return "BadDigest", "The SHA256 you specified did not match the calculated checksum."
	}
	return "", ""
}

// route splits the received path into the object key ("" for a bucket
// request), honouring virtual-host and path style.
func (f *fakeS3) route(r *http.Request, rawPath string) (string, bool) {
	p, err := url.PathUnescape(rawPath)
	if err != nil {
		return "", false
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	if host == f.bucket+"."+f.domain {
		return strings.TrimPrefix(p, "/"), true
	}
	b, key, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	return key, b == f.bucket
}

func (f *fakeS3) serveBucket(w http.ResponseWriter, r *http.Request, q url.Values, body []byte) {
	switch {
	case r.Method == http.MethodPut:
		f.createBody = body
		if f.exists {
			fakeErr(w, 409, "BucketAlreadyOwnedByYou", "Your previous request to create the named bucket succeeded and you already own it.")
			return
		}
		f.exists = true
	case r.Method == http.MethodHead:
	case r.Method == http.MethodGet && q.Get("list-type") == "2":
		f.list(w, q)
	default:
		fakeErr(w, 400, "NotImplemented", "fake does not do this")
	}
}

func (f *fakeS3) list(w http.ResponseWriter, q url.Values) {
	prefix, after := q.Get("prefix"), ""
	if tok := q.Get("continuation-token"); tok != "" {
		b, err := base64.StdEncoding.DecodeString(tok)
		if err != nil {
			fakeErr(w, 400, "InvalidArgument", "bad token")
			return
		}
		after = string(b)
	}
	var keys []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) && k > after {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
	truncated := len(keys) > f.pageSize
	if truncated {
		keys = keys[:f.pageSize]
	}
	for _, k := range keys {
		o := f.objects[k]
		var ek bytes.Buffer
		if err := xml.EscapeText(&ek, []byte(k)); err != nil {
			f.t.Error(err)
		}
		fmt.Fprintf(&b, "<Contents><Key>%s</Key><LastModified>%s</LastModified><ETag>&quot;%s&quot;</ETag><Size>%d</Size><StorageClass>STANDARD</StorageClass></Contents>",
			ek.String(), o.mod.Format("2006-01-02T15:04:05.000Z"), o.etag, len(o.data))
	}
	fmt.Fprintf(&b, "<IsTruncated>%t</IsTruncated>", truncated)
	if truncated {
		fmt.Fprintf(&b, "<NextContinuationToken>%s</NextContinuationToken>", base64.StdEncoding.EncodeToString([]byte(keys[len(keys)-1])))
	}
	b.WriteString("</ListBucketResult>")
	w.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(w, b.String())
}

func (f *fakeS3) serveObject(w http.ResponseWriter, r *http.Request, key string, q url.Values, body []byte) {
	_, hasUploads := q["uploads"]
	uploadID := q.Get("uploadId")
	switch {
	case r.Method == http.MethodPost && hasUploads:
		f.nextID++
		id := "upload-" + strconv.Itoa(f.nextID)
		f.uploads[id] = &fakeUpload{key: key, meta: metaOf(r.Header), checksum: r.Header.Get("X-Amz-Checksum-Algorithm") == "SHA256", parts: map[int][]byte{}}
		fmt.Fprintf(w, `<InitiateMultipartUploadResult><Bucket>%s</Bucket><Key>%s</Key><UploadId>%s</UploadId></InitiateMultipartUploadResult>`, f.bucket, key, id)
	case r.Method == http.MethodPut && uploadID != "":
		u, ok := f.uploads[uploadID]
		if !ok {
			fakeErr(w, 404, "NoSuchUpload", "no such upload")
			return
		}
		pn, err := strconv.Atoi(q.Get("partNumber"))
		if err != nil || pn < 1 {
			fakeErr(w, 400, "InvalidArgument", "bad part number")
			return
		}
		u.parts[pn] = body
		w.Header().Set("ETag", `"`+md5hex(body)+`"`)
	case r.Method == http.MethodPost && uploadID != "":
		f.complete(w, key, uploadID, body)
	case r.Method == http.MethodDelete && uploadID != "":
		if _, ok := f.uploads[uploadID]; !ok {
			fakeErr(w, 404, "NoSuchUpload", "no such upload")
			return
		}
		delete(f.uploads, uploadID)
		w.WriteHeader(204)
	case r.Method == http.MethodPut:
		f.objects[key] = fakeObject{data: body, meta: metaOf(r.Header), etag: md5hex(body), mod: time.Now().UTC().Truncate(time.Second)}
		w.Header().Set("ETag", `"`+md5hex(body)+`"`)
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		o, ok := f.objects[key]
		if !ok {
			if r.Method == http.MethodHead {
				w.WriteHeader(404)
				return
			}
			fakeErr(w, 404, "NoSuchKey", "The specified key does not exist.")
			return
		}
		for k, v := range o.meta {
			w.Header()[k] = v
		}
		w.Header().Set("ETag", `"`+o.etag+`"`)
		w.Header().Set("Last-Modified", o.mod.Format(http.TimeFormat))
		w.Header().Set("Content-Length", strconv.Itoa(len(o.data)))
		if r.Method == http.MethodGet {
			_, _ = w.Write(o.data)
		}
	case r.Method == http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(204)
	default:
		fakeErr(w, 400, "NotImplemented", "fake does not do this")
	}
}

func (f *fakeS3) complete(w http.ResponseWriter, key, uploadID string, body []byte) {
	u, ok := f.uploads[uploadID]
	if !ok {
		fakeErr(w, 404, "NoSuchUpload", "no such upload")
		return
	}
	var req struct {
		Parts []struct {
			PartNumber     int
			ETag           string
			ChecksumSHA256 string
		} `xml:"Part"`
	}
	if err := xml.Unmarshal(body, &req); err != nil || len(req.Parts) == 0 {
		fakeErr(w, 400, "MalformedXML", "bad complete body")
		return
	}
	var data []byte
	for i, p := range req.Parts {
		part, ok := u.parts[p.PartNumber]
		if p.PartNumber != i+1 || !ok {
			fakeErr(w, 400, "InvalidPartOrder", "parts out of order or missing")
			return
		}
		if strings.Trim(p.ETag, `"`) != md5hex(part) {
			fakeErr(w, 400, "InvalidPart", "etag mismatch")
			return
		}
		sum := sha256.Sum256(part)
		if u.checksum && p.ChecksumSHA256 != base64.StdEncoding.EncodeToString(sum[:]) {
			fakeErr(w, 400, "InvalidPart", "checksum mismatch")
			return
		}
		if i < len(req.Parts)-1 && len(part) < MinPartSize {
			fakeErr(w, 400, "EntityTooSmall", "part too small")
			return
		}
		data = append(data, part...)
	}
	delete(f.uploads, uploadID)
	etag := md5hex(data) + "-" + strconv.Itoa(len(req.Parts))
	f.objects[key] = fakeObject{data: data, meta: u.meta, etag: etag, mod: time.Now().UTC().Truncate(time.Second)}
	fmt.Fprintf(w, `<CompleteMultipartUploadResult><Key>%s</Key><ETag>"%s"</ETag></CompleteMultipartUploadResult>`, key, etag)
}

func metaOf(h http.Header) http.Header {
	m := http.Header{}
	for k, v := range h {
		if strings.HasPrefix(strings.ToLower(k), "x-amz-meta-") {
			m[k] = v
		}
	}
	return m
}

func fakeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("X-Amz-Request-Id", "FAKEREQ")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message><RequestId>FAKEREQ</RequestId><AWSAccessKeyId>AKIDFAKE</AWSAccessKeyId></Error>`, code, msg)
}

func md5hex(b []byte) string {
	s := md5.Sum(b)
	return hex.EncodeToString(s[:])
}

// strictEncoded reports whether s uses only unreserved characters,
// uppercase %XX escapes and, when slashOK, '/'.
func strictEncoded(s string, slashOK bool) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
		case c == '/' && slashOK:
		case c == '%' && i+2 < len(s) && isUpperHex(s[i+1]) && isUpperHex(s[i+2]):
			i += 2
		default:
			return false
		}
	}
	return true
}

func isUpperHex(c byte) bool { return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'F') }

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// parseAuth splits "AWS4-HMAC-SHA256 Credential=..., SignedHeaders=...,
// Signature=...".
func parseAuth(h string) (cred, signed, sig string, ok bool) {
	rest, found := strings.CutPrefix(h, "AWS4-HMAC-SHA256 ")
	if !found {
		return "", "", "", false
	}
	for _, part := range strings.Split(rest, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "Credential":
			cred = v
		case "SignedHeaders":
			signed = v
		case "Signature":
			sig = v
		}
	}
	return cred, signed, sig, cred != "" && signed != "" && sig != ""
}

// fakeSignature is a second, test-only SigV4 implementation: it shares no
// code with signer so a bug in one is caught by the other.
func fakeSignature(secret string, t time.Time, region, creq string) string {
	mac := func(k []byte, s string) []byte {
		m := hmac.New(sha256.New, k)
		m.Write([]byte(s))
		return m.Sum(nil)
	}
	day := t.Format("20060102")
	k := mac(mac(mac(mac([]byte("AWS4"+secret), day), region), "s3"), "aws4_request")
	h := sha256.Sum256([]byte(creq))
	sts := "AWS4-HMAC-SHA256\n" + t.Format("20060102T150405Z") + "\n" + day + "/" + region + "/s3/aws4_request\n" + hex.EncodeToString(h[:])
	return hex.EncodeToString(mac(k, sts))
}
