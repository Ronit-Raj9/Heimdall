// Command toolscript is a scripted tool-using model for $0 proofs of
// Bifrost's own features (docs/bifrost-features.md, B2). It speaks the
// Anthropic Messages API (POST /v1/messages), so a throwaway fault proxy can
// use it as its anthropic upstream (BENCH_FAULTPROXY_UPSTREAM) and Bifrost, the
// proxy and the bench all run for real while no provider is ever called.
//
// Unlike the fault proxy's scripted model, which answers every tool turn
// with zero-valued arguments, toolscript replays a per-task script of tool
// calls, Code Mode programs and final answers, so a scripted run can solve
// an MCP task in classic mode (direct tool calls) and in Code Mode (one
// executeToolCode program) and the verifier decides both. The script is
// JSON, re-read on every request so it can be swapped while the server runs:
//
//	{
//	  "default_text": "Done.",
//	  "simulate_prompt_cache": true,
//	  "tasks": [
//	    {"id": "t01", "match": "refund order 1042",
//	     "classic":   [{"tool": "lookup_order", "input": {"order_id": "1042"}},
//	                   {"tool": "refund", "input": {"order_id": "1042"}},
//	                   {"text": "Refunded."}],
//	     "code_mode": [{"tool": "listToolFiles", "input": {}},
//	                   {"code": "o = helpdesk.lookup_order(order_id=\"1042\")\n..."},
//	                   {"text": "Refunded."}]}
//	  ]
//	}
//
// A request is matched to the first task whose match string occurs in its
// first user message (case-insensitive). The step is the number of assistant
// messages already in the request, so every agent turn advances the script
// by one. The variant is code_mode when the request offers a tool named
// executeToolCode (with or without a client prefix), else classic. A tool
// step names the tool without its gateway prefix: toolscript picks the
// offered tool whose name equals it or ends with "-<name>", "_<name>" or
// "__<name>" (Bifrost prefixes MCP tools with their client name). A code
// step calls executeToolCode with {"code": ...}. Past the last step, or for
// a request no task matches, the reply is default_text.
//
// In Code Mode the model must discover the server's module name, which the
// bench makes unique per run: "{{server}}" in a code step or in a string
// input is replaced with the first "<name>.pyi" a listToolFiles result in
// the conversation named, so a script reads listToolFiles, then
// readToolFile {"fileName": "servers/{{server}}.pyi"}, then the program.
//
// Usage is deterministic: input tokens are the request body length / 4.
// With simulate_prompt_cache, a request whose blocks carry cache_control is
// treated the way a caching provider would treat it: the prefix up to the
// last marked block (tools, system, messages) is written to an in-memory
// cache on first sight (cache_creation_input_tokens) and read on every later
// request with the same prefix (cache_read_input_tokens). It is a
// simulation of provider caching, labelled as such wherever it is used.
// A request with "stream": true is answered as Server-Sent Events.
//
// POST /v1/embeddings (the OpenAI shape) answers with a deterministic
// bag-of-words embedding: every lower-cased word is hashed into one of
// embeddingDim buckets and the vector is L2-normalised, so texts that share
// most words are close and unrelated texts are not. It stands in for an
// embedding provider behind Bifrost's semantic cache and complexity
// classifier; its similarities are not a real model's.
// Every request is logged as one JSON line on stderr.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
)

type step struct {
	Tool  string         `json:"tool,omitempty"`
	Input map[string]any `json:"input,omitempty"`
	Code  string         `json:"code,omitempty"`
	Text  string         `json:"text,omitempty"`
}

type task struct {
	ID       string `json:"id"`
	Match    string `json:"match"`
	Classic  []step `json:"classic"`
	CodeMode []step `json:"code_mode"`
}

type script struct {
	DefaultText         string `json:"default_text"`
	SimulatePromptCache bool   `json:"simulate_prompt_cache"`
	Tasks               []task `json:"tasks"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type request struct {
	Model    string            `json:"model"`
	Stream   bool              `json:"stream"`
	System   json.RawMessage   `json:"system"`
	Messages []message         `json:"messages"`
	Tools    []json.RawMessage `json:"tools"`
}

const executeToolCode = "executeToolCode"

var (
	cacheMu sync.Mutex
	cache   = map[string]bool{}
)

func main() {
	listen := flag.String("listen", "127.0.0.1:9200", "address to listen on")
	path := flag.String("script", "script.json", "tool script (JSON), re-read on every request")
	flag.Parse()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 16<<20))
		var req request
		if err := json.Unmarshal(raw, &req); err != nil || len(req.Messages) == 0 {
			http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"toolscript: not a messages request"}}`, http.StatusBadRequest)
			return
		}
		sc, err := load(*path)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"type":"error","error":{"type":"api_error","message":%q}}`, err.Error()), http.StatusInternalServerError)
			return
		}
		reply := answer(sc, &req)
		usage := map[string]any{"input_tokens": len(raw)/4 + 1, "output_tokens": 16}
		cacheNote := ""
		if sc.SimulatePromptCache {
			if prefix, ok := cachedPrefix(&req); ok {
				n := len(prefix)/4 + 1
				in := len(raw)/4 + 1 - n
				if in < 1 {
					in = 1
				}
				usage["input_tokens"] = in
				sum := sha256.Sum256(prefix)
				key := hex.EncodeToString(sum[:])
				cacheMu.Lock()
				hit := cache[key]
				cache[key] = true
				cacheMu.Unlock()
				if hit {
					usage["cache_read_input_tokens"], cacheNote = n, "read"
				} else {
					usage["cache_creation_input_tokens"], cacheNote = n, "write"
				}
			}
		}
		line, _ := json.Marshal(map[string]any{"model": req.Model, "task": reply.task, "variant": reply.variant, "step": reply.step,
			"tool": reply.toolName, "tools_offered": len(req.Tools), "body_bytes": len(raw), "cache": cacheNote})
		log.Print(string(line))
		block := map[string]any{"type": "text", "text": reply.text}
		stop := "end_turn"
		if reply.toolName != "" {
			block = map[string]any{"type": "tool_use", "id": fmt.Sprintf("toolu_ts_%s_%d", reply.task, reply.step), "name": reply.toolName, "input": reply.input}
			stop = "tool_use"
		}
		id := fmt.Sprintf("msg_toolscript_%s_%d", reply.task, reply.step)
		if req.Stream {
			stream(w, id, req.Model, block, stop, usage)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": id, "type": "message", "role": "assistant", "model": req.Model,
			"content": []any{block}, "stop_reason": stop, "usage": usage,
		})
	})
	mux.HandleFunc("POST /v1/embeddings", embeddings)
	log.SetFlags(0)
	log.Printf("toolscript listening on %s, script %s", *listen, *path)
	log.Fatal(http.ListenAndServe(*listen, mux))
}

func load(path string) (*script, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sc script
	if err := json.Unmarshal(b, &sc); err != nil {
		return nil, fmt.Errorf("script %s: %w", path, err)
	}
	if sc.DefaultText == "" {
		sc.DefaultText = "Done."
	}
	return &sc, nil
}

type reply struct {
	task, variant, text, toolName string
	step                          int
	input                         map[string]any
}

// answer picks the reply for one request.
func answer(sc *script, req *request) reply {
	first := ""
	for _, m := range req.Messages {
		if m.Role == "user" {
			first = text(m.Content)
			break
		}
	}
	n := 0
	for _, m := range req.Messages {
		if m.Role == "assistant" {
			n++
		}
	}
	offered := toolNames(req.Tools)
	variant := "classic"
	if resolve(offered, executeToolCode) != "" {
		variant = "code_mode"
	}
	out := reply{text: sc.DefaultText, step: n, variant: variant, task: "none"}
	for _, t := range sc.Tasks {
		if t.Match == "" || !strings.Contains(strings.ToLower(first), strings.ToLower(t.Match)) {
			continue
		}
		out.task = t.ID
		steps := t.Classic
		if variant == "code_mode" && len(t.CodeMode) > 0 {
			steps = t.CodeMode
		}
		if n >= len(steps) {
			return out
		}
		s := steps[n]
		server := discoveredServer(req.Messages)
		switch {
		case s.Code != "":
			out.toolName = resolve(offered, executeToolCode)
			out.input = map[string]any{"code": strings.ReplaceAll(s.Code, "{{server}}", server)}
		case s.Tool != "":
			out.toolName = resolve(offered, s.Tool)
			if out.toolName == "" {
				out.text = fmt.Sprintf("toolscript: tool %s is not offered (%s)", s.Tool, strings.Join(offered, ", "))
				return out
			}
			out.input = map[string]any{}
			for k, v := range s.Input {
				if str, ok := v.(string); ok {
					v = strings.ReplaceAll(str, "{{server}}", server)
				}
				out.input[k] = v
			}
		default:
			out.text = s.Text
		}
		return out
	}
	return out
}

// resolve returns the offered tool that is name, or name behind a client
// prefix.
func resolve(offered []string, name string) string {
	for _, o := range offered {
		if o == name {
			return o
		}
	}
	for _, o := range offered {
		for _, sep := range []string{"__", "-", "_"} {
			if strings.HasSuffix(o, sep+name) {
				return o
			}
		}
	}
	return ""
}

func toolNames(tools []json.RawMessage) []string {
	var out []string
	for _, raw := range tools {
		var t struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &t) == nil && t.Name != "" {
			out = append(out, t.Name)
		}
	}
	return out
}

// text reads a message content that is a string or a list of text parts.
func text(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &parts)
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

// cachedPrefix returns the canonical bytes of the request prefix that ends at
// the last block carrying cache_control (tools, then system, then messages),
// and whether any block carries one.
func cachedPrefix(req *request) ([]byte, bool) {
	marked := func(raw json.RawMessage) bool { return strings.Contains(string(raw), `"cache_control"`) }
	var parts [][]byte
	last := -1
	for _, t := range req.Tools {
		parts = append(parts, t)
		if marked(t) {
			last = len(parts)
		}
	}
	if len(req.System) > 0 {
		parts = append(parts, req.System)
		if marked(req.System) {
			last = len(parts)
		}
	}
	for _, m := range req.Messages {
		parts = append(parts, m.Content)
		if marked(m.Content) {
			last = len(parts)
		}
	}
	if last < 0 {
		return nil, false
	}
	var b []byte
	for _, p := range parts[:last] {
		b = append(b, p...)
	}
	return b, true
}

var pyiRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\.pyi\b`)

// discoveredServer is the first Code Mode module a listToolFiles result in
// the conversation named, or "" before one did.
func discoveredServer(msgs []message) string {
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		var blocks []struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(m.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type != "tool_result" {
				continue
			}
			if got := pyiRe.FindStringSubmatch(text(b.Content)); got != nil {
				return got[1]
			}
		}
	}
	return ""
}

// stream writes the reply as the Anthropic SSE event sequence, the way the
// fault proxy's scripted model does, so a streaming agent can be scripted
// too (Bifrost's MCP agent mode does not run on streamed requests).
func stream(w http.ResponseWriter, id, model string, block map[string]any, stop string, usage map[string]any) {
	startUsage := map[string]any{}
	for k, v := range usage {
		startUsage[k] = v
	}
	startUsage["output_tokens"] = 0
	startBlock, delta := map[string]any{"type": "text", "text": ""}, map[string]any{"type": "text_delta", "text": block["text"]}
	if block["type"] == "tool_use" {
		input, err := json.Marshal(block["input"])
		if err != nil {
			input = []byte("{}")
		}
		startBlock = map[string]any{"type": "tool_use", "id": block["id"], "name": block["name"], "input": map[string]any{}}
		delta = map[string]any{"type": "input_json_delta", "partial_json": string(input)}
	}
	events := []map[string]any{
		{"type": "message_start", "message": map[string]any{"id": id, "type": "message", "role": "assistant", "model": model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": startUsage}},
		{"type": "content_block_start", "index": 0, "content_block": startBlock},
		{"type": "content_block_delta", "index": 0, "delta": delta},
		{"type": "content_block_stop", "index": 0},
		{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": usage},
		{"type": "message_stop"},
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	for _, ev := range events {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev["type"], data)
		_ = rc.Flush()
	}
}

const embeddingDim = 64

// embeddings answers an OpenAI embeddings request.
func embeddings(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	var req struct {
		Model string          `json:"model"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, `{"error":{"message":"toolscript: not an embeddings request"}}`, http.StatusBadRequest)
		return
	}
	var inputs []string
	var one string
	if json.Unmarshal(req.Input, &one) == nil {
		inputs = []string{one}
	} else {
		_ = json.Unmarshal(req.Input, &inputs)
	}
	data := make([]any, 0, len(inputs))
	tokens := 0
	for i, in := range inputs {
		data = append(data, map[string]any{"object": "embedding", "index": i, "embedding": bagOfWords(in)})
		tokens += len(strings.Fields(in))
	}
	line, _ := json.Marshal(map[string]any{"embeddings": len(inputs), "model": req.Model})
	log.Print(string(line))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "model": req.Model, "data": data,
		"usage": map[string]any{"prompt_tokens": tokens, "total_tokens": tokens}})
}

// bagOfWords is the deterministic embedding of s.
func bagOfWords(s string) []float64 {
	v := make([]float64, embeddingDim)
	for _, word := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(word))
		v[h.Sum32()%embeddingDim]++
	}
	norm := 0.0
	for _, x := range v {
		norm += x * x
	}
	if norm == 0 {
		v[0], norm = 1, 1
	}
	norm = math.Sqrt(norm)
	for i := range v {
		v[i] /= norm
	}
	return v
}
