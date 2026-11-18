package s3blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/maximhq/bifrost/bench/evidencestore"
)

func TestNewValidation(t *testing.T) {
	base := Config{Bucket: "evidence", AccessKey: "ak", SecretKey: "sk"}
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"defaults ok", func(*Config) {}, ""},
		{"short bucket", func(c *Config) { c.Bucket = "ab" }, "3 to 63"},
		{"upper bucket", func(c *Config) { c.Bucket = "Evidence" }, "invalid character"},
		{"bucket edge hyphen", func(c *Config) { c.Bucket = "-evidence" }, "start and end"},
		{"bucket double dot", func(c *Config) { c.Bucket = "a..b" }, "invalid bucket"},
		{"bucket ip", func(c *Config) { c.Bucket = "192.168.1.1" }, "invalid bucket"},
		{"no secret", func(c *Config) { c.SecretKey = "" }, "secret key"},
		{"ftp endpoint", func(c *Config) { c.Endpoint = "ftp://h" }, "http:// or https://"},
		{"endpoint path", func(c *Config) { c.Endpoint = "https://h/x" }, "scheme://host"},
		{"endpoint userinfo", func(c *Config) { c.Endpoint = "https://u:p@h" }, "scheme://host"},
		{"small part", func(c *Config) { c.PartSize = 1 << 20 }, "part size"},
		{"huge threshold", func(c *Config) { c.MultipartThreshold = 6 << 30 }, "multipart threshold"},
		{"negative attempts", func(c *Config) { c.MaxAttempts = -1 }, "max attempts"},
		{"bad addressing", func(c *Config) { c.Addressing = "dns" }, "addressing"},
		{"bad prefix", func(c *Config) { c.Prefix = "../x" }, "invalid prefix"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			tt.mutate(&cfg)
			_, err := New(cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("New error %v, want %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "u:p@") {
				t.Errorf("error leaks endpoint password: %v", err)
			}
		})
	}
}

func TestNewDefaultsAndAddressing(t *testing.T) {
	tests := []struct {
		name        string
		cfg         Config
		wantHost    string
		wantVirtual bool
	}{
		{"aws default", Config{Bucket: "evidence"}, "s3.us-east-1.amazonaws.com", true},
		{"aws region", Config{Bucket: "evidence", Region: "eu-west-1"}, "s3.eu-west-1.amazonaws.com", true},
		{"aws dotted bucket", Config{Bucket: "my.evidence"}, "s3.us-east-1.amazonaws.com", false},
		{"aws forced path", Config{Bucket: "evidence", Addressing: "path"}, "s3.us-east-1.amazonaws.com", false},
		{"minio auto", Config{Bucket: "evidence", Endpoint: "http://127.0.0.1:9000"}, "127.0.0.1:9000", false},
		{"r2 auto", Config{Bucket: "evidence", Endpoint: "https://acct.r2.cloudflarestorage.com"}, "acct.r2.cloudflarestorage.com", false},
		{"custom virtual", Config{Bucket: "evidence", Endpoint: "https://s3.example.com:443", Addressing: "virtual"}, "s3.example.com", true},
		{"http default port", Config{Bucket: "evidence", Endpoint: "http://MinIO.local:80/"}, "minio.local", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cfg.AccessKey, tt.cfg.SecretKey = "ak", "sk"
			c, err := New(tt.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if c.host != tt.wantHost || c.virtual != tt.wantVirtual {
				t.Errorf("host %q virtual %v, want %q %v", c.host, c.virtual, tt.wantHost, tt.wantVirtual)
			}
			if c.cfg.PartSize != DefaultPartSize || c.cfg.MultipartThreshold != DefaultMultipartThreshold || c.cfg.MaxAttempts != DefaultMaxAttempts {
				t.Errorf("defaults not applied: %+v", c.cfg)
			}
		})
	}
}

func TestStringHasNoCredentials(t *testing.T) {
	c, err := New(Config{Endpoint: "https://minio.example:9000", Bucket: "evidence", Prefix: "mirror/",
		AccessKey: "AKIDSECRETACCESS", SecretKey: "verysecretkey", SessionToken: "tok3n"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := c.String(), "s3://evidence/mirror (minio.example:9000)"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	plain, err := New(Config{Bucket: "evidence", AccessKey: "ak", SecretKey: "sk"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := plain.String(), "s3://evidence (s3.us-east-1.amazonaws.com)"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	for _, secret := range []string{"AKIDSECRETACCESS", "verysecretkey", "tok3n"} {
		if strings.Contains(c.String(), secret) {
			t.Errorf("String() leaks %q", secret)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, tt := range []struct {
		name     string
		virtual  bool
		prefix   string
		checksum bool
	}{
		{"path", false, "", false},
		{"path prefix checksum", false, "mirror/v1", true},
		{"virtual", true, "", false},
		{"virtual prefix", true, "mirror", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeS3(t)
			c := f.client(t, tt.virtual, func(cfg *Config) { cfg.Prefix = tt.prefix; cfg.ChecksumSHA256 = tt.checksum })
			ctx := context.Background()
			data := []byte(`{"seal":"abc"}`)
			in := putInput(data)
			in.ContentType = "application/json"
			if err := c.Put(ctx, "exp/1/seal.json", in); err != nil {
				t.Fatal(err)
			}
			last := f.reqs()[len(f.reqs())-1]
			if last.header.Get("Content-Md5") == "" || last.header.Get("X-Amz-Meta-Sha256") != in.SHA256 ||
				last.header.Get("X-Amz-Content-Sha256") != in.SHA256 || last.header.Get("Content-Type") != "application/json" {
				t.Errorf("put headers %v", last.header)
			}
			if got := last.header.Get("X-Amz-Checksum-Sha256") != ""; got != tt.checksum {
				t.Errorf("x-amz-checksum-sha256 sent %v, want %v", got, tt.checksum)
			}
			if err := c.Put(ctx, "exp/1/empty", putInput(nil)); err != nil {
				t.Fatalf("put empty: %v", err)
			}
			rc, info, err := c.Get(ctx, "exp/1/seal.json")
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(rc)
			if err != nil {
				t.Fatal(err)
			}
			if err := rc.Close(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, data) {
				t.Errorf("get %q", got)
			}
			if info.Key != "exp/1/seal.json" || info.Size != int64(len(data)) || info.SHA256 != in.SHA256 ||
				info.ETag != md5hex(data) || info.LastModified.IsZero() {
				t.Errorf("get info %+v", info)
			}
			hinfo, err := c.Head(ctx, "exp/1/empty")
			if err != nil || hinfo.Size != 0 || hinfo.SHA256 != sha256hex(nil) {
				t.Errorf("head empty %+v %v", hinfo, err)
			}
			list, err := c.List(ctx, "exp/")
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != 2 || list[0].Key != "exp/1/empty" || list[1].Key != "exp/1/seal.json" || list[1].Size != int64(len(data)) {
				t.Errorf("list %+v", list)
			}
			if err := c.Delete(ctx, "exp/1/seal.json"); err != nil {
				t.Fatal(err)
			}
			if err := c.Delete(ctx, "exp/1/seal.json"); err != nil {
				t.Fatalf("delete missing: %v", err)
			}
			if _, err := c.Head(ctx, "exp/1/seal.json"); !errors.Is(err, evidencestore.ErrNotFound) {
				t.Errorf("head deleted: %v, want ErrNotFound", err)
			}
			if _, _, err := c.Get(ctx, "exp/1/seal.json"); !errors.Is(err, evidencestore.ErrNotFound) {
				t.Errorf("get deleted: %v, want ErrNotFound", err)
			}
			if tt.prefix != "" {
				f.mu.Lock()
				_, ok := f.objects[strings.TrimSuffix(tt.prefix, "/")+"/exp/1/empty"]
				f.mu.Unlock()
				if !ok {
					t.Error("object not stored under the prefix")
				}
			}
		})
	}
}

// countingReaderAt counts ReadAt calls and can serve different bytes after
// the first pass, as a file rewritten underneath the uploader would.
type countingReaderAt struct {
	data   []byte
	later  []byte
	passes atomic.Int64
}

func (r *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off == 0 {
		r.passes.Add(1)
	}
	src := r.data
	if r.passes.Load() > 1 && r.later != nil {
		src = r.later
	}
	return bytes.NewReader(src).ReadAt(p, off)
}

func TestPutRefusesBadInputWithoutSending(t *testing.T) {
	data := []byte("sealed bytes")
	tests := []struct {
		name string
		in   evidencestore.PutInput
		is   error
	}{
		{"wrong sha256", evidencestore.PutInput{Body: bytes.NewReader(data), Size: int64(len(data)), SHA256: sha256hex([]byte("other"))}, evidencestore.ErrChecksum},
		{"size too big", evidencestore.PutInput{Body: bytes.NewReader(data), Size: int64(len(data)) + 1, SHA256: sha256hex(data)}, evidencestore.ErrChecksum},
		{"size too small", evidencestore.PutInput{Body: bytes.NewReader(data), Size: int64(len(data)) - 1, SHA256: sha256hex(data[:len(data)-1])}, evidencestore.ErrChecksum},
		{"multipart wrong sha256", evidencestore.PutInput{Body: bytes.NewReader(make([]byte, 6<<20)), Size: 6 << 20, SHA256: sha256hex(data)}, evidencestore.ErrChecksum},
		{"uppercase sha256", evidencestore.PutInput{Body: bytes.NewReader(data), Size: int64(len(data)), SHA256: strings.ToUpper(sha256hex(data))}, nil},
		{"nil body", evidencestore.PutInput{Size: 1, SHA256: sha256hex(data)}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeS3(t)
			c := f.client(t, false, func(cfg *Config) { cfg.MultipartThreshold = 5 << 20; cfg.PartSize = 5 << 20 })
			err := c.Put(context.Background(), "k", tt.in)
			if err == nil {
				t.Fatal("put succeeded")
			}
			if tt.is != nil && !errors.Is(err, tt.is) {
				t.Errorf("error %v, want %v", err, tt.is)
			}
			if n := len(f.reqs()); n != 0 {
				t.Errorf("%d requests sent, want 0", n)
			}
		})
	}
	t.Run("invalid key", func(t *testing.T) {
		f := newFakeS3(t)
		c := f.client(t, false, nil)
		for _, k := range []string{"", "/abs", "a/../b", "a//b", "a\\b"} {
			if err := c.Put(context.Background(), k, putInput(data)); err == nil {
				t.Errorf("key %q accepted", k)
			}
		}
		if n := len(f.reqs()); n != 0 {
			t.Errorf("%d requests sent, want 0", n)
		}
	})
}

// TestPutBodyChangedAfterHashing shows the server-side digest check: the
// bytes sent differ from the bytes hashed, the fake refuses them (signed
// sha256 or Content-MD5 mismatch) and the client reports ErrChecksum without retrying.
func TestPutBodyChangedAfterHashing(t *testing.T) {
	f := newFakeS3(t)
	c := f.client(t, false, nil)
	data := []byte("original evidence")
	r := &countingReaderAt{data: data, later: []byte("tampered evidence")}
	err := c.Put(context.Background(), "k", evidencestore.PutInput{Body: r, Size: int64(len(data)), SHA256: sha256hex(data)})
	if !errors.Is(err, evidencestore.ErrChecksum) {
		t.Fatalf("error %v, want ErrChecksum", err)
	}
	var se *Error
	if !errors.As(err, &se) || se.Status != 400 || (se.Code != "BadDigest" && se.Code != "XAmzContentSHA256Mismatch") {
		t.Errorf("error %#v, want 400 BadDigest or XAmzContentSHA256Mismatch", se)
	}
	if n := len(f.reqs()); n != 1 {
		t.Errorf("%d requests, want 1 (no retry)", n)
	}
}

func TestListPagination(t *testing.T) {
	f := newFakeS3(t)
	f.pageSize = 2
	c := f.client(t, false, func(cfg *Config) { cfg.Prefix = "m" })
	ctx := context.Background()
	keys := []string{"exp/e", "exp/a", "exp/c", "exp/b", "exp/d", "other/x"}
	for _, k := range keys {
		if err := c.Put(ctx, k, putInput([]byte(k))); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	f.objects["elsewhere/exp/z"] = fakeObject{data: []byte("z")}
	f.mu.Unlock()
	list, err := c.List(ctx, "exp/")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range list {
		got = append(got, o.Key)
		if o.SHA256 != "" || o.Size != int64(len(o.Key)) || o.ETag != md5hex([]byte(o.Key)) {
			t.Errorf("object %+v", o)
		}
	}
	if strings.Join(got, ",") != "exp/a,exp/b,exp/c,exp/d,exp/e" {
		t.Errorf("list %v", got)
	}
	if n := f.count(http.MethodGet, "continuation-token="); n != 2 {
		t.Errorf("%d continuation requests, want 2", n)
	}
	all, err := c.List(ctx, "")
	if err != nil || len(all) != len(keys) {
		t.Errorf("list all: %d %v", len(all), err)
	}
	if _, err := c.List(ctx, "/bad"); err == nil {
		t.Error("invalid prefix accepted")
	}
}

func TestListRejectsStuckToken(t *testing.T) {
	f := newFakeS3(t)
	f.hook = func(w http.ResponseWriter, r *http.Request, body []byte, n int) bool {
		_, _ = io.WriteString(w, `<ListBucketResult><IsTruncated>true</IsTruncated></ListBucketResult>`)
		return true
	}
	c := f.client(t, false, nil)
	if _, err := c.List(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "continuation token") {
		t.Fatalf("error %v", err)
	}
}

func TestHTTPS(t *testing.T) {
	for _, virtual := range []bool{false, true} {
		f := newFakeS3TLS(t)
		c := f.client(t, virtual, nil)
		if c.scheme != "https" {
			t.Fatalf("scheme %q", c.scheme)
		}
		data := []byte("over tls")
		if err := c.Put(context.Background(), "a/b", putInput(data)); err != nil {
			t.Fatalf("virtual %v: %v", virtual, err)
		}
		rc, info, err := c.Get(context.Background(), "a/b")
		if err != nil {
			t.Fatal(err)
		}
		if got := evidencestoreVerify(t, rc, info); !bytes.Equal(got, data) {
			t.Errorf("virtual %v: body %q", virtual, got)
		}
		want := map[bool]string{false: "example.com:", true: "evidence.example.com:"}[virtual]
		if host := f.reqs()[0].host; !strings.HasPrefix(host, want) {
			t.Errorf("virtual %v: Host %q, want %s<port>", virtual, host, want)
		}
	}
}

func TestMakeBucket(t *testing.T) {
	t.Run("idempotent us-east-1", func(t *testing.T) {
		f := newFakeS3(t)
		f.exists = false
		c := f.client(t, false, nil)
		for i := 0; i < 2; i++ {
			if err := c.MakeBucket(context.Background()); err != nil {
				t.Fatalf("make bucket %d: %v", i, err)
			}
		}
		if len(f.createBody) != 0 {
			t.Errorf("us-east-1 sent body %q", f.createBody)
		}
	})
	t.Run("location constraint", func(t *testing.T) {
		f := newFakeS3(t)
		f.region = "eu-west-1"
		f.exists = false
		c := f.client(t, true, nil)
		if err := c.MakeBucket(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(f.createBody), "<LocationConstraint>eu-west-1</LocationConstraint>") {
			t.Errorf("body %q", f.createBody)
		}
		if f.reqs()[0].path != "/" {
			t.Errorf("virtual-host bucket path %q", f.reqs()[0].path)
		}
	})
	t.Run("already exists elsewhere but reachable", func(t *testing.T) {
		f := newFakeS3(t)
		f.hook = func(w http.ResponseWriter, r *http.Request, body []byte, n int) bool {
			if r.Method == http.MethodPut {
				fakeErr(w, 409, "BucketAlreadyExists", "taken")
				return true
			}
			return false
		}
		c := f.client(t, false, nil)
		if err := c.MakeBucket(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("access denied", func(t *testing.T) {
		f := newFakeS3(t)
		f.hook = func(w http.ResponseWriter, r *http.Request, body []byte, n int) bool {
			fakeErr(w, 403, "AccessDenied", "no")
			return true
		}
		c := f.client(t, false, nil)
		err := c.MakeBucket(context.Background())
		var se *Error
		if !errors.As(err, &se) || se.Code != "AccessDenied" {
			t.Fatalf("error %v", err)
		}
	})
}

func TestErrorsCarryNoCredentials(t *testing.T) {
	f := newFakeS3(t)
	f.secret = "server-side-secret"
	c := f.client(t, false, func(cfg *Config) { cfg.SecretKey = "client-secret-value"; cfg.SessionToken = "client-token-value" })
	_, _, err := c.Get(context.Background(), "k")
	if err == nil {
		t.Fatal("get with wrong secret succeeded")
	}
	if !strings.Contains(err.Error(), "SignatureDoesNotMatch") {
		t.Errorf("error %v, want SignatureDoesNotMatch", err)
	}
	for _, s := range []string{"client-secret-value", "client-token-value", f.access} {
		if strings.Contains(err.Error(), s) {
			t.Errorf("error %q contains %q", err, s)
		}
	}
}
