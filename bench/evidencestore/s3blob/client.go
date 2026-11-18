// Package s3blob is an evidencestore.Blob over the S3 REST API, written on
// net/http with no SDK. It signs every request with AWS Signature Version 4
// over the real sha256 of the body, sends Content-MD5 so the service checks
// the bytes it receives, records the object's sha256 as x-amz-meta-sha256 so
// Head and Get can report it, switches to multipart uploads for large
// objects, and retries throttling and server faults with backoff. It works
// with AWS S3 and with S3-compatible services (MinIO, Cloudflare R2,
// Backblaze B2) in path-style or virtual-host style.
package s3blob

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/maximhq/bifrost/bench/evidencestore"
)

const (
	// DefaultRegion is used when Config.Region is empty.
	DefaultRegion = "us-east-1"
	// DefaultPartSize is the multipart part size when Config.PartSize is 0.
	DefaultPartSize = 16 << 20
	// MinPartSize is the smallest part S3 accepts (except the last part).
	MinPartSize = 5 << 20
	// DefaultMultipartThreshold is the object size from which Put uses a
	// multipart upload when Config.MultipartThreshold is 0.
	DefaultMultipartThreshold = 64 << 20
	// DefaultMaxAttempts bounds the tries per request when
	// Config.MaxAttempts is 0.
	DefaultMaxAttempts = 5

	maxSinglePut = 5 << 30
	maxParts     = 10000
	metaSHA256   = "X-Amz-Meta-Sha256"
	s3Namespace  = "http://s3.amazonaws.com/doc/2006-03-01/"
)

// Addressing modes for Config.Addressing.
const (
	AddressingAuto    = "auto"
	AddressingPath    = "path"
	AddressingVirtual = "virtual"
)

// Config locates a bucket and holds the credentials to reach it.
//
// Addressing "auto" uses virtual-host style (bucket.host) only for AWS
// endpoints and buckets whose names are DNS labels without dots; anything
// else, including every custom endpoint, uses path style (host/bucket),
// which MinIO, R2 and B2 all accept and which needs no wildcard DNS.
type Config struct {
	Endpoint           string // https://host[:port] or http://...; "" means https://s3.<region>.amazonaws.com
	Region             string // default us-east-1
	Bucket             string
	Prefix             string // optional key prefix inside the bucket; List strips it
	AccessKey          string
	SecretKey          string
	SessionToken       string // set for temporary credentials
	Addressing         string // auto (default), path or virtual
	ChecksumSHA256     bool   // also send x-amz-checksum-sha256
	PartSize           int64  // default 16 MiB, at least 5 MiB
	MultipartThreshold int64  // default 64 MiB
	MaxAttempts        int    // default 5
	HTTPClient         *http.Client

	now   func() time.Time                                 // test hook
	sleep func(ctx context.Context, d time.Duration) error // test hook
}

// Client talks to one bucket. It is safe for concurrent use.
type Client struct {
	cfg       Config
	scheme    string
	host      string // endpoint host[:port], default port removed
	prefix    string // "" or "dir/"
	virtual   bool
	signer    signer
	http      *http.Client
	now       func() time.Time
	sleep     func(ctx context.Context, d time.Duration) error
	skew      atomic.Int64 // server clock minus local clock, nanoseconds
	jitterSrc func(n int64) int64
}

var _ evidencestore.Blob = (*Client)(nil)

// New checks cfg, fills in defaults and returns a client. It does not
// contact the service.
func New(cfg Config) (*Client, error) {
	if cfg.Region == "" {
		cfg.Region = DefaultRegion
	}
	if err := validBucket(cfg.Bucket); err != nil {
		return nil, err
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("s3blob: access key and secret key are required")
	}
	if strings.ContainsAny(cfg.Region, "/ \t\n") {
		return nil, fmt.Errorf("s3blob: invalid region %q", cfg.Region)
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://s3." + cfg.Region + ".amazonaws.com"
	}
	scheme, host, err := parseEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	prefix, err := normalizePrefix(cfg.Prefix)
	if err != nil {
		return nil, err
	}
	if err := applyDefaults(&cfg); err != nil {
		return nil, err
	}
	virtual, err := useVirtual(cfg.Addressing, host, cfg.Bucket)
	if err != nil {
		return nil, err
	}
	var hc http.Client
	if cfg.HTTPClient != nil {
		hc = *cfg.HTTPClient
	} else if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		// No whole-request timeout (a large part may take minutes), but a
		// server that accepts a request and never answers is given up on
		// and retried instead of stalling a background pass forever.
		tr := dt.Clone()
		tr.ResponseHeaderTimeout = 2 * time.Minute
		hc.Transport = tr
	}
	// A redirect would resend a signed request to a host it was not
	// signed for; S3 redirects (wrong region) surface as errors instead.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := &Client{
		cfg:     cfg,
		scheme:  scheme,
		host:    host,
		prefix:  prefix,
		virtual: virtual,
		signer: signer{
			accessKey:    cfg.AccessKey,
			secretKey:    cfg.SecretKey,
			sessionToken: cfg.SessionToken,
			region:       cfg.Region,
		},
		http:      &hc,
		now:       cfg.now,
		sleep:     cfg.sleep,
		jitterSrc: randInt63n,
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.sleep == nil {
		c.sleep = sleepCtx
	}
	return c, nil
}

// String names the bucket, prefix and endpoint host, never credentials:
// "s3://bucket/prefix (host)", or "s3://bucket (host)" without a prefix.
func (c *Client) String() string {
	loc := "s3://" + c.cfg.Bucket
	if c.prefix != "" {
		loc += "/" + strings.TrimSuffix(c.prefix, "/")
	}
	return loc + " (" + c.host + ")"
}

// Put stores the object. It hashes the body first (sha256 and MD5) and
// refuses with evidencestore.ErrChecksum, sending nothing, when the bytes
// do not match in.SHA256 or in.Size. The service then checks Content-MD5
// and the signed sha256 of what it receives, so a body corrupted in flight
// is refused too.
func (c *Client) Put(ctx context.Context, key string, in evidencestore.PutInput) error {
	if err := evidencestore.ValidKey(key); err != nil {
		return fmt.Errorf("s3 put: %w", err)
	}
	if in.Body == nil || in.Size < 0 {
		return fmt.Errorf("s3 put %s: missing body or negative size", key)
	}
	if !isHexSHA256(in.SHA256) {
		return fmt.Errorf("s3 put %s: sha256 %q is not lowercase hex", key, in.SHA256)
	}
	if in.Size >= c.cfg.MultipartThreshold {
		return c.putMultipart(ctx, key, in)
	}
	sum, err := hashBody(in.Body, in.Size, 0)
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	if got := hex.EncodeToString(sum.whole); got != in.SHA256 {
		return fmt.Errorf("s3 put %s: body sha256 %s, want %s: %w", key, got, in.SHA256, evidencestore.ErrChecksum)
	}
	h := http.Header{}
	h.Set("Content-Type", contentType(in.ContentType))
	h.Set("Content-Md5", base64.StdEncoding.EncodeToString(sum.wholeMD5))
	h.Set(metaSHA256, in.SHA256)
	if c.cfg.ChecksumSHA256 {
		h.Set("X-Amz-Checksum-Sha256", base64.StdEncoding.EncodeToString(sum.whole))
	}
	resp, err := c.do(ctx, request{
		method:  http.MethodPut,
		key:     key,
		header:  h,
		body:    in.Body,
		size:    in.Size,
		payload: in.SHA256,
	})
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	return closeBody(resp)
}

// Get opens the object. The info comes from the response headers; its
// SHA256 is the x-amz-meta-sha256 that Put recorded.
func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, evidencestore.ObjectInfo, error) {
	if err := evidencestore.ValidKey(key); err != nil {
		return nil, evidencestore.ObjectInfo{}, fmt.Errorf("s3 get: %w", err)
	}
	resp, err := c.do(ctx, request{method: http.MethodGet, key: key, payload: emptySHA256, stream: true})
	if err != nil {
		return nil, evidencestore.ObjectInfo{}, fmt.Errorf("s3 get %s: %w", key, err)
	}
	return resp.Body, infoFromHeader(key, resp), nil
}

// Head returns the object's size, ETag, modification time and recorded
// sha256 without its body.
func (c *Client) Head(ctx context.Context, key string) (evidencestore.ObjectInfo, error) {
	if err := evidencestore.ValidKey(key); err != nil {
		return evidencestore.ObjectInfo{}, fmt.Errorf("s3 head: %w", err)
	}
	resp, err := c.do(ctx, request{method: http.MethodHead, key: key, payload: emptySHA256})
	if err != nil {
		return evidencestore.ObjectInfo{}, fmt.Errorf("s3 head %s: %w", key, err)
	}
	info := infoFromHeader(key, resp)
	return info, closeBody(resp)
}

// List pages through ListObjectsV2 and returns every object under prefix,
// sorted by key, with the client prefix removed. S3 listings carry no
// user metadata, so SHA256 is "".
func (c *Client) List(ctx context.Context, prefix string) ([]evidencestore.ObjectInfo, error) {
	if err := evidencestore.ValidPrefix(prefix); err != nil {
		return nil, fmt.Errorf("s3 list: %w", err)
	}
	var out []evidencestore.ObjectInfo
	token := ""
	for page := 0; ; page++ {
		q := url.Values{"list-type": {"2"}, "prefix": {c.prefix + prefix}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		resp, err := c.do(ctx, request{method: http.MethodGet, bucketOnly: true, query: q, payload: emptySHA256, buffer: true})
		if err != nil {
			return nil, fmt.Errorf("s3 list %q: %w", prefix, err)
		}
		var res listResult
		err = decodeXML(resp, &res)
		if err != nil {
			return nil, fmt.Errorf("s3 list %q page %d: %w", prefix, page, err)
		}
		for _, o := range res.Contents {
			k, ok := strings.CutPrefix(o.Key, c.prefix)
			if !ok {
				continue
			}
			out = append(out, evidencestore.ObjectInfo{
				Key:          k,
				Size:         o.Size,
				ETag:         strings.Trim(o.ETag, `"`),
				LastModified: o.LastModified,
			})
		}
		if !res.IsTruncated {
			break
		}
		if res.NextContinuationToken == "" || res.NextContinuationToken == token {
			return nil, fmt.Errorf("s3 list %q: truncated page %d without a new continuation token", prefix, page)
		}
		token = res.NextContinuationToken
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Delete removes the object; a missing object is not an error.
func (c *Client) Delete(ctx context.Context, key string) error {
	if err := evidencestore.ValidKey(key); err != nil {
		return fmt.Errorf("s3 delete: %w", err)
	}
	resp, err := c.do(ctx, request{method: http.MethodDelete, key: key, payload: emptySHA256})
	if errors.Is(err, evidencestore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("s3 delete %s: %w", key, err)
	}
	return closeBody(resp)
}

// MakeBucket creates the bucket and succeeds when it already exists and the
// caller can reach it. Outside us-east-1 it sends the LocationConstraint S3
// requires.
func (c *Client) MakeBucket(ctx context.Context) error {
	var body []byte
	if c.cfg.Region != DefaultRegion {
		body = []byte(`<CreateBucketConfiguration xmlns="` + s3Namespace + `"><LocationConstraint>` +
			xmlEscape(c.cfg.Region) + `</LocationConstraint></CreateBucketConfiguration>`)
	}
	resp, err := c.do(ctx, bytesRequest(http.MethodPut, "", true, nil, nil, body))
	if err == nil {
		return closeBody(resp)
	}
	var se *Error
	if !errors.As(err, &se) {
		return fmt.Errorf("s3 make bucket %s: %w", c.cfg.Bucket, err)
	}
	switch se.Code {
	case "BucketAlreadyOwnedByYou":
		return nil
	case "BucketAlreadyExists":
		// Taken by someone: fine only if these credentials can use it.
		hresp, herr := c.do(ctx, request{method: http.MethodHead, bucketOnly: true, payload: emptySHA256})
		if herr != nil {
			return fmt.Errorf("s3 make bucket %s: %w (head: %v)", c.cfg.Bucket, err, herr)
		}
		return closeBody(hresp)
	}
	return fmt.Errorf("s3 make bucket %s: %w", c.cfg.Bucket, err)
}

// bodySum holds the hashes of one pass over a body: the whole-object sha256
// and MD5 and, for multipart, each part's sha256 and MD5.
type bodySum struct {
	whole    []byte
	wholeMD5 []byte
	parts    []partSum
}

type partSum struct {
	sha256 []byte
	md5    []byte
	size   int64
}

// hashBody reads exactly size bytes from body in one pass. With partSize
// above 0 it also hashes each partSize section. A body shorter or longer
// than size fails with evidencestore.ErrChecksum.
func hashBody(body io.ReaderAt, size, partSize int64) (bodySum, error) {
	whole, wholeMD5 := sha256.New(), md5.New()
	var sum bodySum
	if partSize <= 0 || partSize > size {
		partSize = size
	}
	buf := make([]byte, 1<<20)
	for off := int64(0); ; {
		n := min(partSize, size-off)
		ps, pm := sha256.New(), md5.New()
		w := io.MultiWriter(whole, wholeMD5, ps, pm)
		copied, err := io.CopyBuffer(w, io.NewSectionReader(body, off, n), buf)
		if err != nil {
			return bodySum{}, fmt.Errorf("read body at %d: %w", off, err)
		}
		if copied != n {
			return bodySum{}, fmt.Errorf("body has %d bytes, size says %d: %w", off+copied, size, evidencestore.ErrChecksum)
		}
		sum.parts = append(sum.parts, partSum{sha256: ps.Sum(nil), md5: pm.Sum(nil), size: n})
		off += n
		if off >= size {
			break
		}
	}
	var probe [1]byte
	n, err := body.ReadAt(probe[:], size)
	if n > 0 {
		return bodySum{}, fmt.Errorf("body is longer than size %d: %w", size, evidencestore.ErrChecksum)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return bodySum{}, fmt.Errorf("read body end: %w", err)
	}
	sum.whole = whole.Sum(nil)
	sum.wholeMD5 = wholeMD5.Sum(nil)
	return sum, nil
}

// listResult is the ListObjectsV2 response.
type listResult struct {
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
	Contents              []struct {
		Key          string    `xml:"Key"`
		Size         int64     `xml:"Size"`
		ETag         string    `xml:"ETag"`
		LastModified time.Time `xml:"LastModified"`
	} `xml:"Contents"`
}

// infoFromHeader reads an object's description from a GET or HEAD
// response. A malformed x-amz-meta-sha256 is dropped rather than trusted.
func infoFromHeader(key string, resp *http.Response) evidencestore.ObjectInfo {
	info := evidencestore.ObjectInfo{
		Key:  key,
		Size: resp.ContentLength,
		ETag: strings.Trim(resp.Header.Get("Etag"), `"`),
	}
	if info.Size < 0 {
		if n, err := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64); err == nil {
			info.Size = n
		}
	}
	if t, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		info.LastModified = t.UTC()
	}
	if s := strings.ToLower(resp.Header.Get(metaSHA256)); isHexSHA256(s) {
		info.SHA256 = s
	}
	return info
}

// parseEndpoint accepts http or https with a host and optional port, and no
// path, query or user info. Default ports are dropped so the signed host
// matches the Host header net/http sends.
func parseEndpoint(raw string) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("s3blob: invalid endpoint: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("s3blob: endpoint %q must be http:// or https://", u.Redacted())
	}
	if u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", "", fmt.Errorf("s3blob: endpoint %q must be scheme://host[:port] only", u.Redacted())
	}
	host := strings.ToLower(u.Host)
	if p := u.Port(); (u.Scheme == "https" && p == "443") || (u.Scheme == "http" && p == "80") {
		host = strings.ToLower(u.Hostname())
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	}
	return u.Scheme, host, nil
}

// normalizePrefix turns "a/b" or "a/b/" into "a/b/" and "" into "".
func normalizePrefix(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	p = strings.TrimSuffix(p, "/")
	if err := evidencestore.ValidKey(p); err != nil {
		return "", fmt.Errorf("s3blob: invalid prefix: %w", err)
	}
	return p + "/", nil
}

// applyDefaults fills zero sizes and attempts and checks the S3 limits:
// parts of 5 MiB to 5 GiB, single PUTs of at most 5 GiB.
func applyDefaults(cfg *Config) error {
	if cfg.PartSize == 0 {
		cfg.PartSize = DefaultPartSize
	}
	if cfg.MultipartThreshold == 0 {
		cfg.MultipartThreshold = DefaultMultipartThreshold
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = DefaultMaxAttempts
	}
	if cfg.Addressing == "" {
		cfg.Addressing = AddressingAuto
	}
	if cfg.PartSize < MinPartSize || cfg.PartSize > maxSinglePut {
		return fmt.Errorf("s3blob: part size %d outside %d..%d", cfg.PartSize, MinPartSize, int64(maxSinglePut))
	}
	if cfg.MultipartThreshold < 1 || cfg.MultipartThreshold > maxSinglePut {
		return fmt.Errorf("s3blob: multipart threshold %d outside 1..%d", cfg.MultipartThreshold, int64(maxSinglePut))
	}
	if cfg.MaxAttempts < 1 {
		return fmt.Errorf("s3blob: max attempts %d below 1", cfg.MaxAttempts)
	}
	return nil
}

// useVirtual picks the addressing style; see Config.
func useVirtual(mode, host, bucket string) (bool, error) {
	switch mode {
	case AddressingPath:
		return false, nil
	case AddressingVirtual:
		// Dotted bucket names break wildcard TLS certificates but work over
		// plain HTTP or with a matching certificate, so they are allowed.
		return true, nil
	case AddressingAuto:
		h := host
		if hh, _, err := net.SplitHostPort(host); err == nil {
			h = hh
		}
		return strings.HasSuffix(h, ".amazonaws.com") && !strings.Contains(bucket, "."), nil
	}
	return false, fmt.Errorf("s3blob: addressing %q is not auto, path or virtual", mode)
}

// validBucket applies the S3 bucket naming rules: 3 to 63 characters of
// lowercase letters, digits, dots and hyphens, starting and ending with a
// letter or digit, no "..", and not an IPv4 address.
func validBucket(b string) error {
	if len(b) < 3 || len(b) > 63 {
		return fmt.Errorf("s3blob: bucket %q must be 3 to 63 characters", b)
	}
	for i := 0; i < len(b); i++ {
		c := b[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if !alnum && c != '.' && c != '-' {
			return fmt.Errorf("s3blob: bucket %q has invalid character %q", b, c)
		}
		if (i == 0 || i == len(b)-1) && !alnum {
			return fmt.Errorf("s3blob: bucket %q must start and end with a letter or digit", b)
		}
	}
	if strings.Contains(b, "..") || net.ParseIP(b) != nil {
		return fmt.Errorf("s3blob: invalid bucket name %q", b)
	}
	return nil
}

func isHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func contentType(ct string) string {
	if ct == "" {
		return "application/octet-stream"
	}
	return ct
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return ""
	}
	return b.String()
}

// decodeXML decodes a buffered response body and closes it.
func decodeXML(resp *http.Response, v any) error {
	defer resp.Body.Close()
	if err := xml.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// closeBody drains and closes a response body so the connection is reused.
func closeBody(resp *http.Response) error {
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)); err != nil {
		resp.Body.Close()
		return fmt.Errorf("drain response: %w", err)
	}
	return resp.Body.Close()
}
