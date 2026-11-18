package toolproxy

import (
	"net/http"
	"strings"
)

// MemberHeader is the header the proxy sets on every request it forwards for
// a team member, with the member id its route token named. Any value the
// agent sent is dropped first, so a service (the team mailbox) can trust it
// as the proven sender (docs/multi-agent.md).
const MemberHeader = "X-Heimdall-Member"

// memberRoute strips a team member's route prefix /m/<token>/ from the
// request path and returns the member it names. Every request loses an
// agent-sent MemberHeader; a member's request gets the proxy's own. An
// unknown token is answered 404 and ok is false. A request without the
// prefix has no member.
func (s *Server) memberRoute(w http.ResponseWriter, r *http.Request) (string, bool) {
	r.Header.Del(MemberHeader)
	rest, ok := strings.CutPrefix(r.URL.Path, "/m/")
	if !ok {
		return "", true
	}
	token, tail, _ := strings.Cut(rest, "/")
	agent := s.members[token]
	if token == "" || agent == "" {
		http.Error(w, "not found: unknown member route", http.StatusNotFound)
		return "", false
	}
	r.URL.Path, r.URL.RawPath = "/"+tail, ""
	r.Header.Set(MemberHeader, agent)
	return agent, true
}
