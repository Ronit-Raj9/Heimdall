package traceimport

import (
	"strings"
	"unicode"
)

// MaxAgentBytes caps the agent name a trace gives (fix wave M7-12).
const MaxAgentBytes = 120

// agentName is the agent name a trace gives, as stored on an inbox item:
// one line of at most MaxAgentBytes, without control or format characters
// (such as a right-to-left override that would make the name read as
// another one). The name comes from outside: it is display text only.
func agentName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	return oneLine(s, MaxAgentBytes)
}
