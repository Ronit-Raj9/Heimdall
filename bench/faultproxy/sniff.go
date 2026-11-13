package faultproxy

import (
	"bytes"
	"encoding/json"
	"regexp"

	"github.com/maximhq/bifrost/bench/schemas"
)

// maxSSELine bounds one buffered SSE line; longer lines (huge text deltas) are
// skipped, which loses nothing the ledger needs.
const maxSSELine = 1 << 20

// sniffer watches a response as it streams past and extracts the message id
// and usage for the ledger. It never blocks or alters the stream: Write
// always reports the full length.
type sniffer struct {
	sse      bool
	line     []byte
	skipping bool
	capture  bytes.Buffer // non-SSE body, up to maxCapturedBody

	id    string
	usage *anthropicUsage
}

// anthropicUsage is Anthropic's usage object. Pointers tell "absent" from 0
// so a message_delta can update only the fields it carries.
type anthropicUsage struct {
	Input      *int64 `json:"input_tokens"`
	Output     *int64 `json:"output_tokens"`
	CacheRead  *int64 `json:"cache_read_input_tokens"`
	CacheWrite *int64 `json:"cache_creation_input_tokens"`
}

// sseEvent is the subset of a streamed event the sniffer reads.
type sseEvent struct {
	Type    string `json:"type"`
	Message struct {
		ID    string          `json:"id"`
		Usage *anthropicUsage `json:"usage"`
	} `json:"message"`
	Usage *anthropicUsage `json:"usage"`
}

func newSniffer(sse bool) *sniffer {
	return &sniffer{sse: sse}
}

// Write implements io.Writer.
func (s *sniffer) Write(p []byte) (int, error) {
	if !s.sse {
		if room := maxCapturedBody - s.capture.Len(); room > 0 {
			s.capture.Write(p[:min(room, len(p))])
		}
		return len(p), nil
	}
	rest := p
	for len(rest) > 0 {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			s.buffer(rest)
			break
		}
		s.buffer(rest[:i])
		if !s.skipping {
			s.handleLine(bytes.TrimRight(s.line, "\r"))
		}
		s.line, s.skipping = s.line[:0], false
		rest = rest[i+1:]
	}
	return len(p), nil
}

// result returns the response id and usage seen so far. Usage is nil when the
// response carried none, so the ledger never records a guessed zero.
func (s *sniffer) result() (string, *schemas.Tokens) {
	if !s.sse {
		s.parseBody()
	}
	if s.usage == nil {
		return s.id, nil
	}
	t := &schemas.Tokens{}
	if s.usage.Input != nil {
		t.Fresh = *s.usage.Input
	}
	if s.usage.CacheRead != nil {
		t.CacheRead = *s.usage.CacheRead
	}
	if s.usage.CacheWrite != nil {
		t.CacheWrite = *s.usage.CacheWrite
	}
	if s.usage.Output != nil {
		t.Out = *s.usage.Output
	}
	return s.id, t
}

func (s *sniffer) buffer(b []byte) {
	if s.skipping {
		return
	}
	if len(s.line)+len(b) > maxSSELine {
		s.skipping = true
		s.line = s.line[:0]
		return
	}
	s.line = append(s.line, b...)
}

func (s *sniffer) handleLine(line []byte) {
	data, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		return
	}
	var ev sseEvent
	if json.Unmarshal(bytes.TrimSpace(data), &ev) != nil {
		return
	}
	switch ev.Type {
	case "message_start":
		if s.id == "" {
			s.id = ev.Message.ID
		}
		s.merge(ev.Message.Usage)
	case "message_delta":
		s.merge(ev.Usage)
	}
}

// merge folds u into the running usage. Anthropic's message_delta usage is
// cumulative, so later values replace earlier ones field by field.
func (s *sniffer) merge(u *anthropicUsage) {
	if u == nil {
		return
	}
	if s.usage == nil {
		s.usage = &anthropicUsage{}
	}
	if u.Input != nil {
		s.usage.Input = u.Input
	}
	if u.Output != nil {
		s.usage.Output = u.Output
	}
	if u.CacheRead != nil {
		s.usage.CacheRead = u.CacheRead
	}
	if u.CacheWrite != nil {
		s.usage.CacheWrite = u.CacheWrite
	}
}

// parseBody reads id and usage from a captured JSON body. A truncated capture
// still yields the id, which Anthropic puts first.
func (s *sniffer) parseBody() {
	var body struct {
		ID    string          `json:"id"`
		Type  string          `json:"type"`
		Usage *anthropicUsage `json:"usage"`
	}
	if json.Unmarshal(s.capture.Bytes(), &body) == nil {
		if body.Type == "message" {
			s.id = body.ID
			s.merge(body.Usage)
		}
		return
	}
	if m := messageIDPattern.FindSubmatch(s.capture.Bytes()); m != nil {
		s.id = string(m[1])
	}
}

var messageIDPattern = regexp.MustCompile(`"id"\s*:\s*"(msg_[A-Za-z0-9_-]+)"`)
