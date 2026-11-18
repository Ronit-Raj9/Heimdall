package s3blob

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/maximhq/bifrost/bench/evidencestore"
)

func randomBytes(n int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

func TestMultipartUpload(t *testing.T) {
	for _, checksum := range []bool{false, true} {
		name := "plain"
		if checksum {
			name = "checksum sha256"
		}
		t.Run(name, func(t *testing.T) {
			f := newFakeS3(t)
			c := f.client(t, false, func(cfg *Config) {
				cfg.PartSize = 5 << 20
				cfg.MultipartThreshold = 8 << 20
				cfg.ChecksumSHA256 = checksum
			})
			data := randomBytes(11<<20+123, 1)
			in := putInput(data)
			if err := c.Put(context.Background(), "big/object.bin", in); err != nil {
				t.Fatal(err)
			}
			var create, complete *fakeRequest
			var partSizes []int
			var partQueries []string
			reqs := f.reqs()
			for i := range reqs {
				r := &reqs[i]
				switch {
				case r.method == http.MethodPost && r.query == "uploads=":
					create = r
				case r.method == http.MethodPut && strings.Contains(r.query, "partNumber="):
					partSizes = append(partSizes, len(r.body))
					partQueries = append(partQueries, r.query)
					if r.header.Get("Content-Md5") == "" || r.header.Get("X-Amz-Content-Sha256") != sha256hex(r.body) {
						t.Errorf("part %s missing digests", r.query)
					}
					if got := r.header.Get("X-Amz-Checksum-Sha256") != ""; got != checksum {
						t.Errorf("part checksum header sent %v", got)
					}
				case r.method == http.MethodPost && strings.HasPrefix(r.query, "uploadId="):
					complete = r
				}
			}
			if create == nil || complete == nil {
				t.Fatal("missing create or complete request")
			}
			if create.header.Get("X-Amz-Meta-Sha256") != in.SHA256 {
				t.Errorf("create meta sha256 %q", create.header.Get("X-Amz-Meta-Sha256"))
			}
			if got := create.header.Get("X-Amz-Checksum-Algorithm"); (got == "SHA256") != checksum {
				t.Errorf("checksum algorithm %q", got)
			}
			if want := []int{5 << 20, 5 << 20, 1<<20 + 123}; !equalInts(partSizes, want) {
				t.Errorf("part sizes %v, want %v", partSizes, want)
			}
			if partQueries[0] != "partNumber=1&uploadId=upload-1" {
				t.Errorf("part query %q", partQueries[0])
			}
			var body completeUpload
			if err := xml.Unmarshal(complete.body, &body); err != nil {
				t.Fatal(err)
			}
			for i, p := range body.Parts {
				if p.PartNumber != i+1 || !strings.HasPrefix(p.ETag, `"`) {
					t.Errorf("complete part %d: %+v", i, p)
				}
				if (p.ChecksumSHA256 != "") != checksum {
					t.Errorf("complete part %d checksum %q", i, p.ChecksumSHA256)
				}
			}
			if len(body.Parts) != 3 {
				t.Errorf("complete has %d parts", len(body.Parts))
			}
			if last := reqs[len(reqs)-1]; last.method != http.MethodHead {
				t.Errorf("last request %s, want HEAD verification", last.method)
			}
			rc, info, err := c.Get(context.Background(), "big/object.bin")
			if err != nil {
				t.Fatal(err)
			}
			got := evidencestoreVerify(t, rc, info)
			if !bytes.Equal(got, data) || !strings.HasSuffix(info.ETag, "-3") {
				t.Errorf("round trip mismatch, etag %q", info.ETag)
			}
		})
	}
}

func TestMultipartAbortsOnPartFailure(t *testing.T) {
	f := newFakeS3(t)
	f.hook = func(w http.ResponseWriter, r *http.Request, body []byte, n int) bool {
		if r.Method == http.MethodPut && strings.Contains(r.URL.RawQuery, "partNumber=2") {
			fakeErr(w, 400, "InvalidArgument", "injected part failure")
			return true
		}
		return false
	}
	c := f.client(t, false, func(cfg *Config) { cfg.PartSize = 5 << 20; cfg.MultipartThreshold = 5 << 20 })
	data := randomBytes(12<<20, 2)
	err := c.Put(context.Background(), "k", putInput(data))
	var se *Error
	if !errors.As(err, &se) || se.Code != "InvalidArgument" || !strings.Contains(err.Error(), "part 2") {
		t.Fatalf("error %v", err)
	}
	if n := f.count(http.MethodDelete, "uploadId=upload-1"); n != 1 {
		t.Errorf("%d abort requests, want 1", n)
	}
	if n := f.count(http.MethodPut, "partNumber=3"); n != 0 {
		t.Errorf("part 3 sent after part 2 failed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.uploads) != 0 || len(f.objects) != 0 {
		t.Errorf("leftover uploads %d objects %d", len(f.uploads), len(f.objects))
	}
}

func TestMultipartAbortsWhenCancelled(t *testing.T) {
	f := newFakeS3(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.hook = func(w http.ResponseWriter, r *http.Request, body []byte, n int) bool {
		if strings.Contains(r.URL.RawQuery, "partNumber=2") {
			cancel()
			fakeErr(w, 503, "SlowDown", "slow")
			return true
		}
		return false
	}
	c := f.client(t, false, func(cfg *Config) {
		cfg.PartSize = 5 << 20
		cfg.MultipartThreshold = 5 << 20
		cfg.sleep = sleepCtx
	})
	err := c.Put(ctx, "k", putInput(randomBytes(11<<20, 3)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v, want context.Canceled", err)
	}
	if n := f.count(http.MethodDelete, "uploadId="); n != 1 {
		t.Errorf("%d abort requests, want 1", n)
	}
}

func TestMultipartCompleteErrorIn200(t *testing.T) {
	f := newFakeS3(t)
	var completes atomic.Int32
	f.hook = func(w http.ResponseWriter, r *http.Request, body []byte, n int) bool {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.RawQuery, "uploadId=") {
			if completes.Add(1) == 1 {
				w.WriteHeader(200)
				_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>`+"\n\n"+
					`<Error><Code>InternalError</Code><Message>We encountered an internal error. Please try again.</Message></Error>`)
				return true
			}
		}
		return false
	}
	c := f.client(t, false, func(cfg *Config) { cfg.PartSize = 5 << 20; cfg.MultipartThreshold = 5 << 20 })
	data := randomBytes(6<<20, 4)
	if err := c.Put(context.Background(), "k", putInput(data)); err != nil {
		t.Fatal(err)
	}
	if n := completes.Load(); n != 2 {
		t.Errorf("%d complete requests, want 2", n)
	}
}

func TestMultipartCompleteRetriedAfterSuccess(t *testing.T) {
	f := newFakeS3(t)
	f.hook = func(w http.ResponseWriter, r *http.Request, body []byte, n int) bool {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.RawQuery, "uploadId=") && f.count(http.MethodPost, "uploadId=") == 1 {
			// Complete for real, then lose the response.
			f.process(&discardWriter{h: http.Header{}}, r, body)
			fakeErr(w, 500, "InternalError", "lost response")
			return true
		}
		return false
	}
	c := f.client(t, false, func(cfg *Config) { cfg.PartSize = 5 << 20; cfg.MultipartThreshold = 5 << 20 })
	if err := c.Put(context.Background(), "k", putInput(randomBytes(6<<20, 5))); err != nil {
		t.Fatal(err)
	}
}

func TestPartSizeFor(t *testing.T) {
	c := &Client{cfg: Config{PartSize: 16 << 20}}
	tests := []struct {
		size, want int64
	}{
		{64 << 20, 16 << 20},
		{10000 * (16 << 20), 16 << 20},
		{10000*(16<<20) + 1, 17 << 20},
		{5 << 40, 525 << 20},
	}
	for _, tt := range tests {
		got := c.partSizeFor(tt.size)
		if got != tt.want {
			t.Errorf("partSizeFor(%d) = %d, want %d", tt.size, got, tt.want)
		}
		if (tt.size+got-1)/got > maxParts {
			t.Errorf("partSizeFor(%d) needs more than %d parts", tt.size, maxParts)
		}
	}
}

func TestHashBody(t *testing.T) {
	data := randomBytes(11, 6)
	sum, err := hashBody(bytes.NewReader(data), 11, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.parts) != 3 || sum.parts[2].size != 3 || sha256hexBytes(sum.whole) != sha256hex(data) {
		t.Errorf("sum %+v", sum)
	}
	if sha256hexBytes(sum.parts[1].sha256) != sha256hex(data[4:8]) {
		t.Error("part 2 hash wrong")
	}
	if _, err := hashBody(bytes.NewReader(data), 12, 0); !errors.Is(err, evidencestore.ErrChecksum) {
		t.Errorf("short body: %v", err)
	}
	if _, err := hashBody(bytes.NewReader(data), 10, 0); !errors.Is(err, evidencestore.ErrChecksum) {
		t.Errorf("long body: %v", err)
	}
	empty, err := hashBody(bytes.NewReader(nil), 0, 0)
	if err != nil || sha256hexBytes(empty.whole) != emptySHA256 {
		t.Errorf("empty: %v", err)
	}
}

func sha256hexBytes(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, hexdigits[c>>4], hexdigits[c&0x0f])
	}
	return string(out)
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type discardWriter struct{ h http.Header }

func (d *discardWriter) Header() http.Header         { return d.h }
func (d *discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardWriter) WriteHeader(int)             {}
