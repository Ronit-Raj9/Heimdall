package bifrost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Request kinds, as schemas.GatewayRequest and schemas.LedgerEntry name them.
const (
	kindMessage     = "message"
	kindCountTokens = "count_tokens"
	kindModels      = "models"
	kindOther       = "other"

	statusProcessing = "processing"
)

// objectKinds maps Bifrost's row object (its request type) to a kind. An
// Anthropic /v1/messages call through Bifrost is logged as responses or
// responses_stream.
var objectKinds = map[string]string{
	"responses":              kindMessage,
	"responses_stream":       kindMessage,
	"chat_completion":        kindMessage,
	"chat_completion_stream": kindMessage,
	"text_completion":        kindMessage,
	"text_completion_stream": kindMessage,
	"count_tokens":           kindCountTokens,
	"list_models":            kindModels,
}

// Fault proxy error messages carry the run's nonce. The bench proxy writes
// "ref <nonce> call <n>"; the spike proxy wrote "(nonce <nonce>)" with a
// per-call nonce.
var (
	faultRefPattern   = regexp.MustCompile(`\bref ([A-Za-z0-9_-]+) call ([0-9]+)\b`)
	spikeNoncePattern = regexp.MustCompile(`\(nonce ([A-Za-z0-9_-]+)\)`)
	targetInputFields = []string{"file_path", "path", "command", "pattern"}
	errTrailingJSON   = errors.New("trailing data after JSON value")
)

// parsedRow is one detail row: the bench's view of it plus the fields the
// ledger join needs.
type parsedRow struct {
	Req       schemas.GatewayRequest
	ReqSHA256 string // sha256(raw_request); empty when Bifrost stored no full request
	Nonce     string // fault nonce found in the error or raw response
	NonceCall int    // the call number after the nonce; 0 when absent
	Upstream  bool   // the row has at least one attempt, so it reached the proxy
}

// detailRow is the subset of GET /api/logs/{id} the bench reads.
type detailRow struct {
	ID              string        `json:"id"`
	ParentRequestID *string       `json:"parent_request_id"`
	Timestamp       time.Time     `json:"timestamp"`
	Object          string        `json:"object"`
	Provider        string        `json:"provider"`
	Model           string        `json:"model"`
	Alias           *string       `json:"alias"`
	FallbackIndex   int           `json:"fallback_index"`
	Latency         *float64      `json:"latency"`
	Cost            *float64      `json:"cost"`
	Status          string        `json:"status"`
	Stream          bool          `json:"stream"`
	RawRequest      string        `json:"raw_request"`
	RawResponse     string        `json:"raw_response"`
	LargeRequest    bool          `json:"is_large_payload_request"`
	TokenUsage      *tokenUsage   `json:"token_usage"`
	ErrorDetails    *errorDetails `json:"error_details"`
	AttemptTrail    []attempt     `json:"attempt_trail"`
	CacheDebug      *struct {
		CacheHit bool `json:"cache_hit"`
	} `json:"cache_debug"`
	ToolCalls []struct {
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

type tokenUsage struct {
	PromptTokens        *int64 `json:"prompt_tokens"`
	CompletionTokens    *int64 `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedReadTokens  *int64 `json:"cached_read_tokens"`
		CachedTokens      *int64 `json:"cached_tokens"`
		CachedWriteTokens *int64 `json:"cached_write_tokens"`
		CacheWriteTokens  *int64 `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
}

type errorDetails struct {
	StatusCode *int `json:"status_code"`
	Error      *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type attempt struct {
	Attempt      int     `json:"attempt"`
	KeyName      string  `json:"key_name"`
	FailReason   *string `json:"fail_reason"`
	FailureClass *string `json:"failure_class"`
	StatusCode   *int    `json:"status_code"`
	RetryAfterMS *int    `json:"retry_after_ms"`
}

// parseDetail turns one detail row into a parsedRow. Raw keeps the whole row,
// raw_request included, because probes search it for their nonce. It is
// pure: no I/O, no logging.
func parseDetail(raw []byte) (*parsedRow, error) {
	var d detailRow
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("parse detail row: %w", err)
	}
	if d.ID == "" {
		return nil, errors.New("parse detail row: no id")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, fmt.Errorf("parse detail row %s: %w", d.ID, err)
	}
	r := schemas.GatewayRequest{
		ID: d.ID, Timestamp: d.Timestamp, Status: d.Status, Kind: kindOf(d.Object),
		Provider: d.Provider, ModelRequested: d.Model, ModelServed: d.Model,
		FallbackIndex: d.FallbackIndex, Stream: d.Stream, BifrostUSD: d.Cost,
		CacheHit: d.CacheDebug != nil && d.CacheDebug.CacheHit, Raw: compact.Bytes(),
		Tokens: d.TokenUsage.tokens(), Attempts: d.attempts(),
	}
	if d.ParentRequestID != nil {
		r.ParentID = *d.ParentRequestID
	}
	if d.Latency != nil {
		r.LatencyMS = *d.Latency
	}
	if m := requestedModel(d); m != "" {
		r.ModelRequested = m
	}
	resp := responseInfo(d.RawResponse)
	r.ResponseID = resp.id
	if resp.model != "" {
		r.ModelServed = resp.model
	}
	if d.ErrorDetails != nil {
		if d.ErrorDetails.StatusCode != nil {
			r.ErrorStatus = *d.ErrorDetails.StatusCode
		}
		if d.ErrorDetails.Error != nil {
			r.ErrorMessage = d.ErrorDetails.Error.Message
		}
	}
	r.ToolCalls = d.toolCalls()
	r.ToolResultIDs = toolResultIDs(d.RawRequest)
	p := &parsedRow{Req: r, Upstream: len(d.AttemptTrail) > 0}
	if d.RawRequest != "" && !d.LargeRequest {
		p.ReqSHA256 = utils.SHA256Bytes([]byte(d.RawRequest))
	}
	p.Nonce, p.NonceCall = findNonce(r.ErrorMessage, d.RawResponse)
	return p, nil
}

// toolCallsFromRaw returns the tool_use blocks of an Anthropic response body,
// a JSON message or, for a stream, the event payloads Bifrost stored one per
// blank-line-separated chunk. Streamed input arrives as input_json_delta
// fragments and is joined before hashing. ArgsHash is the hex sha256 of the
// canonical input JSON, empty when the input could not be parsed (a cut
// stream). It is pure.
func toolCallsFromRaw(rawResponse string, stream bool) ([]schemas.ToolCallRef, error) {
	if strings.TrimSpace(rawResponse) == "" {
		return nil, nil
	}
	if !stream {
		var msg struct {
			Content []contentBlock `json:"content"`
		}
		if err := json.Unmarshal([]byte(rawResponse), &msg); err != nil {
			return nil, fmt.Errorf("parse response body: %w", err)
		}
		var out []schemas.ToolCallRef
		for _, b := range msg.Content {
			if b.Type == "tool_use" {
				out = append(out, toolCallRef(b.ID, b.Name, b.Input))
			}
		}
		return out, nil
	}
	return streamToolCalls(rawResponse)
}

// contentBlock is one Anthropic content block.
type contentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
}

// streamEvent is one Anthropic stream event payload.
type streamEvent struct {
	Type         string        `json:"type"`
	Index        int           `json:"index"`
	ContentBlock *contentBlock `json:"content_block"`
	Delta        *struct {
		Type        string `json:"type"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	Message *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
	} `json:"message"`
}

func streamToolCalls(raw string) ([]schemas.ToolCallRef, error) {
	type pending struct {
		block   contentBlock
		partial strings.Builder
		deltas  bool
	}
	open := map[int]*pending{}
	var order []*pending
	for _, ev := range streamEvents(raw) {
		switch ev.Type {
		case "content_block_start":
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				p := &pending{block: *ev.ContentBlock}
				open[ev.Index] = p
				order = append(order, p)
			}
		case "content_block_delta":
			if p, ok := open[ev.Index]; ok && ev.Delta != nil && ev.Delta.Type == "input_json_delta" {
				p.partial.WriteString(ev.Delta.PartialJSON)
				p.deltas = true
			}
		case "content_block_stop":
			delete(open, ev.Index)
		}
	}
	out := make([]schemas.ToolCallRef, 0, len(order))
	for _, p := range order {
		input := p.block.Input
		if p.deltas {
			input = json.RawMessage(p.partial.String())
		}
		out = append(out, toolCallRef(p.block.ID, p.block.Name, input))
	}
	return out, nil
}

// streamEvents decodes the stored stream chunks, skipping any that are not
// JSON (an SSE comment or a truncated tail).
func streamEvents(raw string) []streamEvent {
	var out []streamEvent
	for _, chunk := range strings.Split(raw, "\n\n") {
		payload := ssePayload(chunk)
		if payload == "" {
			continue
		}
		var ev streamEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// ssePayload strips optional "event:" and "data:" framing from one chunk.
func ssePayload(chunk string) string {
	var data []string
	for _, line := range strings.Split(strings.TrimSpace(chunk), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case strings.HasPrefix(line, "event:"):
		default:
			data = append(data, line)
		}
	}
	return strings.Join(data, "\n")
}

// toolCallRef hashes a tool call's input and reads its target.
func toolCallRef(id, name string, input json.RawMessage) schemas.ToolCallRef {
	ref := schemas.ToolCallRef{ID: id, Name: name}
	if len(bytes.TrimSpace(input)) == 0 {
		input = json.RawMessage("{}")
	}
	v, err := decodeJSON(input)
	if err != nil {
		return ref
	}
	if h, err := utils.HashJSON(v); err == nil {
		ref.ArgsHash = h
	}
	if obj, ok := v.(map[string]any); ok {
		for _, f := range targetInputFields {
			if s, ok := obj[f].(string); ok && s != "" {
				ref.Target = s
				break
			}
		}
	}
	return ref
}

// decodeJSON decodes b keeping numbers exact, so the canonical form (and the
// hash) of an input does not depend on float formatting.
func decodeJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errTrailingJSON
	}
	return v, nil
}

// toolResultIDs returns the tool_use_id of every tool_result block in an
// Anthropic request body, in order, without repeats.
func toolResultIDs(rawRequest string) []string {
	if strings.TrimSpace(rawRequest) == "" {
		return nil
	}
	var req struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(rawRequest), &req); err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range req.Messages {
		var blocks []contentBlock
		if err := json.Unmarshal(m.Content, &blocks); err != nil {
			continue // string content has no blocks
		}
		for _, b := range blocks {
			if b.Type == "tool_result" && b.ToolUseID != "" && !seen[b.ToolUseID] {
				seen[b.ToolUseID] = true
				out = append(out, b.ToolUseID)
			}
		}
	}
	return out
}

// response holds what the upstream response says about itself.
type response struct {
	id    string
	model string
}

// responseInfo reads the upstream message id and model: the top-level
// message of a JSON body, or the message_start event of a stream.
func responseInfo(raw string) response {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return response{}
	}
	var msg struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(raw), &msg); err == nil {
		if msg.Type == "message" {
			return response{id: msg.ID, model: msg.Model}
		}
		return response{}
	}
	for _, ev := range streamEvents(raw) {
		if ev.Type == "message_start" && ev.Message != nil {
			return response{id: ev.Message.ID, model: ev.Message.Model}
		}
	}
	return response{}
}

// findNonce returns the fault nonce and call number from an error message or
// the raw upstream body.
func findNonce(texts ...string) (string, int) {
	for _, t := range texts {
		if m := faultRefPattern.FindStringSubmatch(t); m != nil {
			n, err := strconv.Atoi(m[2])
			if err != nil {
				n = 0
			}
			return m[1], n
		}
	}
	for _, t := range texts {
		if m := spikeNoncePattern.FindStringSubmatch(t); m != nil {
			return m[1], 0
		}
	}
	return "", 0
}

// requestedModel is the model the client asked for: the alias when Bifrost
// resolved one, else the model in the stored request.
func requestedModel(d detailRow) string {
	if d.Alias != nil && *d.Alias != "" {
		return *d.Alias
	}
	var req struct {
		Model string `json:"model"`
	}
	if d.RawRequest != "" && json.Unmarshal([]byte(d.RawRequest), &req) == nil {
		return req.Model
	}
	return ""
}

func kindOf(object string) string {
	if k, ok := objectKinds[object]; ok {
		return k
	}
	return kindOther
}

// tokens splits Bifrost's usage into four disjoint buckets. prompt_tokens
// includes cache reads and writes (64 = 16 fresh + 16 read + 32 written in
// the spike rows). Nil when usage was not recorded.
func (u *tokenUsage) tokens() *schemas.Tokens {
	if u == nil || u.PromptTokens == nil || u.CompletionTokens == nil {
		return nil
	}
	var read, write int64
	if d := u.PromptTokensDetails; d != nil {
		read = firstSet(d.CachedReadTokens, d.CachedTokens)
		write = firstSet(d.CachedWriteTokens, d.CacheWriteTokens)
	}
	fresh := *u.PromptTokens - read - write
	if fresh < 0 {
		fresh = 0
	}
	return &schemas.Tokens{Fresh: fresh, CacheRead: read, CacheWrite: write, Out: *u.CompletionTokens}
}

func firstSet(vals ...*int64) int64 {
	for _, v := range vals {
		if v != nil {
			return *v
		}
	}
	return 0
}

// attempts converts the attempt trail; N is 1-based.
func (d detailRow) attempts() []schemas.Attempt {
	out := make([]schemas.Attempt, 0, len(d.AttemptTrail))
	for _, a := range d.AttemptTrail {
		x := schemas.Attempt{N: a.Attempt + 1, KeyName: a.KeyName}
		if a.StatusCode != nil {
			x.StatusCode = *a.StatusCode
		}
		if a.FailReason != nil {
			x.FailReason = *a.FailReason
		}
		if a.FailureClass != nil {
			x.FailureClass = *a.FailureClass
		}
		if a.RetryAfterMS != nil {
			x.RetryAfterMS = *a.RetryAfterMS
		}
		out = append(out, x)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// toolCalls reads tool calls from the raw upstream response; when Bifrost
// stored none (raw storage off) it falls back to the row's own tool_calls.
func (d detailRow) toolCalls() []schemas.ToolCallRef {
	if d.RawResponse != "" {
		if calls, err := toolCallsFromRaw(d.RawResponse, d.Stream); err == nil {
			return calls
		}
	}
	var out []schemas.ToolCallRef
	for _, tc := range d.ToolCalls {
		out = append(out, toolCallRef(tc.ID, tc.Function.Name, json.RawMessage(tc.Function.Arguments)))
	}
	return out
}
