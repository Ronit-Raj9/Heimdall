//go:build integration

package s3blob

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/evidencestore"
	"github.com/maximhq/bifrost/bench/evidencestore/internal/miniotest"
)

// minioClient returns a client for a fresh bucket on srv, created with
// MakeBucket (twice, to check it is idempotent).
func minioClient(t *testing.T, srv miniotest.Server, bucket string, virtual bool, mutate func(*Config)) *Client {
	t.Helper()
	cfg := Config{
		Endpoint:   srv.Endpoint,
		Region:     srv.Region,
		Bucket:     bucket,
		AccessKey:  srv.AccessKey,
		SecretKey:  srv.SecretKey,
		Addressing: AddressingPath,
	}
	if virtual {
		cfg.Endpoint = srv.VirtualEndpoint()
		cfg.Addressing = AddressingVirtual
		cfg.HTTPClient = srv.HTTPClient()
	}
	if mutate != nil {
		mutate(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		if err := c.MakeBucket(ctx); err != nil {
			t.Fatalf("make bucket (call %d): %v", i+1, err)
		}
	}
	return c
}

func TestMinIORoundTrip(t *testing.T) {
	srv := miniotest.Start(t)
	for _, tt := range []struct {
		name    string
		bucket  string
		virtual bool
		prefix  string
	}{
		{"path", "hd-path", false, ""},
		{"path prefix", "hd-path-prefix", false, "mirror/v1"},
		{"virtual", "hd-virtual", true, "mirror"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := minioClient(t, srv, tt.bucket, tt.virtual, func(cfg *Config) { cfg.Prefix = tt.prefix })
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			objects := map[string][]byte{
				"exp/1/seals.log":             []byte("seal line\n"),
				"exp/1/runs/a b+c=d~e$f.json": []byte(`{"x":1}`),
				"exp/1/ünïcode/日本.txt":        []byte("unicode key"),
				"exp/1/empty":                 nil,
				"exp/2/other.bin":             randomBytes(4096, 9),
			}
			for k, v := range objects {
				if err := c.Put(ctx, k, putInput(v)); err != nil {
					t.Fatalf("put %q: %v", k, err)
				}
			}
			for k, v := range objects {
				rc, info, err := c.Get(ctx, k)
				if err != nil {
					t.Fatalf("get %q: %v", k, err)
				}
				got := evidencestoreVerify(t, rc, info)
				if !bytes.Equal(got, v) || info.Key != k || info.ETag == "" || info.LastModified.IsZero() {
					t.Errorf("get %q: info %+v", k, info)
				}
				hinfo, err := c.Head(ctx, k)
				if err != nil || hinfo.SHA256 != sha256hex(v) || hinfo.Size != int64(len(v)) {
					t.Errorf("head %q: %+v %v", k, hinfo, err)
				}
			}
			list, err := c.List(ctx, "exp/1/")
			if err != nil {
				t.Fatal(err)
			}
			var keys []string
			for _, o := range list {
				keys = append(keys, o.Key)
				if o.Size != int64(len(objects[o.Key])) {
					t.Errorf("list %q size %d", o.Key, o.Size)
				}
			}
			want := "exp/1/empty,exp/1/runs/a b+c=d~e$f.json,exp/1/seals.log,exp/1/ünïcode/日本.txt"
			if strings.Join(keys, ",") != want {
				t.Errorf("list %v, want %s", keys, want)
			}
			if _, err := c.Head(ctx, "exp/missing"); !errors.Is(err, evidencestore.ErrNotFound) {
				t.Errorf("head missing: %v", err)
			}
			if _, _, err := c.Get(ctx, "exp/missing"); !errors.Is(err, evidencestore.ErrNotFound) {
				t.Errorf("get missing: %v", err)
			}
			bad := putInput([]byte("x"))
			bad.SHA256 = sha256hex([]byte("y"))
			if err := c.Put(ctx, "exp/bad", bad); !errors.Is(err, evidencestore.ErrChecksum) {
				t.Errorf("put wrong sha256: %v", err)
			}
			for k := range objects {
				if err := c.Delete(ctx, k); err != nil {
					t.Fatalf("delete %q: %v", k, err)
				}
			}
			if err := c.Delete(ctx, "exp/1/seals.log"); err != nil {
				t.Errorf("delete missing: %v", err)
			}
			if list, err := c.List(ctx, ""); err != nil || len(list) != 0 {
				t.Errorf("list after delete: %v %v", list, err)
			}
		})
	}
}

// TestMinIOServerChecksDigests shows MinIO itself refusing bytes that do
// not match what was signed: the body changes between hashing and sending.
func TestMinIOServerChecksDigests(t *testing.T) {
	srv := miniotest.Start(t)
	c := minioClient(t, srv, "hd-digest", false, nil)
	data := []byte("original evidence")
	r := &countingReaderAt{data: data, later: []byte("tampered evidence")}
	err := c.Put(context.Background(), "k", evidencestore.PutInput{Body: r, Size: int64(len(data)), SHA256: sha256hex(data)})
	if !errors.Is(err, evidencestore.ErrChecksum) {
		t.Fatalf("error %v, want ErrChecksum", err)
	}
	if _, err := c.Head(context.Background(), "k"); !errors.Is(err, evidencestore.ErrNotFound) {
		t.Errorf("refused object exists: %v", err)
	}
}

func TestMinIOMultipart(t *testing.T) {
	srv := miniotest.Start(t)
	data := randomBytes(11<<20+77, 11)
	for _, tt := range []struct {
		name     string
		virtual  bool
		checksum bool
	}{
		{"path no checksum", false, false},
		{"path checksum", false, true},
		{"virtual checksum", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bucket := "hd-mp-" + strings.ReplaceAll(tt.name, " ", "-")
			c := minioClient(t, srv, bucket, tt.virtual, func(cfg *Config) {
				cfg.PartSize = 5 << 20
				cfg.MultipartThreshold = 5 << 20
				cfg.ChecksumSHA256 = tt.checksum
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			in := putInput(data)
			in.ContentType = "application/x-tar"
			if err := c.Put(ctx, "big/archive.tar", in); err != nil {
				t.Fatal(err)
			}
			info, err := c.Head(ctx, "big/archive.tar")
			if err != nil {
				t.Fatal(err)
			}
			if info.Size != int64(len(data)) || info.SHA256 != in.SHA256 || !strings.HasSuffix(info.ETag, "-3") {
				t.Errorf("head %+v", info)
			}
			rc, info, err := c.Get(ctx, "big/archive.tar")
			if err != nil {
				t.Fatal(err)
			}
			if got := evidencestoreVerify(t, rc, info); !bytes.Equal(got, data) {
				t.Error("multipart round trip mismatch")
			}
			if err := c.Delete(ctx, "big/archive.tar"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestMinIOWrongCredentials checks the error is an *Error with no secret
// in its text.
func TestMinIOWrongCredentials(t *testing.T) {
	srv := miniotest.Start(t)
	minioClient(t, srv, "hd-creds", false, nil)
	c, err := New(Config{Endpoint: srv.Endpoint, Bucket: "hd-creds", AccessKey: srv.AccessKey, SecretKey: "wrong-secret-value", Addressing: AddressingPath})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = c.Get(context.Background(), "k")
	var se *Error
	if !errors.As(err, &se) || se.Status != 403 {
		t.Fatalf("error %v, want 403", err)
	}
	if strings.Contains(err.Error(), "wrong-secret-value") {
		t.Errorf("error leaks secret: %v", err)
	}
}
