package miniotest

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParsePort(t *testing.T) {
	tests := []struct {
		out     string
		want    string
		wantErr bool
	}{
		{"127.0.0.1:49153", "49153", false},
		{"127.0.0.1:32768\n[::1]:32768", "32768", false},
		{"", "", true},
		{"garbage", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.out, func(t *testing.T) {
			got, err := parsePort(tt.out)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("parsePort(%q) = %q, %v", tt.out, got, err)
			}
		})
	}
}

func TestNewHostDialer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.WriteString(w, r.Host); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	s := Server{Domain: "minio.example.test", Port: port}
	client := s.HTTPClient()
	for _, host := range []string{"minio.example.test", "bucket.minio.example.test", "Deep.Bucket.MINIO.example.test"} {
		resp, err := client.Get("http://" + host + ":" + port + "/x")
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		body, err := io.ReadAll(resp.Body)
		if cerr := resp.Body.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != host+":"+port {
			t.Errorf("server saw Host %q, want %q", body, host+":"+port)
		}
	}
	dial := NewHostDialer("minio.example.test", srv.Listener.Addr().String())
	if _, err := dial(context.Background(), "tcp", "notminio.example.test.invalid:1"); err == nil {
		t.Error("unrelated host was mapped")
	}
	if got := s.VirtualEndpoint(); got != "http://minio.example.test:"+port {
		t.Errorf("VirtualEndpoint %q", got)
	}
}
