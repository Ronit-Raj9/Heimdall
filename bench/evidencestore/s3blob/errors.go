package s3blob

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/evidencestore"
)

// Error is an error response from the S3 service, parsed from its XML body
// (or from the status alone for a HEAD, which has no body). errors.Is maps
// it onto the package-level sentinels: a missing key is
// evidencestore.ErrNotFound and a digest mismatch is
// evidencestore.ErrChecksum.
type Error struct {
	Status    int
	Code      string
	Message   string
	RequestID string

	serverTime time.Time // ServerTime from a RequestTimeTooSkewed body
}

// Error formats the status, code, message and request id. It never holds a
// credential: S3 error bodies carry the access key id in a separate element
// that is not kept.
func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "s3 status %d", e.Status)
	if e.Code != "" {
		b.WriteString(" " + e.Code)
	}
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	if e.RequestID != "" {
		b.WriteString(" (request id " + e.RequestID + ")")
	}
	return b.String()
}

// Is reports whether the S3 error means a missing object or a checksum
// mismatch. A HEAD 404 has no code, so any 404 except a missing bucket or
// multipart upload counts as a missing object.
func (e *Error) Is(target error) bool {
	switch target {
	case evidencestore.ErrNotFound:
		if e.Code == "NoSuchKey" {
			return true
		}
		return e.Status == http.StatusNotFound && e.Code != "NoSuchBucket" && e.Code != "NoSuchUpload"
	case evidencestore.ErrChecksum:
		switch e.Code {
		case "BadDigest", "InvalidDigest", "XAmzContentSHA256Mismatch", "XAmzContentChecksumMismatch":
			return true
		}
	}
	return false
}

// retryable reports whether the request may succeed when sent again:
// throttling, timeouts, server faults and every 5xx.
func (e *Error) retryable() bool {
	switch e.Code {
	case "SlowDown", "RequestTimeout", "InternalError", "ServiceUnavailable", "RequestTimeTooSkewed":
		return true
	}
	return e.Status >= 500 || e.Status == http.StatusTooManyRequests
}

// xmlError is the S3 <Error> document.
type xmlError struct {
	XMLName    xml.Name `xml:"Error"`
	Code       string   `xml:"Code"`
	Message    string   `xml:"Message"`
	RequestID  string   `xml:"RequestId"`
	ServerTime string   `xml:"ServerTime"`
}

// parseError builds an Error from a status, the response headers and the
// (possibly empty or non-XML) body. The request id falls back to the
// x-amz-request-id header.
func parseError(status int, h http.Header, body []byte) *Error {
	e := &Error{Status: status, RequestID: h.Get("X-Amz-Request-Id")}
	var x xmlError
	if len(bytes.TrimSpace(body)) > 0 && xml.Unmarshal(body, &x) == nil {
		e.Code = x.Code
		e.Message = x.Message
		if x.RequestID != "" {
			e.RequestID = x.RequestID
		}
		if t, err := time.Parse(time.RFC3339, x.ServerTime); err == nil {
			e.serverTime = t
		}
	}
	if e.Code == "" && status == http.StatusNotFound {
		e.Code = "NotFound"
	}
	return e
}

// isErrorDocument reports whether a 200 body is really an <Error>: S3 can
// answer CompleteMultipartUpload with 200 and then fail in the body.
func isErrorDocument(body []byte) bool {
	d := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := d.Token()
		if err != nil {
			return false
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local == "Error"
		}
	}
}
