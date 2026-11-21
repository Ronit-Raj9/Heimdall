package webhooks

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func TestCreateListSecretHidden(t *testing.T) {
	st := Open(t.TempDir())
	h, err := st.Create(Input{URL: ptr("https://example.com/hook?token=abc")}, "u1", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h.Secret, "whsec_") || h.Events[0] != EventExperimentFinished || !h.Enabled {
		t.Fatalf("bad hook %+v", h)
	}
	v := h.View()
	if strings.Contains(v.URL, "abc") || v.SecretLast4 != h.Secret[len(h.Secret)-4:] {
		t.Fatalf("view leaks: %+v", v)
	}
	fi, err := os.Stat(st.hooksPath())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("hooks file mode %v %v", fi.Mode(), err)
	}
	if _, err := st.Update(h.ID, Input{Enabled: ptr(false)}, false); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Get(h.ID)
	if got.Enabled {
		t.Fatal("update not saved")
	}
	if err := st.Delete(h.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(h.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestValidateURL(t *testing.T) {
	for _, u := range []string{"http://example.com", "https://127.0.0.1/x", "https://localhost/x", "https://10.0.0.1/", "ftp://x", "notaurl", "https://169.254.169.254/"} {
		if ValidateURL(u, false) == nil {
			t.Errorf("hosted accepted %s", u)
		}
	}
	if err := ValidateURL("http://127.0.0.1:9000/x", true); err != nil {
		t.Errorf("local refused localhost: %v", err)
	}
	if _, err := Open(t.TempDir()).Create(Input{URL: ptr("https://e.com"), Events: &[]string{"nope"}}, "", false); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown event: %v", err)
	}
}

func TestDispatchSignedAndRetried(t *testing.T) {
	var calls atomic.Int32
	var sigOK atomic.Bool
	var secret atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		ts, _ := strconv.ParseInt(r.Header.Get("X-Heimdall-Timestamp"), 10, 64)
		if r.Header.Get("X-Heimdall-Signature") == Sign(secret.Load().(string), ts, body) && r.Header.Get("X-Heimdall-Event") == EventExperimentFinished {
			sigOK.Store(true)
		}
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	st := Open(t.TempDir())
	h, err := st.Create(Input{URL: ptr(srv.URL)}, "u1", true)
	if err != nil {
		t.Fatal(err)
	}
	secret.Store(h.Secret)
	// A disabled hook and one on another event get nothing.
	if _, err := st.Create(Input{URL: ptr(srv.URL), Enabled: ptr(false)}, "u1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Create(Input{URL: ptr(srv.URL), Events: &[]string{EventJobFailed}}, "u1", true); err != nil {
		t.Fatal(err)
	}
	ds := Dispatch(context.Background(), st, Sender{AllowPrivate: true, Backoff: time.Millisecond}, NewEvent(EventExperimentFinished, "org1", map[string]string{"exp_id": "e1"}))
	if len(ds) != 1 || !ds[0].OK || ds[0].Attempts != 2 || !sigOK.Load() {
		t.Fatalf("deliveries %+v sig %v", ds, sigOK.Load())
	}
	rec, _ := st.Deliveries(10)
	if len(rec) != 1 || rec[0].HookID != h.ID {
		t.Fatalf("recorded %+v", rec)
	}
}

func TestHostedSenderRefusesLoopback(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	d := Sender{Attempts: 1}.Send(context.Background(), Hook{ID: "x", URL: srv.URL, Secret: "s"}, NewEvent(EventPing, "", nil))
	if d.OK || d.Error == "" {
		t.Fatalf("hosted sender reached loopback: %+v", d)
	}
}
