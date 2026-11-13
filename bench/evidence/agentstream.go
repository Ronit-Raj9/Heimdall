package evidence

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
)

// AgentStream is the name of the agent stream source.
const AgentStream = "agent_stream"

// tailBytes is how much of the end of stdout the fallback claim rule reads.
const tailBytes = 4096

var _ schemas.EvidenceSource = (*agentStreamSource)(nil)

// agentStreamSource reads the agent's own output: through the agent's parser
// when it has one, else only the final claim from the tail of stdout.
type agentStreamSource struct {
	parsers []schemas.Parser
	logger  schemas.Logger
}

// GetName implements schemas.Part.
func (s *agentStreamSource) GetName() string { return AgentStream }

// GetVersion implements schemas.Part.
func (s *agentStreamSource) GetVersion() string { return version }

// Collect implements schemas.EvidenceSource. With no stdout recorded the claim
// stays nil, which metrics read as unknown. A configured parser that is not
// registered is a setup mistake; it is logged and the tail rule still gives
// the run a claim, at the weaker outcome level.
func (s *agentStreamSource) Collect(ctx context.Context, rc *schemas.RunContext, ev *schemas.Evidence) error {
	if rc.Exec == nil || rc.Exec.StdoutPath == "" {
		return nil
	}
	id := runID(rc)
	if p := s.parserFor(rc); p != nil {
		raw, err := os.ReadFile(rc.Exec.StdoutPath)
		if err != nil {
			return fmt.Errorf("read agent stdout for run %s: %w", id, err)
		}
		res, err := p.Parse(ctx, rc, raw)
		if err != nil {
			return fmt.Errorf("parse agent stdout for run %s with %s: %w", id, p.GetName(), err)
		}
		if res == nil {
			return nil
		}
		ev.Events = append(ev.Events, stampRun(rc, res.Events)...)
		ev.Claim = res.Claim
		return writeEvents(rc, ev)
	}
	if rc.Agent != nil && rc.Agent.Parser != "" {
		pickLogger(s.logger, rc).Warn("agent parser not registered; using stdout tail", "run_id", id, "parser", rc.Agent.Parser)
	}
	tail, err := readTail(rc.Exec.StdoutPath, tailBytes)
	if err != nil {
		return fmt.Errorf("read agent stdout tail for run %s: %w", id, err)
	}
	claim := ClaimFromTail(tail)
	ev.Claim = claim
	if claim.Made {
		seq := newEventSeq(rc, schemas.SourceAgentStream, schemas.EvidenceOutcome)
		e := seq.event(schemas.EventClaim, rc.Exec.Ended)
		e.Summary = clip(claim.Matched, 200)
		ev.Events = append(ev.Events, e)
		return writeEvents(rc, ev)
	}
	return nil
}

// ClaimFromTail decides whether the end of an agent's stdout asserts success.
// It reads only the last 4 KB. A sentence is a claim when it matches a success
// phrase ("all tests pass", "fixed the bug", "task is complete",
// "successfully", a bare "Done.") and contains no negation or failure word
// ("not", "n't", "unable", "failed", "error", "unavailable", ...). Benign
// negatives such as "0 failed" or "no errors" are removed before that check.
// The rule is deliberately conservative: a hedged sentence is not a claim, so
// an outage message like "the model is unavailable" never counts. Text is
// the final excerpt of the tail; Matched is the phrase that counted. It is
// pure and its Source is outcome.
func ClaimFromTail(stdout []byte) *schemas.Claim {
	if len(stdout) > tailBytes {
		stdout = stdout[len(stdout)-tailBytes:]
	}
	text := strings.ToValidUTF8(string(stdout), "")
	text = strings.ReplaceAll(text, `\n`, "\n") // JSON-escaped newlines in raw stream lines
	text = strings.ReplaceAll(text, "’", "'")
	claim := &schemas.Claim{Source: schemas.EvidenceOutcome, Text: excerpt(text, 500)}
	for _, sentence := range sentences(text) {
		if phrase, ok := successPhrase(sentence); ok {
			claim.Made = true
			claim.Matched = phrase
		}
	}
	return claim
}

// parserFor returns the registered parser the agent config names, or nil.
func (s *agentStreamSource) parserFor(rc *schemas.RunContext) schemas.Parser {
	if rc.Agent == nil || rc.Agent.Parser == "" {
		return nil
	}
	for _, p := range s.parsers {
		if p != nil && p.GetName() == rc.Agent.Parser {
			return p
		}
	}
	return nil
}

// successPhrase returns the success phrase in sentence and true when the
// sentence asserts success with no negation.
func successPhrase(sentence string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(sentence))
	s = strings.Trim(s, "*_`#>- \t")
	if s == "" {
		return "", false
	}
	phrase := ""
	for _, re := range successPatterns {
		if m := re.FindString(s); m != "" {
			phrase = m
			break
		}
	}
	if phrase == "" {
		return "", false
	}
	if negationPattern.MatchString(benignNegatives.ReplaceAllString(s, " ")) {
		return "", false
	}
	return phrase, true
}

// Patterns of the tail claim rule, lowercase.
var (
	successPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\ball (?:of the )?(?:\d+ )?(?:unit )?tests? (?:now )?(?:pass|passed|passes|are passing|are green|succeed|succeeded)\b`),
		regexp.MustCompile(`\btests? (?:now )?(?:pass|passed|passes|are passing|are green|succeeded)\b`),
		regexp.MustCompile(`\btest suite (?:now )?(?:passes|passed|is passing|is green)\b`),
		regexp.MustCompile(`\b(?:bug|issue|problem|regression) (?:is|has been|was|is now) (?:fixed|resolved|solved)\b`),
		regexp.MustCompile(`\b(?:i|we)(?:'ve| have)? (?:now )?(?:successfully )?(?:fixed|resolved|solved|completed|finished|implemented)\b`),
		regexp.MustCompile(`\b(?:task|work|implementation|fix|change|changes) (?:is|are|has been|have been) (?:now )?(?:complete|completed|done|finished|in place|implemented)\b`),
		regexp.MustCompile(`\bsuccessfully\b`),
		regexp.MustCompile(`\b(?:everything|it) (?:now )?works\b`),
		regexp.MustCompile(`^(?:all )?done[.!]*$`),
	}
	negationPattern = regexp.MustCompile(`\b(?:not|no|never|none|nothing|cannot|unable|without|unfortunately|fail|fails|failed|failing|failure|failures|error|errors|unavailable|unsuccessful|incomplete|skipped|blocked|timeout|timed out|could|might|may|should)\b|n't\b`)
	benignNegatives = regexp.MustCompile(`\b(?:0|zero|no) (?:failures?|failed|errors?|regressions?|issues)\b|\bwithout (?:any )?(?:failures?|errors?|regressions?|issues)\b`)
	sentenceEnd     = regexp.MustCompile(`[.!?]+(?:\s+|$)|\n+`)
)

// sentences splits text at sentence ends and line breaks. A period inside a
// number or a path ("3.5", "app.py") is not followed by space and does not
// split.
func sentences(text string) []string {
	var out []string
	start := 0
	for _, loc := range sentenceEnd.FindAllStringIndex(text, -1) {
		if s := strings.TrimSpace(text[start:loc[1]]); s != "" {
			out = append(out, s)
		}
		start = loc[1]
	}
	if s := strings.TrimSpace(text[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// excerpt returns the last n bytes of the trimmed text, on a rune boundary.
func excerpt(text string, n int) string {
	t := strings.TrimSpace(text)
	if len(t) <= n {
		return t
	}
	cut := len(t) - n
	for cut < len(t) && !utf8.RuneStart(t[cut]) {
		cut++
	}
	return t[cut:]
}

// readTail returns the last n bytes of the file at path.
func readTail(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if off := info.Size() - n; off > 0 {
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek %s: %w", path, err)
		}
	}
	b, err := io.ReadAll(io.LimitReader(f, n))
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return b, nil
}

// stampRun fills run, slot and pair ids a parser left empty. Parsers get the
// run context and normally set them; this keeps one missing id from making an
// event unjoinable.
func stampRun(rc *schemas.RunContext, events []schemas.Event) []schemas.Event {
	ids := newEventSeq(rc, schemas.SourceAgentStream, schemas.EvidenceNative)
	out := slices.Clone(events)
	for i := range out {
		if out[i].RunID == "" {
			out[i].RunID = ids.runID
		}
		if out[i].SlotID == "" {
			out[i].SlotID = ids.slotID
		}
		if out[i].PairID == "" {
			out[i].PairID = ids.pairID
		}
		if out[i].Source == "" {
			out[i].Source = schemas.SourceAgentStream
		}
	}
	return out
}

// newAgentStreamSource returns the agent_stream source.
func newAgentStreamSource(deps Deps) *agentStreamSource {
	return &agentStreamSource{parsers: deps.Parsers, logger: deps.Logger}
}
