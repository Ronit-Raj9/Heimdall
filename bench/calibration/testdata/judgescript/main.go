// Command judgescript is a scripted judge model for $0 tests of judged
// grading and judge calibration (docs/judge-calibration.md). It speaks the
// Anthropic Messages API (POST /v1/messages) and the OpenAI Chat Completions
// API (POST /v1/chat/completions), so a throwaway fault proxy can use it as
// its upstream (BENCH_FAULTPROXY_UPSTREAM=http://<host>:<port>) and Bifrost,
// the proxy and the bench judge all run for real while no provider is ever
// called.
//
// The verdict comes from a JSON script, re-read on every request so it can be
// swapped while the server runs:
//
//	{
//	  "default": "reference",                      // reference | correct | incorrect | unparsable | any reply text
//	  "by_prompt_sha256": {"<sha256 of the whole prompt>": "correct"},
//	  "by_answer": {"<graded response, whitespace collapsed>": "incorrect"},
//	  "by_answer_contains": [["Rex", "correct"]]   // first match wins
//	}
//
// Lookup order: by_prompt_sha256, by_answer, by_answer_contains, default.
// "reference" says correct when the graded response contains the reference
// answer (case-insensitive), else incorrect. The graded response is the last
// <<< ... >>> block of the prompt, which is where every shipped judge
// template puts it; the reference is qa-v1's second block or a "Correct
// Answer:" line. "unparsable" replies a sentence the judge cannot read.
// Every request is logged as one JSON line on stderr (prompt hash, answer,
// verdict).
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

type script struct {
	Default          string            `json:"default"`
	ByPromptSHA256   map[string]string `json:"by_prompt_sha256"`
	ByAnswer         map[string]string `json:"by_answer"`
	ByAnswerContains [][2]string       `json:"by_answer_contains"`

	ByImageSHA256 map[string]string `json:"by_image_sha256"` // X1 vision grading: sha256 of the first image's decoded bytes -> reply (images.go)
}

func main() {
	listen := flag.String("listen", "127.0.0.1:9200", "address to listen on")
	path := flag.String("script", "script.json", "verdict script (JSON), re-read on every request")
	flag.Parse()
	h := func(format string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
			var req struct {
				Model    string `json:"model"`
				Messages []struct {
					Content json.RawMessage `json:"content"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(raw, &req); err != nil || len(req.Messages) == 0 {
				http.Error(w, `{"error":{"message":"judgescript: not a chat request"}}`, http.StatusBadRequest)
				return
			}
			prompt := text(req.Messages[len(req.Messages)-1].Content)
			sc, err := load(*path)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusInternalServerError)
				return
			}
			reply, answer, sum := decide(sc, prompt)
			images := imageHashes(req.Messages[len(req.Messages)-1].Content) // X1 vision grading: image blocks (images.go)
			if r, ok := byImage(sc, images); ok {                            // X1 vision grading
				reply = r
			}
			line, _ := json.Marshal(map[string]any{"prompt_sha256": sum, "answer": answer, "reply": reply, "model": req.Model, "images": images})
			log.Print(string(line))
			w.Header().Set("Content-Type", "application/json")
			in, out := len(prompt)/4+1, 1
			if format == "anthropic" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id": "msg_judgescript", "type": "message", "role": "assistant", "model": req.Model,
					"content": []any{map[string]any{"type": "text", "text": reply}}, "stop_reason": "end_turn",
					"usage": map[string]any{"input_tokens": in, "output_tokens": out},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "chatcmpl-judgescript", "object": "chat.completion", "model": req.Model,
				"choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": reply}}},
				"usage":   map[string]any{"prompt_tokens": in, "completion_tokens": out, "total_tokens": in + out},
			})
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/messages", h("anthropic"))
	mux.HandleFunc("POST /v1/chat/completions", h("openai"))
	log.SetFlags(0)
	log.Printf("judgescript listening on %s, script %s", *listen, *path)
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
	return &sc, nil
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

// decide returns the reply, the graded response and the prompt's sha256.
func decide(sc *script, prompt string) (reply, answer, sum string) {
	h := sha256.Sum256([]byte(prompt))
	sum = hex.EncodeToString(h[:])
	blocks := fenced(prompt)
	if len(blocks) > 0 {
		answer = collapse(blocks[len(blocks)-1])
	}
	v, ok := sc.ByPromptSHA256[sum]
	if !ok {
		v, ok = sc.ByAnswer[answer]
	}
	if !ok {
		for _, rule := range sc.ByAnswerContains {
			if strings.Contains(answer, rule[0]) {
				v, ok = rule[1], true
				break
			}
		}
	}
	if !ok {
		v = sc.Default
	}
	switch v {
	case "", "reference":
		ref := reference(prompt, blocks)
		v = "incorrect"
		if ref != "" && strings.Contains(strings.ToLower(answer), strings.ToLower(ref)) {
			v = "correct"
		}
	case "unparsable":
		v = "I cannot decide this one."
	}
	return v, answer, sum
}

// fenced returns the text of every <<< ... >>> block, in order.
func fenced(s string) []string {
	var out []string
	for {
		i := strings.Index(s, "<<<")
		if i < 0 {
			return out
		}
		s = s[i+3:]
		j := strings.Index(s, ">>>")
		if j < 0 {
			return out
		}
		out = append(out, strings.TrimSpace(s[:j]))
		s = s[j+3:]
	}
}

// reference is qa-v1's reference block (the second of three) or the text
// after a "Correct Answer:" line (longmemeval-v1); several reference answers
// joined by "\nor: " count by their first.
func reference(prompt string, blocks []string) string {
	ref := ""
	if len(blocks) == 3 {
		ref = blocks[1]
	}
	for _, l := range strings.Split(prompt, "\n") {
		if r, ok := strings.CutPrefix(strings.TrimSpace(l), "Correct Answer:"); ok {
			ref = r
		}
	}
	ref, _, _ = strings.Cut(ref, "\nor: ")
	return collapse(ref)
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }
