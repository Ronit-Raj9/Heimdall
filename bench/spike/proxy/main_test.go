package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanFires(t *testing.T) {
	cases := []struct {
		name string
		p    plan
		call int
		want bool
	}{
		{"persistent before", plan{Mode: "persistent", FromCall: 2, Status: 503}, 1, false},
		{"persistent at", plan{Mode: "persistent", FromCall: 2, Status: 503}, 2, true},
		{"persistent after", plan{Mode: "persistent", FromCall: 2, Status: 503}, 5, true},
		{"once at", plan{Mode: "once", Call: 2, Status: 503}, 2, true},
		{"once after", plan{Mode: "once", Call: 2, Status: 503}, 3, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.p.validate(); err != nil {
				t.Fatalf("validate: %v", err)
			}
			if got := tc.p.fires(tc.call); got != tc.want {
				t.Fatalf("fires(%d) = %v, want %v", tc.call, got, tc.want)
			}
		})
	}
}

func TestDescribeHeadersRedacts(t *testing.T) {
	h := http.Header{}
	h.Set("X-Api-Key", "slot-a")
	h.Set("Anthropic-Version", "2023-06-01")
	h.Set("X-Goog-Api-Key", "secret")
	h.Set("Some-Other", "value")
	_, got := describeHeaders(h)
	if got["anthropic-version"] != "2023-06-01" {
		t.Fatalf("anthropic-version kept = %q", got["anthropic-version"])
	}
	for _, name := range []string{"x-api-key", "x-goog-api-key", "some-other"} {
		if strings.Contains(got[name], "slot-a") || strings.Contains(got[name], "secret") || got[name] == "value" {
			t.Fatalf("%s leaked: %q", name, got[name])
		}
	}
}

func TestScriptedAndInjected(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatalf("create ledger: %v", err)
	}
	s := &server{mode: "scripted", plans: map[string]plan{"slot-a": {Mode: "once", Call: 2, Status: 503}}, calls: map[string]int{}, ledger: f}
	srv := httptest.NewServer(http.HandlerFunc(s.serveUpstream))
	defer srv.Close()
	cases := []struct {
		name   string
		body   string
		status int
		stream bool
	}{
		{"call 1 json", `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`, 200, false},
		{"call 2 injected", `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`, 503, false},
		{"call 3 stream", `{"model":"claude-sonnet-4-6","stream":true,"messages":[{"role":"user","content":"hi"}]}`, 200, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/messages", strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("x-api-key", "slot-a")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			if tc.stream {
				sc := bufio.NewScanner(resp.Body)
				var events []string
				for sc.Scan() {
					if after, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
						events = append(events, after)
					}
				}
				if events[0] != "message_start" || events[len(events)-1] != "message_stop" {
					t.Fatalf("events = %v", events)
				}
				return
			}
			var body map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if tc.status == 200 && !strings.HasPrefix(body["id"].(string), "msg_") {
				t.Fatalf("id = %v", body["id"])
			}
			if tc.status == 503 && body["type"] != "error" {
				t.Fatalf("error envelope = %v", body)
			}
		})
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if lines := strings.Count(string(data), "\n"); lines != 3 {
		t.Fatalf("ledger lines = %d, want 3", lines)
	}
}
