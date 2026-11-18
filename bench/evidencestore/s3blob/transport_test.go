package s3blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/evidencestore"
)

// sleepRecorder replaces the backoff wait and records each requested delay.
type sleepRecorder struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (s *sleepRecorder) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.delays = append(s.delays, d)
	s.mu.Unlock()
	return ctx.Err()
}

func TestRetryResendsIdenticalBody(t *testing.T) {
	f := newFakeS3(t)
	f.hook = func(w http.ResponseWriter, r *http.Request, body []byte, n int) bool {
		if n <= 2 {
			fakeErr(w, 503, "SlowDown", "Please reduce your request rate.")
			return true
		}
		return false
	}
	rec := &sleepRecorder{}
	c := f.client(t, false, func(cfg *Config) { cfg.sleep = rec.sleep })
	data := bytes.Repeat([]byte("evidence "), 1000)
	if err := c.Put(context.Background(), "k", putInput(data)); err != nil {
		t.Fatal(err)
	}
	reqs := f.reqs()
	if len(reqs) != 3 {
		t.Fatalf("%d requests, want 3", len(reqs))
	}
	for i, r := range reqs {
		if !bytes.Equal(r.body, data) {
			t.Errorf("attempt %d body differs (%d bytes)", i+1, len(r.body))
		}
		if r.header.Get("Content-Md5") != reqs[0].header.Get("Content-Md5") {
			t.Errorf("attempt %d Content-MD5 differs", i+1)
		}
	}
	if len(rec.delays) != 2 {
		t.Fatalf("delays %v, want 2", rec.delays)
	}
	if d := rec.delays[0]; d < 100*time.Millisecond || d > 200*time.Millisecond {
		t.Errorf("first delay %v outside [100ms,200ms]", d)
	}
	if d := rec.delays[1]; d < 200*time.Millisecond || d > 400*time.Millisecond {
		t.Errorf("second delay %v outside [200ms,400ms]", d)
	}
}

func TestRetryPolicy(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		code     string
		attempts int
		wantReqs int
	}{
		{"400 no retry", 400, "InvalidArgument", 5, 1},
		{"403 no retry", 403, "AccessDenied", 5, 1},
		{"500 until max", 500, "InternalError", 4, 4},
		{"429 retried", 429, "", 3, 3},
		{"request timeout 400 retried", 400, "RequestTimeout", 2, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeS3(t)
			f.hook = func(w http.ResponseWriter, r *http.Request, body []byte, n int) bool {
				if tt.code == "" {
					w.WriteHeader(tt.status)
				} else {
					fakeErr(w, tt.status, tt.code, "injected")
				}
				return true
			}
			c := f.client(t, false, func(cfg *Config) { cfg.MaxAttempts = tt.attempts })
			err := c.Put(context.Background(), "k", putInput([]byte("x")))
			var se *Error
			if !errors.As(err, &se) || se.Status != tt.status {
				t.Fatalf("error %v, want status %d", err, tt.status)
			}
			if n := len(f.reqs()); n != tt.wantReqs {
				t.Errorf("%d requests, want %d", n, tt.wantReqs)
			}
		})
	}
}

func TestRetryNetworkError(t *testing.T) {
	f := newFakeS3(t)
	f.hook = func(w http.ResponseWriter, r *http.Request, body []byte, n int) bool {
		if n == 1 {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("no hijacker")
				return true
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Error(err)
				return true
			}
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
			return true
		}
		return false
	}
	c := f.client(t, false, nil)
	data := []byte("after a dropped connection")
	if err := c.Put(context.Background(), "k", putInput(data)); err != nil {
		t.Fatal(err)
	}
	if n := len(f.reqs()); n != 2 {
		t.Errorf("%d requests, want 2", n)
	}
}

func TestCancelDuringBackoff(t *testing.T) {
	f := newFakeS3(t)
	f.hook = func(w http.ResponseWriter, r *http.Request, body []byte, n int) bool {
		fakeErr(w, 503, "ServiceUnavailable", "down")
		return true
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waited := make(chan time.Duration, 1)
	c := f.client(t, false, func(cfg *Config) {
		cfg.sleep = func(sctx context.Context, d time.Duration) error {
			waited <- d
			cancel()
			return sleepCtx(sctx, time.Hour)
		}
	})
	start := time.Now()
	_, err := c.Head(ctx, "k")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v, want context.Canceled", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("backoff wait ignored cancellation")
	}
	if n := len(f.reqs()); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
	if d := <-waited; d <= 0 {
		t.Errorf("backoff %v", d)
	}
}

func TestSleepCtx(t *testing.T) {
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v", err)
	}
}

func TestClockSkewRetry(t *testing.T) {
	f := newFakeS3(t)
	rec := &sleepRecorder{}
	c := f.client(t, false, func(cfg *Config) {
		cfg.now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
		cfg.sleep = rec.sleep
	})
	data := []byte("skewed clock")
	if err := c.Put(context.Background(), "k", putInput(data)); err != nil {
		t.Fatal(err)
	}
	if n := len(f.reqs()); n != 2 {
		t.Errorf("%d requests, want 2", n)
	}
	if len(rec.delays) != 0 {
		t.Errorf("skew retry waited %v", rec.delays)
	}
	if skew := time.Duration(c.skew.Load()); skew < 119*time.Minute || skew > 121*time.Minute {
		t.Errorf("skew %v, want about 2h", skew)
	}
	if _, err := c.Head(context.Background(), "k"); err != nil {
		t.Fatalf("head after skew correction: %v", err)
	}
	if n := len(f.reqs()); n != 3 {
		t.Errorf("%d requests, want 3 (no second skew retry)", n)
	}
}

func TestBackoffBounds(t *testing.T) {
	c := &Client{jitterSrc: randInt63n}
	for attempt := 1; attempt <= 40; attempt++ {
		d := c.backoff(attempt)
		ceil := min(backoffCap, backoffBase<<min(attempt-1, 15))
		if d < ceil/2 || d > ceil {
			t.Errorf("attempt %d: %v outside [%v,%v]", attempt, d, ceil/2, ceil)
		}
	}
	c.jitterSrc = func(n int64) int64 { return n - 1 }
	if d := c.backoff(30); d != backoffCap {
		t.Errorf("capped backoff %v, want %v", d, backoffCap)
	}
}

func TestGetStreamsWithoutBuffering(t *testing.T) {
	f := newFakeS3(t)
	c := f.client(t, false, nil)
	data := bytes.Repeat([]byte{7}, 3<<20)
	if err := c.Put(context.Background(), "big", putInput(data)); err != nil {
		t.Fatal(err)
	}
	rc, info, err := c.Get(context.Background(), "big")
	if err != nil {
		t.Fatal(err)
	}
	vr := evidencestoreVerify(t, rc, info)
	if !bytes.Equal(vr, data) {
		t.Error("body mismatch")
	}
	reqs := f.reqs()
	if ae := reqs[len(reqs)-1].header.Get("Accept-Encoding"); ae != "identity" {
		t.Errorf("Accept-Encoding %q, want identity (no transparent gzip)", ae)
	}
}

// evidencestoreVerify reads and closes rc and checks the body against the
// size and sha256 the info claims.
func evidencestoreVerify(t *testing.T, rc io.ReadCloser, info evidencestore.ObjectInfo) []byte {
	t.Helper()
	var b bytes.Buffer
	if _, err := b.ReadFrom(rc); err != nil {
		t.Fatal(err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	if sha256hex(b.Bytes()) != info.SHA256 || int64(b.Len()) != info.Size {
		t.Fatalf("info %+v does not match body", info)
	}
	return b.Bytes()
}
