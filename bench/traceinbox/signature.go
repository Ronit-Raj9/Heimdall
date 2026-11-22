package traceinbox

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/clusters"
	"github.com/maximhq/bifrost/bench/traceimport"
)

// Signature tuning. A request is cut into word 3-shingles after the failure
// clusters' normalization (numbers, ids, timestamps and paths become
// placeholders); its sketch is the sketchSize smallest shingle hashes (a
// bottom-k MinHash sketch), so two requests that differ in a word or two
// usually share the sketch and collapse into one item, and different
// requests almost never do.
const (
	shingleWords    = 3
	sketchSize      = 3
	maxRequestWords = 400
)

// redactedMarker matches a redaction placeholder, so "[REDACTED email]" is
// one token whatever was scrubbed.
var redactedMarker = regexp.MustCompile(`\[REDACTED [a-z ]+\]`)

// wordPattern splits normalized text into words; placeholders such as <n>
// stay whole.
var wordPattern = regexp.MustCompile(`<[a-z]+>|[\p{L}\p{N}_]+`)

// Signature computes the failure signature of a redacted session: the
// request sketch, the first failing tool, and the error fingerprint (the
// first stable error line of that tool, else of the outcome hints, else the
// kind of flag that raised it). The id is "f_" and 16 hex characters.
func Signature(s *traceimport.Session, flagKinds []string) (string, SignatureParts) {
	parts := SignatureParts{Request: RequestSketch(s.FirstRequest())}
	var errText string
	for _, c := range s.ToolCalls {
		if c.IsError {
			parts.Tool = c.Name
			errText = firstNonEmpty(c.Error, c.Result)
			break
		}
	}
	if fp := clusters.FirstStableLine(errText, false); fp != "" {
		parts.Error = fp
	} else if s.Outcome.Status == traceimport.OutcomeError {
		for _, h := range s.Outcome.Hints {
			if fp := clusters.FirstStableLine(h, true); fp != "" {
				parts.Error = fp
				break
			}
		}
	}
	if slices.Contains(flagKinds, FlagGolden) {
		// A passing session kept as a golden is its own item, never merged
		// with a failure of the same request.
		parts.Tool, parts.Error = "", "golden"
	}
	if parts.Error == "" && len(flagKinds) > 0 {
		kinds := slices.Clone(flagKinds)
		slices.Sort(kinds)
		parts.Error = "flag:" + strings.Join(slices.Compact(kinds), ",")
	}
	sum := sha256.Sum256([]byte(parts.Request + "\x1f" + parts.Tool + "\x1f" + parts.Error))
	return "f_" + hex.EncodeToString(sum[:8]), parts
}

// ItemID is the inbox id of a signature: one item per signature.
func ItemID(signature string) string {
	return "inc-" + strings.TrimPrefix(signature, "f_")[:12]
}

// RequestSketch returns "q_" and the hash of the request's shingle sketch,
// or "q_none" for a session without a user request.
func RequestSketch(req string) string {
	words := requestWords(req)
	if len(words) == 0 {
		return "q_none"
	}
	if len(words) < shingleWords {
		return "q_" + clusters.Hash(strings.Join(words, " "))
	}
	var hashes []uint64
	for i := 0; i+shingleWords <= len(words); i++ {
		sum := sha256.Sum256([]byte(strings.Join(words[i:i+shingleWords], " ")))
		hashes = append(hashes, binary.BigEndian.Uint64(sum[:8]))
	}
	slices.Sort(hashes)
	hashes = slices.Compact(hashes)
	if len(hashes) > sketchSize {
		hashes = hashes[:sketchSize]
	}
	parts := make([]string, len(hashes))
	for i, h := range hashes {
		parts[i] = fmt.Sprintf("%016x", h)
	}
	return "q_" + clusters.Hash(parts...)
}

// requestWords normalizes a request into words: redaction markers become one
// token, then the clusters normalizer runs line by line (its line clip is
// avoided by feeding it short lines).
func requestWords(req string) []string {
	req = redactedMarker.ReplaceAllString(req, " redacted ")
	var words []string
	for _, line := range strings.Split(req, "\n") {
		for _, chunk := range chunks(line, 120) {
			norm := clusters.NormalizeLine(chunk)
			norm = strings.TrimSuffix(norm, "...")
			words = append(words, wordPattern.FindAllString(norm, -1)...)
			if len(words) >= maxRequestWords {
				return words[:maxRequestWords]
			}
		}
	}
	return words
}

// chunks splits a line into pieces of at most n runes on spaces where it can.
func chunks(line string, n int) []string {
	fields := strings.Fields(line)
	var out []string
	var cur strings.Builder
	for _, f := range fields {
		if cur.Len() > 0 && cur.Len()+1+len(f) > n {
			out = append(out, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
		}
		cur.WriteString(f)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
