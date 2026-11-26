package evidence

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/secrets"
	"github.com/maximhq/bifrost/bench/utils"
)

// removed replaces every eval cue, protected task id and canary.
const removed = "[removed]"

// cueWords are the eval cue words of improve/gates.go the improver must
// never read (A3.1), plus the canary marker.
var cueWords = []string{"bench_", "toolproxy", "/opt/bench", "heimdall", "grader", "verifier", "hidden test", strings.ToLower(utils.CanaryMarker)}

var cuePattern = func() *regexp.Regexp {
	q := make([]string, len(cueWords))
	for i, w := range cueWords {
		q[i] = regexp.QuoteMeta(w)
	}
	return regexp.MustCompile(`(?i)(` + strings.Join(q, "|") + `)`)
}()

// shingleLen is the word n-gram length of the answer check, as in the
// integrity gates.
const shingleLen = 8

// scrubber cleans every text the improver may read, in the order of A3.1:
// literal keys and known secret shapes, then key-value secrets, then eval
// cues, protected task ids and canaries. leaks reports text that holds a
// protected secret; such a run is dropped whole.
type scrubber struct {
	literal  []string
	ids      *regexp.Regexp
	canaries []string
	secret   map[string]bool // 8-word shingles of the protected secrets
	short    []string        // protected secrets under 8 words, lowercased
}

func newScrubber(p improve.Protected) *scrubber {
	s := &scrubber{secret: map[string]bool{}}
	var ids []string
	for _, id := range p.TaskIDs {
		if id != "" {
			ids = append(ids, regexp.QuoteMeta(id))
		}
	}
	if len(ids) > 0 {
		s.ids = regexp.MustCompile(`(^|[^A-Za-z0-9_])(` + strings.Join(ids, "|") + `)($|[^A-Za-z0-9_])`)
	}
	for _, c := range p.Canaries {
		if c != "" {
			s.canaries = append(s.canaries, c)
		}
	}
	for _, sec := range p.Secrets {
		sh := shingles(sec)
		if len(sh) == 0 {
			if t := strings.ToLower(strings.TrimSpace(sec)); len(t) >= 8 {
				s.short = append(s.short, t)
			}
			continue
		}
		for k := range sh {
			s.secret[k] = true
		}
	}
	return s
}

// withLiteral returns a copy that also masks the run's literal keys.
func (s *scrubber) withLiteral(lit []string) *scrubber {
	c := *s
	c.literal = append(append([]string(nil), s.literal...), lit...)
	return &c
}

func (s *scrubber) clean(t string) string {
	if t == "" {
		return t
	}
	t = secrets.Redact(t, s.literal...)
	t = utils.RedactSecrets(t)
	t = cuePattern.ReplaceAllString(t, removed)
	if s.ids != nil {
		// Twice: adjacent ids share the separator between them.
		for range 2 {
			t = s.ids.ReplaceAllString(t, "${1}"+removed+"${3}")
		}
	}
	for _, c := range s.canaries {
		t = strings.ReplaceAll(t, c, removed)
	}
	return t
}

func (s *scrubber) leaks(t string) bool {
	if t == "" {
		return false
	}
	if len(s.secret) > 0 {
		for k := range shingles(t) {
			if s.secret[k] {
				return true
			}
		}
	}
	if len(s.short) > 0 {
		low := strings.ToLower(t)
		for _, sec := range s.short {
			if strings.Contains(low, sec) {
				return true
			}
		}
	}
	return false
}

// shingles is the set of word n-grams of s, lowercased, punctuation
// dropped (the same rule as the integrity gates).
func shingles(s string) map[string]bool {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
	out := map[string]bool{}
	for i := 0; i+shingleLen <= len(words); i++ {
		out[strings.Join(words[i:i+shingleLen], " ")] = true
	}
	return out
}
