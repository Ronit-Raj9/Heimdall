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
	snd := Sender{AllowPrivate: true}
	ds := Dispatch(context.Background(), st, snd, NewEvent(EventExperimentFinished, "org1", map[string]string{"exp_id": "e1"}))
	// The first attempt fails (502) and stays queued with a backoff.
	if len(ds) != 1 || ds[0].OK || ds[0].State != StateRetrying || ds[0].Attempts != 1 || ds[0].NextAttemptAt == nil || !sigOK.Load() {
		t.Fatalf("deliveries %+v sig %v", ds, sigOK.Load())
	}
	// Not due yet: nothing sent.
	if n, err := st.ProcessDue(context.Background(), snd, time.Now()); err != nil || n != 0 {
		t.Fatalf("early pass %d %v", n, err)
	}
	// A fresh Store over the same folder (a restart) still has it, and
	// delivers it once due, with the same delivery id and signature.
	again := Open(st.dir[:len(st.dir)-len("/.heimdall/webhooks")])
	if n, err := again.ProcessDue(context.Background(), snd, ds[0].NextAttemptAt.Add(time.Second)); err != nil || n != 1 {
		t.Fatalf("due pass %d %v", n, err)
	}
	rec, _ := again.Deliveries(10)
	if len(rec) != 1 || rec[0].HookID != h.ID || rec[0].ID != ds[0].ID || rec[0].State != StateDelivered || rec[0].Attempts != 2 {
		t.Fatalf("recorded %+v", rec)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls %d", calls.Load())
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

// sealer is a test Cipher that reverses and prefixes.
type sealer struct{}

func (sealer) Seal(id, p string) (string, error) { return "enc:" + id + ":" + reverse(p), nil }
func (sealer) Open(id, s string) (string, error) {
	rest, ok := strings.CutPrefix(s, "enc:"+id+":")
	if !ok {
		return "", errors.New("wrong id")
	}
	return reverse(rest), nil
}

func reverse(s string) string {
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

func TestSecretsEncryptedAtRest(t *testing.T) {
	root := t.TempDir()
	// A file from before encryption: plaintext secret.
	plain := Open(root)
	old, err := plain.Create(Input{URL: ptr("https://example.com/a")}, "u", false)
	if err != nil {
		t.Fatal(err)
	}
	st := OpenWith(root, sealer{})
	got, err := st.Get(old.ID)
	if err != nil || got.Secret != old.Secret {
		t.Fatalf("legacy read %+v %v", got, err)
	}
	h, err := st.Create(Input{URL: ptr("https://example.com/b")}, "u", false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(st.hooksPath())
	if strings.Contains(string(raw), old.Secret) || strings.Contains(string(raw), h.Secret) || !strings.Contains(string(raw), "secret_enc") {
		t.Fatalf("plaintext secret on disk: %s", raw)
	}
	if got, err := st.Get(h.ID); err != nil || got.Secret != h.Secret {
		t.Fatalf("round trip %+v %v", got, err)
	}
	if _, err := Open(root).List(); err == nil {
		t.Fatal("encrypted file read without a cipher")
	}
}

func TestOutboxFailsForGoodAndRetries(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusGone)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(int(status.Load())) }))
	defer srv.Close()
	st := Open(t.TempDir())
	if _, err := st.Create(Input{URL: ptr(srv.URL)}, "u", true); err != nil {
		t.Fatal(err)
	}
	snd := Sender{AllowPrivate: true}
	ds := Dispatch(context.Background(), st, snd, NewEvent(EventExperimentFinished, "o", nil))
	if len(ds) != 1 || ds[0].State != StateFailed || ds[0].StatusCode != http.StatusGone {
		t.Fatalf("410 not final: %+v", ds)
	}
	list, _ := st.Deliveries(10)
	if len(list) != 1 || list[0].State != StateFailed {
		t.Fatalf("log %+v", list)
	}
	if _, err := st.Retry("whd_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown retry %v", err)
	}
	status.Store(http.StatusOK)
	d, err := st.Retry(ds[0].ID)
	if err != nil || d.State != StateRetrying {
		t.Fatalf("retry %+v %v", d, err)
	}
	if n, err := st.ProcessDue(context.Background(), snd, time.Now().Add(time.Second)); err != nil || n != 1 {
		t.Fatalf("pass %d %v", n, err)
	}
	if _, err := st.Retry(ds[0].ID); !errors.Is(err, ErrAlreadyDelivered) {
		t.Fatalf("retry delivered %v", err)
	}
}

func TestBackoff(t *testing.T) {
	for n, want := range map[int]time.Duration{1: 30 * time.Second, 2: time.Minute, 3: 2 * time.Minute, 20: time.Hour} {
		if got := Backoff(n); got != want {
			t.Errorf("Backoff(%d) = %v, want %v", n, got, want)
		}
	}
}

// G3 B6: compose service names, internal suffixes and the extra special-use
// ranges are refused on hosted servers, at validation and at connect time.
func TestValidateURLRefusesInternalHosts(t *testing.T) {
	for _, u := range []string{"https://bifrost:8080/hook", "https://registry/v2", "https://x.internal/h",
		"https://100.64.0.9/h", "https://[fd00:ec2::254]/h", "https://169.254.169.254/latest"} {
		if err := ValidateURL(u, false); err == nil {
			t.Errorf("%s: accepted", u)
		}
	}
	if err := ValidateURL("https://hooks.example.com/heimdall", false); err != nil {
		t.Fatalf("public: %v", err)
	}
	if err := ValidateURL("http://bifrost:8080/hook", true); err != nil {
		t.Fatalf("local server: %v", err)
	}
	if err := GuardedDialer(time.Second).Control("tcp", "100.64.0.9:443", nil); err == nil {
		t.Fatal("dialer let carrier-grade NAT through")
	}
}
