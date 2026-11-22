package voiceconnect

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestCheck(t *testing.T) {
	ok := []*schemas.VoicePlatformAgent{
		{Platform: "vapi", AgentID: "asst_1", APIKeySecret: "VAPI_KEY"},
		{Platform: "retell", AgentID: "agent_9f", APIKeySecret: "RETELL_KEY"},
		{Platform: "elevenlabs", AgentID: "agent_01"},
		{Platform: "elevenlabs", AgentID: "agent_01", URL: "https://api.eu.residency.elevenlabs.io"},
		{Platform: "websocket", URL: "wss://voice.example.com/twilio", Framing: "twilio"},
		{Platform: "websocket", URL: "wss://voice.example.com/ws"},
	}
	for _, s := range ok {
		if err := Check(s); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}
	bad := map[string]*schemas.VoicePlatformAgent{
		"want one of":              {Platform: "livekit"},
		"api_key_secret":           {Platform: "vapi", AgentID: "a"},
		"agent_id":                 {Platform: "retell", APIKeySecret: "K"},
		"needs voice_platform.url": {Platform: "websocket"},
		"wss://":                   {Platform: "websocket", URL: "https://x.example.com"},
		"framing \"pcm\"":          {Platform: "websocket", URL: "wss://x.example.com", Framing: "pcm"},
		"only for a websocket":     {Platform: "vapi", AgentID: "a", APIKeySecret: "K", Framing: "twilio"},
		"not a Settings":           {Platform: "vapi", AgentID: "a", APIKeySecret: "bad-name"},
		"no user info":             {Platform: "elevenlabs", AgentID: "a", URL: "https://u:p@x.example.com"},
	}
	for want, s := range bad {
		if err := Check(s); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: got %v, want %q", s, err, want)
		}
	}
	if tgt, err := AudioTarget(&schemas.VoicePlatformAgent{Platform: "vapi"}); err != nil || tgt != "api.vapi.ai:443" {
		t.Errorf("vapi target %q %v", tgt, err)
	}
	if tgt, _ := AudioTarget(&schemas.VoicePlatformAgent{Platform: "websocket", URL: "wss://v.example.com:8443/x"}); tgt != "v.example.com:8443" {
		t.Errorf("websocket target %q", tgt)
	}
	if SupportsAudio("retell") || !SupportsAudio("vapi") || SupportsText("websocket") {
		t.Error("support matrix")
	}
	if _, err := New(&schemas.VoicePlatformAgent{Platform: "websocket", URL: "wss://v.example.com"}, "", Options{}); err == nil {
		t.Error("websocket must have no text mode")
	}
}

func TestVapiChain(t *testing.T) {
	var mu sync.Mutex
	var got []vapiChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer vk-secret" {
			w.WriteHeader(401)
			w.Write([]byte(`{"message":"Invalid key"}`))
			return
		}
		var req vapiChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		got = append(got, req)
		n := len(got)
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"id": "chat_" + string(rune('0'+n)), "output": []any{
			map[string]any{"role": "tool", "content": "ignored"},
			map[string]any{"role": "assistant", "content": "You said: " + req.Input},
		}})
	}))
	defer srv.Close()
	spec := &schemas.VoicePlatformAgent{Platform: "vapi", AgentID: "asst_1", APIKeySecret: "K", URL: srv.URL}
	c, err := New(spec, "vk-secret", Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if g, _ := c.Opening(ctx, "s1"); g != "" {
		t.Errorf("opening %q", g)
	}
	r1, err := c.Converse(ctx, "s1", "My order is late")
	if err != nil || r1 != "You said: My order is late" {
		t.Fatalf("turn 1: %q %v", r1, err)
	}
	if _, err := c.Converse(ctx, "s1", "Order B-3001"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Converse(ctx, "s2", "Other session"); err != nil {
		t.Fatal(err)
	}
	if got[0].AssistantID != "asst_1" || got[0].PreviousChatID != "" {
		t.Errorf("first turn %+v", got[0])
	}
	if got[1].AssistantID != "" || got[1].PreviousChatID != "chat_1" {
		t.Errorf("second turn must chain on chat_1: %+v", got[1])
	}
	if got[2].AssistantID != "asst_1" || got[2].PreviousChatID != "" {
		t.Errorf("a new session starts fresh: %+v", got[2])
	}
	bad, _ := New(spec, "wrong", Options{})
	_, err = bad.Converse(ctx, "s9", "hi")
	if err == nil || IsInfra(err) || !strings.Contains(err.Error(), "Vapi answered HTTP 401: Invalid key") {
		t.Errorf("401 must be a refusal: %v", err)
	}
	if _, err := c.Converse(ctx, "s1", "  "); err == nil {
		t.Error("empty turn must be refused")
	}
}

func TestRetellChat(t *testing.T) {
	var ended []string
	creates := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer rk" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.URL.Path == "/create-chat":
			creates++
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			if b["agent_id"] != "agent_7" {
				w.WriteHeader(422)
				return
			}
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]any{"chat_id": "chat_x", "agent_id": "agent_7", "chat_status": "ongoing"})
		case r.URL.Path == "/create-chat-completion":
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			if b["chat_id"] != "chat_x" {
				w.WriteHeader(400)
				return
			}
			if b["content"] == "boom" {
				w.WriteHeader(503)
				w.Write([]byte(`{"status":"error","message":"overloaded"}`))
				return
			}
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]any{"messages": []any{
				map[string]any{"role": "tool_call_invocation", "tool_call_id": "t1", "name": "lookup", "arguments": "{}"},
				map[string]any{"role": "tool_call_result", "tool_call_id": "t1", "content": "{}", "successful": true},
				map[string]any{"role": "agent", "content": "Found it."},
				map[string]any{"role": "agent", "content": "Anything else?"},
			}})
		case strings.HasPrefix(r.URL.Path, "/end-chat/") && r.Method == http.MethodPatch:
			ended = append(ended, strings.TrimPrefix(r.URL.Path, "/end-chat/"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, err := New(&schemas.VoicePlatformAgent{Platform: "retell", AgentID: "agent_7", APIKeySecret: "K", URL: srv.URL}, "rk", Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		r, err := c.Converse(ctx, "s1", "where is my order")
		if err != nil || r != "Found it.\nAnything else?" {
			t.Fatalf("turn %d: %q %v", i, r, err)
		}
	}
	if creates != 1 {
		t.Errorf("create-chat called %d times, want once per session", creates)
	}
	_, err = c.Converse(ctx, "s1", "boom")
	if !IsInfra(err) {
		t.Errorf("503 must be infrastructure: %v", err)
	}
	if err := c.End(ctx, "s1"); err != nil || len(ended) != 1 || ended[0] != "chat_x" {
		t.Errorf("end: %v %v", err, ended)
	}
	if err := c.End(ctx, "never"); err != nil {
		t.Errorf("end of an unknown session: %v", err)
	}
}

// fakeEleven is an ElevenLabs conversation server: it checks the text_only
// override, greets, pings once and answers each user_message.
type fakeEleven struct {
	t        *testing.T
	greeting string
	textOnly bool
	pongs    int
	mu       sync.Mutex
}

func serverConn(t *testing.T, w http.ResponseWriter, r *http.Request) (*wsConn, bool) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "upgrade required", 426)
		return nil, false
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		t.Fatal("no hijacker")
	}
	nc, rw, err := hj.Hijack()
	if err != nil {
		t.Fatal(err)
	}
	accept := wsAccept(r.Header.Get("Sec-WebSocket-Key"))
	rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + accept + "\r\n\r\n")
	rw.Flush()
	return &wsConn{rw: serverSide{nc}, br: bufio.NewReader(nc)}, true
}

// serverSide writes unmasked frames, as a server must.
type serverSide struct{ net.Conn }

func (f *fakeEleven) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("agent_id") != "agent_01" && r.URL.Query().Get("token") != "signed" {
		http.Error(w, "unknown agent", 404)
		return
	}
	c, ok := serverConn(f.t, w, r)
	if !ok {
		return
	}
	send := func(v any) {
		raw, _ := json.Marshal(v)
		c.rw.Write(encodeFrame(opText, raw, false))
	}
	defer c.rw.Close()
	for {
		_, msg, err := c.readMessage()
		if err != nil {
			return
		}
		var ev map[string]any
		json.Unmarshal(msg, &ev)
		switch ev["type"] {
		case "conversation_initiation_client_data":
			conv, _ := ev["conversation_config_override"].(map[string]any)["conversation"].(map[string]any)
			f.mu.Lock()
			f.textOnly = conv["text_only"] == true
			f.mu.Unlock()
			send(map[string]any{"type": "conversation_initiation_metadata", "conversation_initiation_metadata_event": map[string]any{"conversation_id": "conv_1", "agent_output_audio_format": "pcm_16000", "user_input_audio_format": "pcm_16000"}})
			send(map[string]any{"type": "ping", "ping_event": map[string]any{"event_id": 7, "ping_ms": 0}})
			if f.greeting != "" {
				send(map[string]any{"type": "agent_response", "agent_response_event": map[string]any{"agent_response": f.greeting, "event_id": 1}})
			}
		case "pong":
			f.mu.Lock()
			if ev["event_id"] == float64(7) {
				f.pongs++
			}
			f.mu.Unlock()
		case "user_message":
			text, _ := ev["text"].(string)
			if text == "hang up" {
				return
			}
			send(map[string]any{"type": "user_transcript", "user_transcription_event": map[string]any{"user_transcript": text}})
			send(map[string]any{"type": "agent_response", "agent_response_event": map[string]any{"agent_response": "Echo: " + text, "event_id": 2}})
			send(map[string]any{"type": "agent_response", "agent_response_event": map[string]any{"agent_response": "Done.", "event_id": 3}})
		}
	}
}

func TestElevenLabsTextMode(t *testing.T) {
	f := &fakeEleven{t: t, greeting: "Hi, this is Acme support."}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/convai/conversation", f.handle)
	var srvURL string
	mux.HandleFunc("/v1/convai/conversation/get-signed-url", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("xi-api-key") != "xi-secret" || r.URL.Query().Get("agent_id") != "agent_01" {
			w.WriteHeader(401)
			w.Write([]byte(`{"detail":{"status":"invalid_api_key"}}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"signed_url": strings.Replace(srvURL, "http://", "ws://", 1) + "/v1/convai/conversation?agent_id=agent_01&token=signed"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL
	ctx := context.Background()
	for _, key := range []string{"", "xi-secret"} {
		c, err := New(&schemas.VoicePlatformAgent{Platform: "elevenlabs", AgentID: "agent_01", URL: srv.URL}, key, Options{GreetingWait: time.Second, Settle: 100 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		g, err := c.Opening(ctx, "s1")
		if err != nil || g != "Hi, this is Acme support." {
			t.Fatalf("key %q opening %q %v", key, g, err)
		}
		r, err := c.Converse(ctx, "s1", "My grinder is cracked")
		if err != nil || r != "Echo: My grinder is cracked\nDone." {
			t.Fatalf("key %q reply %q %v", key, r, err)
		}
		r, err = c.Converse(ctx, "s1", "Refund please")
		if err != nil || r != "Echo: Refund please\nDone." {
			t.Fatalf("second reply %q %v", r, err)
		}
		_, err = c.Converse(ctx, "s1", "hang up")
		if !IsInfra(err) {
			t.Errorf("a closed conversation is infrastructure: %v", err)
		}
		c.End(ctx, "s1")
	}
	f.mu.Lock()
	if !f.textOnly || f.pongs < 2 {
		t.Errorf("text_only %v pongs %d", f.textOnly, f.pongs)
	}
	f.mu.Unlock()
	bad, _ := New(&schemas.VoicePlatformAgent{Platform: "elevenlabs", AgentID: "agent_01", URL: srv.URL}, "wrong", Options{})
	if _, err := bad.Converse(ctx, "s1", "hi"); err == nil || IsInfra(err) || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "wrong") {
		t.Errorf("bad key: %v", err)
	}
}

func TestScrub(t *testing.T) {
	if s := scrub("dial wss://x/?token=abc123 Bearer sk-live-1"); strings.Contains(s, "abc123") || strings.Contains(s, "sk-live-1") {
		t.Error(s)
	}
}
