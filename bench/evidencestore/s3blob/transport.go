package s3blob

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"time"
)

const (
	backoffBase = 200 * time.Millisecond
	backoffCap  = 5 * time.Second
	// maxBuffered bounds the XML responses (listings, multipart replies)
	// read into memory.
	maxBuffered = 32 << 20
	// maxErrorBody bounds an error response read for its XML.
	maxErrorBody = 1 << 20
)

// request is one S3 call, rebuilt and re-signed on every attempt so a retry
// re-reads the body from its ReaderAt.
type request struct {
	method     string
	key        string // object key below the client prefix; unused with bucketOnly
	bucketOnly bool
	query      url.Values
	header     http.Header
	body       io.ReaderAt
	offset     int64
	size       int64
	payload    string // lowercase hex sha256 of the body
	stream     bool   // hand the success body to the caller unread
	buffer     bool   // read the success body inside the retry loop
	errorIn200 bool   // a 200 body may be an <Error> document
}

// bytesRequest wraps an in-memory body, with its sha256 as the signed
// payload and its MD5 as Content-MD5.
func bytesRequest(method, key string, bucketOnly bool, q url.Values, h http.Header, body []byte) request {
	if h == nil {
		h = http.Header{}
	}
	sum := sha256.Sum256(body)
	if len(body) > 0 {
		m := md5.Sum(body)
		h.Set("Content-Md5", base64.StdEncoding.EncodeToString(m[:]))
	}
	return request{
		method:     method,
		key:        key,
		bucketOnly: bucketOnly,
		query:      q,
		header:     h,
		body:       bytes.NewReader(body),
		size:       int64(len(body)),
		payload:    hex.EncodeToString(sum[:]),
		buffer:     true,
	}
}

// do sends r, retrying network errors, 5xx, 429 and the S3 throttling and
// timeout codes with exponential backoff and jitter, up to MaxAttempts.
// RequestTimeTooSkewed corrects the signing clock from the server's Date
// and retries at once. Any other 4xx returns immediately. Context
// cancellation stops the backoff wait.
func (c *Client) do(ctx context.Context, r request) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		resp, retry, err := c.attempt(ctx, r)
		if err == nil {
			return resp, nil
		}
		if !retry || attempt >= c.cfg.MaxAttempts {
			if attempt > 1 {
				return nil, fmt.Errorf("after %d attempts: %w", attempt, err)
			}
			return nil, err
		}
		var se *Error
		if errors.As(err, &se) && se.Code == "RequestTimeTooSkewed" {
			continue
		}
		if serr := c.sleep(ctx, c.backoff(attempt)); serr != nil {
			return nil, fmt.Errorf("retry wait after attempt %d (%v): %w", attempt, err, serr)
		}
	}
}

// attempt sends r once. The bool says whether a failure is worth retrying.
func (c *Client) attempt(ctx context.Context, r request) (*http.Response, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	req, err := c.newRequest(ctx, r)
	if err != nil {
		return nil, false, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, false, cerr
		}
		var certErr *tls.CertificateVerificationError
		return nil, !errors.As(err, &certErr), err
	}
	if resp.StatusCode >= 300 {
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		cerr := resp.Body.Close()
		if rerr == nil {
			rerr = cerr
		}
		se := parseError(resp.StatusCode, resp.Header, body)
		if se.Code == "RequestTimeTooSkewed" {
			c.adjustSkew(resp.Header, se)
		}
		if rerr != nil && ctx.Err() == nil {
			return nil, true, se
		}
		return nil, se.retryable(), se
	}
	if r.stream || !(r.buffer || r.errorIn200) {
		return resp, false, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBuffered+1))
	cerr := resp.Body.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, false, cerr
		}
		return nil, true, fmt.Errorf("read response: %w", err)
	}
	if len(body) > maxBuffered {
		return nil, false, fmt.Errorf("response larger than %d bytes", maxBuffered)
	}
	if r.errorIn200 && isErrorDocument(body) {
		se := parseError(resp.StatusCode, resp.Header, body)
		return nil, se.retryable(), se
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, false, nil
}

// newRequest builds and signs the HTTP request for r. Path style puts the
// bucket in the path (/bucket/key); virtual-host style puts it in the host
// (bucket.host/key).
func (c *Client) newRequest(ctx context.Context, r request) (*http.Request, error) {
	host := c.host
	p := "/" + c.cfg.Bucket
	if c.virtual {
		host = c.cfg.Bucket + "." + c.host
		p = ""
	}
	if !r.bucketOnly {
		p += "/" + c.prefix + r.key
	}
	if p == "" {
		p = "/"
	}
	req, err := http.NewRequestWithContext(ctx, r.method, c.scheme+"://"+host+"/", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.URL.Path = p
	req.URL.RawQuery = canonicalQuery(r.query)
	for k, vs := range r.header {
		req.Header[k] = append([]string(nil), vs...)
	}
	// An explicit identity encoding stops net/http from asking for gzip and
	// transparently decompressing, which would hand Get bytes other than
	// the stored ones and drop Content-Length. It is not signed.
	req.Header.Set("Accept-Encoding", "identity")
	if r.size > 0 {
		body, off, n := r.body, r.offset, r.size
		req.ContentLength = n
		req.Body = io.NopCloser(io.NewSectionReader(body, off, n))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(io.NewSectionReader(body, off, n)), nil
		}
	} else if r.method == http.MethodPut || r.method == http.MethodPost {
		req.Body = http.NoBody
		req.GetBody = func() (io.ReadCloser, error) { return http.NoBody, nil }
	}
	c.signer.sign(req, r.payload, c.now().Add(time.Duration(c.skew.Load())))
	return req, nil
}

// adjustSkew records how far the server clock is from ours, from the Date
// header or the error body's ServerTime, so the next signature uses the
// server's time.
func (c *Client) adjustSkew(h http.Header, se *Error) {
	server, err := http.ParseTime(h.Get("Date"))
	if err != nil {
		server = se.serverTime
	}
	if server.IsZero() {
		return
	}
	c.skew.Store(int64(server.Sub(c.now())))
}

// backoff is the wait before attempt+1: base*2^(attempt-1), capped, with
// equal jitter (half fixed, half random) so concurrent clients spread out.
func (c *Client) backoff(attempt int) time.Duration {
	d := backoffCap
	if attempt < 16 {
		d = min(backoffCap, backoffBase<<(attempt-1))
	}
	half := int64(d / 2)
	return time.Duration(half + c.jitterSrc(half+1))
}

// sleepCtx waits d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func randInt63n(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return rand.Int64N(n)
}
