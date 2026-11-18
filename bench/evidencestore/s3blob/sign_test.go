package s3blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/evidencestore"
)

// awsSigner is the example identity from the S3 SigV4 documentation
// ("Signature Calculations for the Authorization Header: Transferring
// Payload in a Single Chunk" and "Examples of Signature Calculations").
var awsSigner = signer{
	accessKey: "AKIAIOSFODNN7EXAMPLE",
	secretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	region:    "us-east-1",
}

var awsTime = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

func signatureOf(t *testing.T, auth string) (string, string) {
	t.Helper()
	_, signed, sig, ok := parseAuth(auth)
	if !ok {
		t.Fatalf("bad Authorization %q", auth)
	}
	return signed, sig
}

func TestSignAWSVectors(t *testing.T) {
	put := "Welcome to Amazon S3."
	putSum := sha256.Sum256([]byte(put))
	tests := []struct {
		name     string
		method   string
		url      string
		headers  map[string]string
		payload  string
		wantCReq string
		signed   string
		sig      string
	}{
		{
			name:    "GET object with Range",
			method:  "GET",
			url:     "https://examplebucket.s3.amazonaws.com/test.txt",
			headers: map[string]string{"Range": "bytes=0-9"},
			payload: emptySHA256,
			wantCReq: "GET\n/test.txt\n\nhost:examplebucket.s3.amazonaws.com\nrange:bytes=0-9\n" +
				"x-amz-content-sha256:" + emptySHA256 + "\nx-amz-date:20130524T000000Z\n\n" +
				"host;range;x-amz-content-sha256;x-amz-date\n" + emptySHA256,
			signed: "host;range;x-amz-content-sha256;x-amz-date",
			sig:    "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41",
		},
		{
			name:   "PUT object with Date and storage class",
			method: "PUT",
			url:    "https://examplebucket.s3.amazonaws.com/test$file.text",
			headers: map[string]string{
				"Date":                "Fri, 24 May 2013 00:00:00 GMT",
				"X-Amz-Storage-Class": "REDUCED_REDUNDANCY",
			},
			payload: hex.EncodeToString(putSum[:]),
			wantCReq: "PUT\n/test%24file.text\n\ndate:Fri, 24 May 2013 00:00:00 GMT\nhost:examplebucket.s3.amazonaws.com\n" +
				"x-amz-content-sha256:44ce7dd67c959e0d3524ffac1771dfbba87d2b6b4b4e99e42034a8b803f8b072\n" +
				"x-amz-date:20130524T000000Z\nx-amz-storage-class:REDUCED_REDUNDANCY\n\n" +
				"date;host;x-amz-content-sha256;x-amz-date;x-amz-storage-class\n" +
				"44ce7dd67c959e0d3524ffac1771dfbba87d2b6b4b4e99e42034a8b803f8b072",
			signed: "date;host;x-amz-content-sha256;x-amz-date;x-amz-storage-class",
			sig:    "98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd",
		},
		{
			name:    "GET bucket lifecycle",
			method:  "GET",
			url:     "https://examplebucket.s3.amazonaws.com/?lifecycle",
			payload: emptySHA256,
			wantCReq: "GET\n/\nlifecycle=\nhost:examplebucket.s3.amazonaws.com\n" +
				"x-amz-content-sha256:" + emptySHA256 + "\nx-amz-date:20130524T000000Z\n\n" +
				"host;x-amz-content-sha256;x-amz-date\n" + emptySHA256,
			signed: "host;x-amz-content-sha256;x-amz-date",
			sig:    "fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543",
		},
		{
			name:    "GET bucket listing",
			method:  "GET",
			url:     "https://examplebucket.s3.amazonaws.com/?max-keys=2&prefix=J",
			payload: emptySHA256,
			wantCReq: "GET\n/\nmax-keys=2&prefix=J\nhost:examplebucket.s3.amazonaws.com\n" +
				"x-amz-content-sha256:" + emptySHA256 + "\nx-amz-date:20130524T000000Z\n\n" +
				"host;x-amz-content-sha256;x-amz-date\n" + emptySHA256,
			signed: "host;x-amz-content-sha256;x-amz-date",
			sig:    "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, tt.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			awsSigner.sign(req, tt.payload, awsTime)
			creq, _ := canonicalRequest(req, tt.payload)
			if creq != tt.wantCReq {
				t.Errorf("canonical request:\n%s\nwant:\n%s", creq, tt.wantCReq)
			}
			signed, sig := signatureOf(t, req.Header.Get("Authorization"))
			if signed != tt.signed {
				t.Errorf("signed headers %q, want %q", signed, tt.signed)
			}
			if sig != tt.sig {
				t.Errorf("signature %s, want %s", sig, tt.sig)
			}
			wantCred := "Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request"
			if !strings.Contains(req.Header.Get("Authorization"), wantCred) {
				t.Errorf("authorization %q lacks %q", req.Header.Get("Authorization"), wantCred)
			}
		})
	}
}

func TestCanonicalURI(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "/"},
		{"/", "/"},
		{"/a b", "/a%20b"},
		{"/a+b", "/a%2Bb"},
		{"/a=b", "/a%3Db"},
		{"/a~b-c_d.e", "/a~b-c_d.e"},
		{"/test$file.text", "/test%24file.text"},
		{"/ü/日本", "/%C3%BC/%E6%97%A5%E6%9C%AC"},
		{"/bucket/runs/r1/x/y.json", "/bucket/runs/r1/x/y.json"},
		{"/a%20b", "/a%2520b"},
		{"/a:b@c,d;e&f'g(h)!*", "/a%3Ab%40c%2Cd%3Be%26f%27g%28h%29%21%2A"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := canonicalURI(tt.in); got != tt.want {
				t.Errorf("canonicalURI(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCanonicalQuery(t *testing.T) {
	tests := []struct {
		name string
		q    url.Values
		want string
	}{
		{"empty", url.Values{}, ""},
		{"empty value", url.Values{"uploads": {""}}, "uploads="},
		{"sorted", url.Values{"uploadId": {"x"}, "partNumber": {"2"}}, "partNumber=2&uploadId=x"},
		{"escapes slash space plus", url.Values{"prefix": {"a/b c+d"}}, "prefix=a%2Fb%20c%2Bd"},
		{"repeated sorted by value", url.Values{"k": {"b", "a"}}, "k=a&k=b"},
		{"unicode and tilde", url.Values{"continuation-token": {"ü~="}}, "continuation-token=%C3%BC~%3D"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canonicalQuery(tt.q); got != tt.want {
				t.Errorf("canonicalQuery = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCanonicalHeaders(t *testing.T) {
	req, err := http.NewRequest("PUT", "http://h.example:9000/b/k", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Amz-Meta-Note", "  a   b  c ")
	req.Header.Add("X-Amz-Meta-Multi", "one")
	req.Header.Add("X-Amz-Meta-Multi", "two")
	req.Header.Set("User-Agent", "skipped")
	req.Header.Set("Content-Type", "text/plain")
	got, signed := canonicalHeaders(req)
	want := "content-type:text/plain\nhost:h.example:9000\nx-amz-meta-multi:one,two\nx-amz-meta-note:a b c\n"
	if got != want {
		t.Errorf("headers:\n%q\nwant\n%q", got, want)
	}
	if signed != "content-type;host;x-amz-meta-multi;x-amz-meta-note" {
		t.Errorf("signed %q", signed)
	}
}

func TestSignSessionToken(t *testing.T) {
	s := awsSigner
	s.sessionToken = "FQoGZXIvYXdzEXAMPLETOKEN//+=="
	req, err := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.sign(req, emptySHA256, awsTime)
	if req.Header.Get("X-Amz-Security-Token") != s.sessionToken {
		t.Fatalf("token header %q", req.Header.Get("X-Amz-Security-Token"))
	}
	signed, sig := signatureOf(t, req.Header.Get("Authorization"))
	if signed != "host;x-amz-content-sha256;x-amz-date;x-amz-security-token" {
		t.Errorf("signed %q", signed)
	}
	if sig == "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41" {
		t.Error("token did not change the signature")
	}
	if strings.Contains(req.Header.Get("Authorization"), s.secretKey) {
		t.Error("authorization header contains the secret")
	}
}

// TestSignedURIMatchesWire checks that the path and query net/http writes
// are exactly the canonical ones that were signed, for path and
// virtual-host style.
func TestSignedURIMatchesWire(t *testing.T) {
	tests := []struct {
		name     string
		virtual  bool
		prefix   string
		key      string
		query    url.Values
		wantHost string
		wantURI  string
	}{
		{"path plain", false, "", "runs/r1/a.json", nil, "s3.example.test", "/bkt/runs/r1/a.json"},
		{"path special", false, "", "a b/c+d=e~f$g", nil, "s3.example.test", "/bkt/a%20b/c%2Bd%3De~f%24g"},
		{"path unicode prefix", false, "mirror/ü", "日.txt", nil, "s3.example.test", "/bkt/mirror/%C3%BC/%E6%97%A5.txt"},
		{"virtual special", true, "", "a b/c+d", nil, "bkt.s3.example.test", "/a%20b/c%2Bd"},
		{"virtual uploads", true, "p", "k", url.Values{"uploads": {""}}, "bkt.s3.example.test", "/p/k?uploads="},
		{"path part", false, "", "k", url.Values{"uploadId": {"a/b+c"}, "partNumber": {"3"}}, "s3.example.test", "/bkt/k?partNumber=3&uploadId=a%2Fb%2Bc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode := AddressingPath
			if tt.virtual {
				mode = AddressingVirtual
			}
			c, err := New(Config{Endpoint: "https://s3.example.test", Bucket: "bkt", Prefix: tt.prefix,
				AccessKey: "ak", SecretKey: "sk", Addressing: mode})
			if err != nil {
				t.Fatal(err)
			}
			req, err := c.newRequest(context.Background(), request{method: "GET", key: tt.key, query: tt.query, payload: emptySHA256})
			if err != nil {
				t.Fatal(err)
			}
			if req.URL.Host != tt.wantHost {
				t.Errorf("host %q, want %q", req.URL.Host, tt.wantHost)
			}
			if got := req.URL.RequestURI(); got != tt.wantURI {
				t.Errorf("wire URI %q, want %q", got, tt.wantURI)
			}
			creq, _ := canonicalRequest(req, emptySHA256)
			lines := strings.Split(creq, "\n")
			wire := lines[1]
			if lines[2] != "" {
				wire += "?" + lines[2]
			}
			if wire != tt.wantURI {
				t.Errorf("canonical URI+query %q, want %q", wire, tt.wantURI)
			}
		})
	}
}

// TestSignedRequestsAcceptedByFake sends keys with awkward characters
// through the fake server, which recomputes each signature from the wire.
func TestSignedRequestsAcceptedByFake(t *testing.T) {
	keys := []string{"plain.txt", "a b.txt", "x+y=z", "tilde~dollar$", "ünï/cødé/日本.bin", "deep/nested/path/to/file", "quote'paren(1)!*", "semi;colon:at@comma,"}
	for _, virtual := range []bool{false, true} {
		for _, token := range []string{"", "session/token+="} {
			name := "path"
			if virtual {
				name = "virtual"
			}
			if token != "" {
				name += " token"
			}
			t.Run(name, func(t *testing.T) {
				f := newFakeS3(t)
				f.token = token
				c := f.client(t, virtual, func(cfg *Config) { cfg.Prefix = "pre fix" })
				ctx := context.Background()
				for _, k := range keys {
					data := []byte("body of " + k)
					if err := c.Put(ctx, k, putInput(data)); err != nil {
						t.Fatalf("put %q: %v", k, err)
					}
					rc, info, err := c.Get(ctx, k)
					if err != nil {
						t.Fatalf("get %q: %v", k, err)
					}
					got, err := io.ReadAll(rc)
					if cerr := rc.Close(); err == nil {
						err = cerr
					}
					if err != nil || !bytes.Equal(got, data) || info.SHA256 != sha256hex(data) {
						t.Fatalf("get %q: %q %+v %v", k, got, info, err)
					}
				}
				list, err := c.List(ctx, "")
				if err != nil {
					t.Fatal(err)
				}
				if len(list) != len(keys) {
					t.Fatalf("list has %d objects, want %d", len(list), len(keys))
				}
				for _, k := range keys {
					if err := c.Delete(ctx, k); err != nil {
						t.Fatalf("delete %q: %v", k, err)
					}
				}
			})
		}
	}
}

func putInput(data []byte) evidencestore.PutInput {
	return evidencestore.PutInput{Body: bytes.NewReader(data), Size: int64(len(data)), SHA256: sha256hex(data)}
}

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
