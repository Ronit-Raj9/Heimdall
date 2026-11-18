package monitorcert

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// keywordMonitor scores a request by the share of its configured terms
// (case-insensitive substrings and regular expressions) that occur anywhere
// in the agent's actions or final message, and blocks when at least BlockAt
// terms match. The instruction is not scanned: it is the task, not the
// agent.
type keywordMonitor struct {
	keywords []string // lower-cased
	patterns []*regexp.Regexp
	blockAt  int
}

func newKeywordMonitor(c *schemas.MonitorConfig) (*keywordMonitor, error) {
	m := &keywordMonitor{blockAt: c.BlockAt}
	if m.blockAt < 1 {
		m.blockAt = 1
	}
	for _, k := range c.Keywords {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
			m.keywords = append(m.keywords, k)
		}
	}
	for _, p := range c.Patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("keyword monitor: pattern %q: %w", p, err)
		}
		m.patterns = append(m.patterns, re)
	}
	if len(m.keywords)+len(m.patterns) == 0 {
		return nil, fmt.Errorf("keyword monitor: no keywords or patterns")
	}
	return m, nil
}

// Score implements Monitor.
func (m *keywordMonitor) Score(ctx context.Context, reqs []schemas.MonitorRequest) ([]schemas.MonitorResponse, error) {
	out := make([]schemas.MonitorResponse, 0, len(reqs))
	for i := range reqs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, m.scoreOne(&reqs[i]))
	}
	return out, nil
}

func (m *keywordMonitor) scoreOne(req *schemas.MonitorRequest) schemas.MonitorResponse {
	text := strings.Join(agentTexts(req), "\n")
	lower := strings.ToLower(text)
	var matched []string
	for _, k := range m.keywords {
		if strings.Contains(lower, k) {
			matched = append(matched, k)
		}
	}
	for _, re := range m.patterns {
		if re.MatchString(text) {
			matched = append(matched, re.String())
		}
	}
	sort.Strings(matched)
	score := float64(len(matched)) / float64(len(m.keywords)+len(m.patterns))
	resp := schemas.MonitorResponse{Decision: schemas.MonitorAllow, Score: &score}
	if len(matched) >= m.blockAt {
		resp.Decision = schemas.MonitorBlock
	}
	if len(matched) > 0 {
		resp.Reason = "matched: " + strings.Join(matched, ", ")
	}
	return resp
}

// agentTexts is every agent-controlled string of a request: action names
// and contents and the final message.
func agentTexts(req *schemas.MonitorRequest) []string {
	var out []string
	add := func(a *schemas.MonitorAction) {
		out = append(out, a.Name, a.Content)
	}
	for i := range req.Actions {
		add(&req.Actions[i])
	}
	if req.Action != nil {
		add(req.Action)
	}
	if req.FinalMessage != "" {
		out = append(out, req.FinalMessage)
	}
	return out
}
