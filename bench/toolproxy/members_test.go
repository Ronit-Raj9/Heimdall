package toolproxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMemberRoute(t *testing.T) {
	s := &Server{members: map[string]string{"tok1": "worker-a"}}
	for _, tc := range []struct {
		name, path, spoof string
		agent, rest       string
		ok                bool
		header            string
	}{
		{name: "member route", path: "/m/tok1/s/mailbox/send", agent: "worker-a", rest: "/s/mailbox/send", ok: true, header: "worker-a"},
		{name: "spoofed header on member route", path: "/m/tok1/s/mailbox/send", spoof: "lead", agent: "worker-a", rest: "/s/mailbox/send", ok: true, header: "worker-a"},
		{name: "plain route drops a spoofed header", path: "/s/mailbox/send", spoof: "lead", rest: "/s/mailbox/send", ok: true},
		{name: "unknown token", path: "/m/nope/s/mailbox/send", ok: false},
		{name: "empty token", path: "/m//s/x", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, tc.path, nil)
			if tc.spoof != "" {
				r.Header.Set(MemberHeader, tc.spoof)
			}
			w := httptest.NewRecorder()
			agent, ok := s.memberRoute(w, r)
			if ok != tc.ok || agent != tc.agent {
				t.Fatalf("agent %q ok %v", agent, ok)
			}
			if !ok {
				if w.Code != http.StatusNotFound {
					t.Errorf("status %d", w.Code)
				}
				return
			}
			if r.URL.Path != tc.rest || r.Header.Get(MemberHeader) != tc.header {
				t.Errorf("path %q header %q", r.URL.Path, r.Header.Get(MemberHeader))
			}
		})
	}
}
