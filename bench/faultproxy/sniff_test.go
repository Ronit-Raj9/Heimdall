package faultproxy

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestSnifferSSE(t *testing.T) {
	stream := sseFirst + sseRest
	cases := []struct {
		name  string
		chunk int
	}{
		{"one write", len(stream)},
		{"byte at a time", 1},
		{"odd chunks", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sn := newSniffer(FormatAnthropic, true)
			for i := 0; i < len(stream); i += tc.chunk {
				end := min(i+tc.chunk, len(stream))
				if n, err := sn.Write([]byte(stream[i:end])); err != nil || n != end-i {
					t.Fatalf("Write = %d, %v", n, err)
				}
			}
			id, tok := sn.result()
			want := schemas.Tokens{Fresh: 11, CacheRead: 5, CacheWrite: 7, Out: 42}
			if id != "msg_up123" || tok == nil || *tok != want {
				t.Fatalf("id %q tokens %+v", id, tok)
			}
		})
	}
}

func TestSnifferSkipsHugeLines(t *testing.T) {
	sn := newSniffer(FormatAnthropic, true)
	huge := "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"" + strings.Repeat("x", maxSSELine+10) + "\"}}\n\n"
	for _, part := range []string{huge, sseFirst} {
		if _, err := sn.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if id, _ := sn.result(); id != "msg_up123" {
		t.Fatalf("id after huge line = %q", id)
	}
}

func TestSnifferJSON(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantID   string
		wantToks bool
	}{
		{"message", upstreamJSON, "msg_json9", true},
		{"truncated message", upstreamJSON[:40], "msg_json9", false},
		{"error envelope", `{"type":"error","error":{"type":"overloaded_error","message":"x"}}`, "", false},
		{"count tokens", `{"input_tokens":5}`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sn := newSniffer(FormatAnthropic, false)
			if _, err := sn.Write([]byte(tc.body)); err != nil {
				t.Fatal(err)
			}
			id, tok := sn.result()
			if id != tc.wantID || (tok != nil) != tc.wantToks {
				t.Fatalf("id %q tokens %+v", id, tok)
			}
		})
	}
}

// OpenAI and Gemini bodies and streams as the real APIs send them.
const (
	openAIChatJSON   = `{"id":"chatcmpl-up1","object":"chat.completion","model":"gpt-5-mini","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":64},"completion_tokens_details":{"reasoning_tokens":12}}}`
	openAIChatStream = "data: {\"id\":\"chatcmpl-up2\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\n\n" +
		"data: {\"id\":\"chatcmpl-up2\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}],\"usage\":null}\n\n" +
		"data: {\"id\":\"chatcmpl-up2\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":50,\"completion_tokens\":5,\"prompt_tokens_details\":{\"cached_tokens\":0}}}\n\n" +
		"data: [DONE]\n\n"
	openAIRespJSON   = `{"id":"resp_up3","object":"response","status":"completed","output":[],"usage":{"input_tokens":200,"input_tokens_details":{"cached_tokens":128},"output_tokens":40,"output_tokens_details":{"reasoning_tokens":20}}}`
	openAIRespStream = "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_up4\",\"object\":\"response\",\"status\":\"in_progress\",\"usage\":null}}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"delta\":\"hi\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_up4\",\"object\":\"response\",\"status\":\"completed\",\"usage\":{\"input_tokens\":9,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":3}}}\n\n"
	geminiStream = "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"h\"}]}}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":1},\"responseId\":\"gm-up5\"}\r\n\r\n" +
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"i\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":4,\"cachedContentTokenCount\":6,\"thoughtsTokenCount\":2},\"responseId\":\"gm-up5\"}\r\n\r\n"
)

func TestSnifferFormats(t *testing.T) {
	cases := []struct {
		name, format, body string
		sse                bool
		wantID             string
		want               *schemas.Tokens
	}{
		{"openai chat json", FormatOpenAI, openAIChatJSON, false, "chatcmpl-up1", &schemas.Tokens{Fresh: 36, CacheRead: 64, Out: 30}},
		{"openai chat stream", FormatOpenAI, openAIChatStream, true, "chatcmpl-up2", &schemas.Tokens{Fresh: 50, Out: 5}},
		{"openai responses json", FormatOpenAI, openAIRespJSON, false, "resp_up3", &schemas.Tokens{Fresh: 72, CacheRead: 128, Out: 40}},
		{"openai responses stream", FormatOpenAI, openAIRespStream, true, "resp_up4", &schemas.Tokens{Fresh: 9, Out: 3}},
		{"openai truncated", FormatOpenAI, openAIChatJSON[:30], false, "chatcmpl-up1", nil},
		{"openai error", FormatOpenAI, `{"error":{"message":"x","type":"server_error","code":null}}`, false, "", nil},
		{"openai input tokens", FormatOpenAI, `{"object":"response.input_tokens","input_tokens":5}`, false, "", nil},
		{"gemini stream", FormatGemini, geminiStream, true, "gm-up5", &schemas.Tokens{Fresh: 4, CacheRead: 6, Out: 6}},
		{"gemini json", FormatGemini, `{"candidates":[],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2},"responseId":"gm-6"}`, false, "gm-6", &schemas.Tokens{Fresh: 3, Out: 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, chunk := range []int{len(tc.body), 1, 5} {
				sn := newSniffer(tc.format, tc.sse)
				for i := 0; i < len(tc.body); i += chunk {
					if _, err := sn.Write([]byte(tc.body[i:min(i+chunk, len(tc.body))])); err != nil {
						t.Fatal(err)
					}
				}
				id, tok := sn.result()
				if id != tc.wantID || (tok == nil) != (tc.want == nil) || (tok != nil && *tok != *tc.want) {
					t.Fatalf("chunk %d: id %q tokens %+v, want %q %+v", chunk, id, tok, tc.wantID, tc.want)
				}
			}
		})
	}
}
