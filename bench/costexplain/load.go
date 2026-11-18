package costexplain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// metricVerifiedSuccess is the success metric of every track.
const metricVerifiedSuccess = "verified_success"

// hashPrefix is how many hex characters of a prompt or tool hash are kept.
const hashPrefix = 12

// Graded reports whether a sealed run enters a comparison (a counted run
// with no evidence problem and an outcome that scores) and whether it is a
// verified success. Success is read exactly as the leaderboard and the matrix
// read it: the verified_success metric when observed, else the outcome.
func Graded(sr *schemas.SealedRun) (success, ok bool) {
	if sr == nil || !sr.Slot.Counted || len(sr.Problems) > 0 {
		return false, false
	}
	y, scores := sr.Run.Outcome.Score()
	if !scores {
		return false, false
	}
	if v, ok := sr.Metrics[metricVerifiedSuccess].Float(); ok {
		y = 0
		if v >= 0.5 {
			y = 1
		}
	}
	return y == 1, true
}

// RunEvidence is what one run's folder says about its cost and its model
// traffic: the priced rows, the gateway rows, and the request prefixes read
// from Bifrost's raw rows.
type RunEvidence struct {
	Run          *schemas.SealedRun
	Success      bool
	Rows         []schemas.CostRow
	RowsFound    bool // cost.json (or the cost in evidence.json) was present
	Requests     []schemas.GatewayRequest
	Prompts      map[string]int // system prompt hash prefix -> message rows
	Tools        map[string]int // tool list hash prefix -> message rows
	Keys         map[string]int // provider key name -> message rows
	CacheKeyRows int            // message rows that set a prompt_cache_key (OpenAI cache routing hint)
	Refusals     int            // responses that carry a refusal
	RawRows      int            // message rows with a raw detail to read
	ReadProblems []string       // files that could not be parsed
}

// LoadRun reads one graded run's cost rows (cost.json, else the cost in
// evidence.json) and gateway rows (gateway.requests.jsonl, else the log in
// evidence.json). A file that does not parse is recorded in ReadProblems and
// read as missing, so its cost stays unknown rather than zero.
func LoadRun(sr *schemas.SealedRun, success bool, read RunFileReader) (*RunEvidence, error) {
	ev := &RunEvidence{Run: sr, Success: success, Prompts: map[string]int{}, Tools: map[string]int{}, Keys: map[string]int{}}
	if read == nil {
		return ev, nil
	}
	data, found, err := read(sr, FileCost)
	if err != nil {
		return nil, fmt.Errorf("run %s: read %s: %w", sr.Run.RunID, FileCost, err)
	}
	if found {
		if err := json.Unmarshal(data, &ev.Rows); err != nil {
			ev.ReadProblems = append(ev.ReadProblems, FileCost+": "+err.Error())
		} else {
			ev.RowsFound = true
		}
	}
	data, found, err = read(sr, FileRequests)
	if err != nil {
		return nil, fmt.Errorf("run %s: read %s: %w", sr.Run.RunID, FileRequests, err)
	}
	requestsFound := false
	if found {
		if err := ev.readRequests(data); err != nil {
			ev.ReadProblems = append(ev.ReadProblems, FileRequests+": "+err.Error())
			ev.Requests = nil
		} else {
			requestsFound = true
		}
	}
	if ev.RowsFound && requestsFound {
		return ev, nil
	}
	data, found, err = read(sr, FileEvidence)
	if err != nil {
		return nil, fmt.Errorf("run %s: read %s: %w", sr.Run.RunID, FileEvidence, err)
	}
	if !found {
		return ev, nil
	}
	var doc struct {
		Cost     *[]schemas.CostRow  `json:"cost"`
		Requests *schemas.RequestLog `json:"requests"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		ev.ReadProblems = append(ev.ReadProblems, FileEvidence+": "+err.Error())
		return ev, nil
	}
	if !ev.RowsFound && doc.Cost != nil {
		ev.Rows, ev.RowsFound = *doc.Cost, true
	}
	if !requestsFound && doc.Requests != nil {
		ev.Requests = doc.Requests.Requests
	}
	return ev, nil
}

// readRequests decodes the gateway rows, one JSON object per line (a
// decoder, since one row with its raw detail can be larger than a scanner
// line), and reads each message row's raw detail.
func (ev *RunEvidence) readRequests(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var r schemas.GatewayRequest
		err := dec.Decode(&r)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if r.Kind == "message" && len(r.Raw) > 0 {
			ev.readRaw(r.Raw)
		}
		r.Raw = nil
		ev.Requests = append(ev.Requests, r)
	}
}

// readRaw reads the parts of one Bifrost raw row that explain a cache: the
// system prompt, the tool list and the provider key, plus whether the
// response is a refusal. Fields that are absent or of another shape are
// skipped; nothing here decides a cost.
func (ev *RunEvidence) readRaw(raw json.RawMessage) {
	var row map[string]json.RawMessage
	if json.Unmarshal(raw, &row) != nil {
		return
	}
	ev.RawRows++
	var params map[string]json.RawMessage
	_ = json.Unmarshal(row["params"], &params)
	var request map[string]json.RawMessage
	var rawRequest string
	if json.Unmarshal(row["raw_request"], &rawRequest) == nil {
		_ = json.Unmarshal([]byte(rawRequest), &request)
	}
	if len(params["prompt_cache_key"]) > 0 && string(params["prompt_cache_key"]) != "null" || len(request["prompt_cache_key"]) > 0 {
		ev.CacheKeyRows++
	}
	if h := systemPromptHash(row, params, request); h != "" {
		ev.Prompts[h]++
	}
	if h := toolListHash(row, params); h != "" {
		ev.Tools[h]++
	}
	var key string
	if json.Unmarshal(row["selected_key_name"], &key) == nil && key != "" {
		ev.Keys[key]++
	}
	if isRefusal(row) {
		ev.Refusals++
	}
}

// systemPromptHash hashes the system (or developer) messages at the head of
// the input history, else the system parameter, else the system part of the
// raw request: Responses API instructions, an Anthropic system field, or
// leading system messages of messages or input.
func systemPromptHash(row, params, request map[string]json.RawMessage) string {
	parts := leadingSystem(row["input_history"])
	for _, src := range []json.RawMessage{params["system"], params["instructions"], request["instructions"], request["system"]} {
		if len(parts) > 0 {
			break
		}
		if len(src) > 0 && string(src) != "null" {
			parts = append(parts, src)
		}
	}
	if len(parts) == 0 {
		parts = leadingSystem(request["messages"])
	}
	if len(parts) == 0 {
		parts = leadingSystem(request["input"])
	}
	if len(parts) == 0 {
		return ""
	}
	return canonicalHash(parts)
}

// leadingSystem returns the content of the system or developer messages at
// the head of a message list.
func leadingSystem(list json.RawMessage) []json.RawMessage {
	var history []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if len(list) == 0 || json.Unmarshal(list, &history) != nil {
		return nil
	}
	var parts []json.RawMessage
	for _, m := range history {
		if m.Role != "system" && m.Role != "developer" {
			break
		}
		parts = append(parts, m.Content)
	}
	return parts
}

// toolListHash hashes the tool definitions sent with the request.
func toolListHash(row, params map[string]json.RawMessage) string {
	tools := row["tools"]
	if len(tools) == 0 || string(tools) == "null" {
		tools = params["tools"]
	}
	if len(tools) == 0 || string(tools) == "null" {
		return ""
	}
	return canonicalHash([]json.RawMessage{tools})
}

// isRefusal reports a refusal in the response: a non-empty refusal on the
// output message, or a refusal or content-filter stop reason in the raw
// response.
func isRefusal(row map[string]json.RawMessage) bool {
	var out struct {
		Refusal *string `json:"refusal"`
	}
	if json.Unmarshal(row["output_message"], &out) == nil && out.Refusal != nil && strings.TrimSpace(*out.Refusal) != "" {
		return true
	}
	resp := string(row["raw_response"])
	for _, marker := range []string{`stop_reason\":\"refusal`, `"stop_reason":"refusal"`, `finish_reason\":\"content_filter`, `"finish_reason":"content_filter"`} {
		if strings.Contains(resp, marker) {
			return true
		}
	}
	return false
}

// canonicalHash is the sha256 prefix of the parts re-encoded with sorted
// keys, so key order and whitespace never change it.
func canonicalHash(parts []json.RawMessage) string {
	h := sha256.New()
	for _, p := range parts {
		var v any
		if json.Unmarshal(p, &v) == nil {
			if b, err := json.Marshal(v); err == nil {
				p = b
			}
		}
		h.Write(p)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:hashPrefix]
}
