package tracedraft

import (
	"github.com/maximhq/bifrost/bench/traceimport"
)

// ReplyTokens is the reply cap of one draft call (Draft sets MaxTokens to it).
const ReplyTokens = 1500

// EstimateTokens is the size of the first draft call for this session: the
// prompt's characters over four as input tokens, and the reply cap as output.
// The quote repair round, when needed, sends about the same again plus the
// first reply, so the worst case is a little over twice this.
func EstimateTokens(item string, s *traceimport.Session, kind string) (in, out int) {
	if s == nil {
		return 0, 0
	}
	sess, err := redactedCopy(s)
	if err != nil {
		sess = s
	}
	chars := 0
	for _, m := range prompt(item, sess, kind) {
		chars += len(m.Content)
	}
	return chars/4 + 1, ReplyTokens
}
