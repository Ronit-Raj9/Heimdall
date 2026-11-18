package s3blob

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/evidencestore"
)

func TestParseErrorAndSentinels(t *testing.T) {
	xmlBody := func(code string) []byte {
		return []byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>` + code + `</Code><Message>msg ` + code +
			`</Message><RequestId>RID1</RequestId><AWSAccessKeyId>AKIAEXAMPLEKEYID</AWSAccessKeyId></Error>`)
	}
	tests := []struct {
		name      string
		status    int
		body      []byte
		wantCode  string
		notFound  bool
		checksum  bool
		retryable bool
	}{
		{"no such key", 404, xmlBody("NoSuchKey"), "NoSuchKey", true, false, false},
		{"head 404 no body", 404, nil, "NotFound", true, false, false},
		{"no such bucket", 404, xmlBody("NoSuchBucket"), "NoSuchBucket", false, false, false},
		{"no such upload", 404, xmlBody("NoSuchUpload"), "NoSuchUpload", false, false, false},
		{"bad digest", 400, xmlBody("BadDigest"), "BadDigest", false, true, false},
		{"invalid digest", 400, xmlBody("InvalidDigest"), "InvalidDigest", false, true, false},
		{"sha mismatch", 400, xmlBody("XAmzContentSHA256Mismatch"), "XAmzContentSHA256Mismatch", false, true, false},
		{"slow down", 503, xmlBody("SlowDown"), "SlowDown", false, false, true},
		{"internal 200", 200, xmlBody("InternalError"), "InternalError", false, false, true},
		{"request timeout", 400, xmlBody("RequestTimeout"), "RequestTimeout", false, false, true},
		{"too many", 429, nil, "", false, false, true},
		{"bad gateway html", 502, []byte("<html>bad gateway</html>"), "", false, false, true},
		{"access denied", 403, xmlBody("AccessDenied"), "AccessDenied", false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := parseError(tt.status, http.Header{"X-Amz-Request-Id": {"HDR"}}, tt.body)
			if e.Code != tt.wantCode {
				t.Errorf("code %q, want %q", e.Code, tt.wantCode)
			}
			wrapped := fmt.Errorf("s3 get k: %w", e)
			if got := errors.Is(wrapped, evidencestore.ErrNotFound); got != tt.notFound {
				t.Errorf("Is(ErrNotFound) = %v", got)
			}
			if got := errors.Is(wrapped, evidencestore.ErrChecksum); got != tt.checksum {
				t.Errorf("Is(ErrChecksum) = %v", got)
			}
			if got := e.retryable(); got != tt.retryable {
				t.Errorf("retryable = %v", got)
			}
			if strings.Contains(e.Error(), "AKIAEXAMPLEKEYID") {
				t.Errorf("error %q leaks the access key id", e.Error())
			}
			var as *Error
			if !errors.As(wrapped, &as) || as.Status != tt.status {
				t.Errorf("errors.As failed or status %d", as.Status)
			}
		})
	}
}

func TestParseErrorDetails(t *testing.T) {
	body := []byte(`<Error><Code>RequestTimeTooSkewed</Code><Message>skewed</Message><RequestId>R9</RequestId>` +
		`<RequestTime>20130524T000000Z</RequestTime><ServerTime>2026-11-18T01:02:03Z</ServerTime></Error>`)
	e := parseError(403, http.Header{"X-Amz-Request-Id": {"HDR"}}, body)
	if e.RequestID != "R9" || e.Message != "skewed" {
		t.Errorf("%+v", e)
	}
	if !e.serverTime.Equal(time.Date(2026, 11, 18, 1, 2, 3, 0, time.UTC)) {
		t.Errorf("server time %v", e.serverTime)
	}
	if got, want := e.Error(), "s3 status 403 RequestTimeTooSkewed: skewed (request id R9)"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if e := parseError(404, http.Header{"X-Amz-Request-Id": {"HDR"}}, nil); e.RequestID != "HDR" {
		t.Errorf("request id from header: %+v", e)
	}
}

func TestIsErrorDocument(t *testing.T) {
	tests := []struct {
		body string
		want bool
	}{
		{`<?xml version="1.0"?><Error><Code>InternalError</Code></Error>`, true},
		{"\n\n<Error><Code>SlowDown</Code></Error>", true},
		{`<CompleteMultipartUploadResult><ETag>"x"</ETag></CompleteMultipartUploadResult>`, false},
		{``, false},
		{`not xml`, false},
	}
	for _, tt := range tests {
		if got := isErrorDocument([]byte(tt.body)); got != tt.want {
			t.Errorf("isErrorDocument(%q) = %v", tt.body, got)
		}
	}
}
