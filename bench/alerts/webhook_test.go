package alerts

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// receiver is a local fake webhook receiver that answers with a scripted
// sequence of statuses and records every request.
type receiver struct {
	mu       sync.Mutex
	statuses []int
	bodies   [][]byte
	headers  []http.Header
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = append(r.bodies, body)
	r.headers = append(r.headers, req.Header.Clone())
	status := http.StatusOK
	if n := len(r.bodies) - 1; n < len(r.statuses) {
		status = r.statuses[n]
	}
	w.WriteHeader(status)
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

// testSender returns a sender whose environment is env, whose backoff waits
// are recorded instead of slept, and whose log goes to a temp file.
func testSender(t *testing.T, env map[string]string) (*Sender, string, *[]time.Duration) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "deliveries.jsonl")
	var waits []time.Duration
	s := NewSender(logPath,
		WithGetenv(func(k string) string { return env[k] }),
		WithSleep(func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }),
		WithClock(func() time.Time { return time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC) }),
	)
	return s, logPath, &waits
}

func readLog(t *testing.T, p string) []Delivery {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()
	var out []Delivery
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var d Delivery
		if err := json.Unmarshal(sc.Bytes(), &d); err != nil {
			t.Fatalf("log line %q: %v", sc.Text(), err)
		}
		out = append(out, d)
	}
	return out
}

func samplePayload() Payload {
	return Payload{Event: EventVerdictRegressed, Schedule: "nightly", Experiment: "e_x", Decision: "regressed",
		Action: "block", Link: LinkPath("e_x"), Summary: Summary{Runs: 8, RunErrors: 1, CostUSD: 0.5}}
}

func TestSendSignsBody(t *testing.T) {
	rcv := &receiver{}
	srv := httptest.NewServer(rcv)
	defer srv.Close()
	s, logPath, _ := testSender(t, map[string]string{"HOOK_URL": srv.URL + "/hook", "HOOK_SECRET": "s3cret"})
	cfg := Config{URLEnv: "HOOK_URL", SecretEnv: "HOOK_SECRET"}
	if err := s.Send(context.Background(), []Config{cfg}, samplePayload()); err != nil {
		t.Fatalf("send: %v", err)
	}
	if rcv.count() != 1 {
		t.Fatalf("requests = %d, want 1", rcv.count())
	}
	body, h := rcv.bodies[0], rcv.headers[0]
	if !Verify([]byte("s3cret"), body, h.Get(HeaderSignature)) {
		t.Fatalf("signature %q does not verify", h.Get(HeaderSignature))
	}
	if Verify([]byte("other"), body, h.Get(HeaderSignature)) {
		t.Fatal("signature verifies under the wrong secret")
	}
	if h.Get(HeaderEvent) != EventVerdictRegressed || h.Get(HeaderDelivery) != "1" {
		t.Fatalf("headers = %v", h)
	}
	var p Payload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	if p.Experiment != "e_x" || p.Link != "/verdict?exp=e_x" || p.Summary.Runs != 8 || p.SentAt.IsZero() {
		t.Fatalf("payload = %+v", p)
	}
	logged := readLog(t, logPath)
	if len(logged) != 1 || !logged[0].Delivered || logged[0].Status != 200 || logged[0].Target != "HOOK_URL" {
		t.Fatalf("log = %+v", logged)
	}
	raw, _ := os.ReadFile(logPath)
	if strings.Contains(string(raw), srv.URL) || strings.Contains(string(raw), "s3cret") {
		t.Fatalf("log leaks the URL or the secret: %s", raw)
	}
}

func TestSendRetries(t *testing.T) {
	tests := []struct {
		name      string
		statuses  []int
		wantErr   bool
		wantCalls int
		wantWaits []time.Duration
	}{
		{"retries then succeeds", []int{503, 429, 200}, false, 3, []time.Duration{time.Second, 2 * time.Second}},
		{"gives up after 3 retries", []int{500, 500, 500, 500, 500}, true, 4, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}},
		{"4xx is final", []int{400}, true, 1, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rcv := &receiver{statuses: tt.statuses}
			srv := httptest.NewServer(rcv)
			defer srv.Close()
			s, logPath, waits := testSender(t, map[string]string{"HOOK_URL": srv.URL})
			err := s.Send(context.Background(), []Config{{URLEnv: "HOOK_URL"}}, samplePayload())
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v", err, tt.wantErr)
			}
			if rcv.count() != tt.wantCalls {
				t.Fatalf("calls = %d, want %d", rcv.count(), tt.wantCalls)
			}
			if len(*waits) != len(tt.wantWaits) {
				t.Fatalf("waits = %v, want %v", *waits, tt.wantWaits)
			}
			for i := range tt.wantWaits {
				if (*waits)[i] != tt.wantWaits[i] {
					t.Fatalf("waits = %v, want %v", *waits, tt.wantWaits)
				}
			}
			if got := len(readLog(t, logPath)); got != tt.wantCalls {
				t.Fatalf("log lines = %d, want %d", got, tt.wantCalls)
			}
			if err != nil && strings.Contains(err.Error(), srv.URL) {
				t.Fatalf("error leaks the URL: %v", err)
			}
		})
	}
}

func TestSendSlackFormat(t *testing.T) {
	rcv := &receiver{}
	srv := httptest.NewServer(rcv)
	defer srv.Close()
	s, _, _ := testSender(t, map[string]string{"SLACK_URL": srv.URL})
	if err := s.Send(context.Background(), []Config{{URLEnv: "SLACK_URL", Format: FormatSlack}}, samplePayload()); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(rcv.bodies[0], &body); err != nil {
		t.Fatal(err)
	}
	text, ok := body["text"].(string)
	if len(body) != 1 || !ok {
		t.Fatalf("slack body = %s, want only text", rcv.bodies[0])
	}
	for _, want := range []string{"verdict_regressed", "nightly", "e_x", "verdict regressed", "/verdict?exp=e_x"} {
		if !strings.Contains(text, want) {
			t.Errorf("text %q lacks %q", text, want)
		}
	}
	if rcv.headers[0].Get(HeaderSignature) != "" {
		t.Fatal("unsigned config sent a signature")
	}
}

func TestSendRefusals(t *testing.T) {
	rcv := &receiver{}
	srv := httptest.NewServer(rcv)
	defer srv.Close()
	tests := []struct {
		name string
		env  map[string]string
		cfg  Config
		want string
	}{
		{"url env missing", map[string]string{}, Config{URLEnv: "HOOK_URL"}, "HOOK_URL is not set"},
		{"secret env missing", map[string]string{"HOOK_URL": srv.URL}, Config{URLEnv: "HOOK_URL", SecretEnv: "HOOK_SECRET"}, "not sending unsigned"},
		{"ftp scheme", map[string]string{"HOOK_URL": "ftp://127.0.0.1/x"}, Config{URLEnv: "HOOK_URL"}, "not http or https"},
		{"plain http off loopback", map[string]string{"HOOK_URL": "http://example.com/x"}, Config{URLEnv: "HOOK_URL"}, "only allowed to a loopback host"},
		{"user info", map[string]string{"HOOK_URL": "https://u:p@example.com/x"}, Config{URLEnv: "HOOK_URL"}, "user info"},
		{"literal url in url_env", map[string]string{}, Config{URLEnv: "https://hooks.example.com/x"}, "not an environment variable name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, logPath, _ := testSender(t, tt.env)
			err := s.Send(context.Background(), []Config{tt.cfg}, samplePayload())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			logged := readLog(t, logPath)
			if len(logged) != 1 || logged[0].Delivered || !strings.Contains(logged[0].Error, tt.want) {
				t.Fatalf("log = %+v", logged)
			}
		})
	}
	if rcv.count() != 0 {
		t.Fatalf("a refused alert reached the receiver %d times", rcv.count())
	}
}

func TestSendNetworkErrorScrubbed(t *testing.T) {
	srv := httptest.NewServer(&receiver{})
	addr := srv.URL
	srv.Close() // nothing listens any more
	s, logPath, _ := testSender(t, map[string]string{"HOOK_URL": addr + "/secret-path"})
	err := s.Send(context.Background(), []Config{{URLEnv: "HOOK_URL"}}, samplePayload())
	if err == nil {
		t.Fatal("want an error")
	}
	raw, _ := os.ReadFile(logPath)
	host := strings.TrimPrefix(addr, "http://")
	if strings.Contains(err.Error(), host) || strings.Contains(string(raw), host) || strings.Contains(string(raw), "secret-path") {
		t.Fatalf("URL leaked: err %v, log %s", err, raw)
	}
	if got := len(readLog(t, logPath)); got != MaxRetries+1 {
		t.Fatalf("log lines = %d, want %d", got, MaxRetries+1)
	}
}

func TestWantsAndValidate(t *testing.T) {
	c := Config{URLEnv: "U", Events: []string{EventPolicyBlock}}
	if !c.Wants(EventPolicyBlock) || c.Wants(EventRunErrors) {
		t.Fatal("Wants does not follow Events")
	}
	if !(Config{URLEnv: "U"}).Wants(EventRunErrors) {
		t.Fatal("empty Events must subscribe to every event")
	}
	bad := []Config{
		{},
		{URLEnv: "U", Events: []string{"nope"}},
		{URLEnv: "U", Format: "xml"},
		{URLEnv: "U", SecretEnv: "not a name"},
		{URLEnv: "U", TimeoutS: -1},
	}
	for i, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("config %d %+v validated", i, c)
		}
	}
}

func TestCheckURL(t *testing.T) {
	tests := []struct {
		raw string
		ok  bool
	}{
		{"https://hooks.example.com/x", true},
		{"http://127.0.0.1:9000/x", true},
		{"http://localhost/x", true},
		{"http://[::1]:80/x", true},
		{"http://10.0.0.1/x", false},
		{"file:///etc/passwd", false},
		{"https://", false},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if err := CheckURL(tt.raw); (err == nil) != tt.ok {
				t.Fatalf("CheckURL(%q) = %v, want ok %v", tt.raw, err, tt.ok)
			}
		})
	}
}

func TestSlackTextAndLink(t *testing.T) {
	if LinkPath("") != "" || LinkPath("e_a") != "/verdict?exp=e_a" {
		t.Fatal("LinkPath")
	}
	got := SlackText(Payload{Event: EventScheduleFailed, Schedule: "s", Message: "daily cap reached"})
	if !strings.Contains(got, "schedule_failed schedule s") || !strings.Contains(got, "daily cap reached") {
		t.Fatalf("text = %q", got)
	}
}
