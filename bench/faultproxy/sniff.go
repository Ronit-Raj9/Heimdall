package faultproxy

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// maxSSELine bounds one buffered SSE line; longer lines (huge text deltas) are
// skipped, which loses nothing the ledger needs.
const maxSSELine = 1 << 20

// sniffer watches a response as it streams past and extracts the response id
// and usage for the ledger, in the provider's wire format. It never blocks or
// alters the stream: Write always reports the full length.
type sniffer struct {
	format   string
	sse      bool
	line     []byte
	skipping bool
	capture  bytes.Buffer // non-SSE body, up to maxCapturedBody

	id     string
	usage  *anthropicUsage
	tokens *schemas.Tokens // openai and gemini: the latest complete usage seen
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

func newSniffer(format string, sse bool) *sniffer {
	return &sniffer{format: format, sse: sse}
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
	if s.format != FormatAnthropic {
		return s.id, s.tokens
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
	data = bytes.TrimSpace(data)
	switch s.format {
	case FormatOpenAI:
		s.readOpenAI(data)
		return
	case FormatGemini:
		s.readGemini(data)
		return
	}
	var ev sseEvent
	if json.Unmarshal(data, &ev) != nil {
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
	switch s.format {
	case FormatOpenAI:
		if !s.readOpenAI(s.capture.Bytes()) {
			s.idFromPrefix(openAIIDPattern)
		}
		return
	case FormatGemini:
		if !s.readGemini(s.capture.Bytes()) {
			s.idFromPrefix(geminiIDPattern)
		}
		return
	}
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

// readOpenAI reads one Chat Completions body or chunk, or one Responses body
// or stream event. The id is the first chatcmpl- or resp_ id seen; usage is
// the latest complete one (OpenAI sends it once, at the end). It reports
// whether data was JSON.
func (s *sniffer) readOpenAI(data []byte) bool {
	var ev struct {
		ID       string       `json:"id"`
		Object   string       `json:"object"`
		Usage    *openAIUsage `json:"usage"`
		Response *struct {
			ID    string       `json:"id"`
			Usage *openAIUsage `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &ev) != nil {
		return false
	}
	switch {
	case strings.HasPrefix(ev.Object, "chat.completion") || ev.Object == "response":
		if s.id == "" {
			s.id = ev.ID
		}
		s.setTokens(ev.Usage.tokens())
	case ev.Response != nil:
		if s.id == "" {
			s.id = ev.Response.ID
		}
		s.setTokens(ev.Response.Usage.tokens())
	}
	return true
}

// readGemini reads one generateContent body or stream chunk: responseId and
// usageMetadata, which each chunk repeats cumulatively.
func (s *sniffer) readGemini(data []byte) bool {
	var ev struct {
		ResponseID string `json:"responseId"`
		Usage      *struct {
			Prompt     *int64 `json:"promptTokenCount"`
			Candidates *int64 `json:"candidatesTokenCount"`
			Cached     *int64 `json:"cachedContentTokenCount"`
			Thoughts   *int64 `json:"thoughtsTokenCount"`
		} `json:"usageMetadata"`
	}
	if json.Unmarshal(data, &ev) != nil {
		return false
	}
	if s.id == "" {
		s.id = ev.ResponseID
	}
	if u := ev.Usage; u != nil && u.Prompt != nil {
		cached := deref(u.Cached)
		s.tokens = &schemas.Tokens{Fresh: max(*u.Prompt-cached, 0), CacheRead: cached, Out: deref(u.Candidates) + deref(u.Thoughts)}
	}
	return true
}

func (s *sniffer) setTokens(t *schemas.Tokens) {
	if t != nil {
		s.tokens = t
	}
}

// idFromPrefix recovers the id from a truncated capture.
func (s *sniffer) idFromPrefix(re *regexp.Regexp) {
	if m := re.FindSubmatch(s.capture.Bytes()); m != nil && s.id == "" {
		s.id = string(m[1])
	}
}

// openAIUsage covers both OpenAI usage shapes: Chat Completions
// (prompt/completion) and Responses (input/output).
type openAIUsage struct {
	PromptTokens     *int64 `json:"prompt_tokens"`
	CompletionTokens *int64 `json:"completion_tokens"`
	InputTokens      *int64 `json:"input_tokens"`
	OutputTokens     *int64 `json:"output_tokens"`
	PromptDetails    *struct {
		CachedTokens *int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	InputDetails *struct {
		CachedTokens *int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

// tokens splits OpenAI usage into the ledger's buckets: prompt or input
// tokens include the cached ones, and completion or output tokens include
// reasoning. OpenAI has no cache-write bucket.
func (u *openAIUsage) tokens() *schemas.Tokens {
	if u == nil {
		return nil
	}
	in, out := u.PromptTokens, u.CompletionTokens
	if in == nil {
		in, out = u.InputTokens, u.OutputTokens
	}
	if in == nil || out == nil {
		return nil
	}
	var cached int64
	switch {
	case u.PromptDetails != nil:
		cached = deref(u.PromptDetails.CachedTokens)
	case u.InputDetails != nil:
		cached = deref(u.InputDetails.CachedTokens)
	}
	return &schemas.Tokens{Fresh: max(*in-cached, 0), CacheRead: cached, Out: *out}
}

func deref(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

var (
	messageIDPattern = regexp.MustCompile(`"id"\s*:\s*"(msg_[A-Za-z0-9_-]+)"`)
	openAIIDPattern  = regexp.MustCompile(`"id"\s*:\s*"((?:chatcmpl-|resp_)[A-Za-z0-9_-]+)"`)
	geminiIDPattern  = regexp.MustCompile(`"responseId"\s*:\s*"([A-Za-z0-9_-]+)"`)
)
