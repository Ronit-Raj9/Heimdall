package voiceconnect

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// vapi is the Vapi chat connector: POST /chat with the assistant id and the
// user's text; each later turn names the previous chat (previousChatId), so
// Vapi carries the conversation. The reply is the assistant messages of the
// chat's output.
type vapi struct {
	base
	mu    sync.Mutex
	chats map[string]string // session id -> last chat id
}

type vapiChatRequest struct {
	AssistantID    string `json:"assistantId,omitempty"`
	Input          string `json:"input"`
	PreviousChatID string `json:"previousChatId,omitempty"`
}

type vapiMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Message string          `json:"message"`
}

type vapiChat struct {
	ID     string        `json:"id"`
	Output []vapiMessage `json:"output"`
}

func (v *vapi) Opening(context.Context, string) (string, error) { return "", nil }

func (v *vapi) Converse(ctx context.Context, sessionID, userTurn string) (string, error) {
	if err := checkTurn(v.platform, sessionID, userTurn); err != nil {
		return "", err
	}
	defer v.lock(sessionID)()
	v.mu.Lock()
	prev := v.chats[sessionID]
	v.mu.Unlock()
	req := vapiChatRequest{Input: userTurn}
	if prev != "" {
		// previousChatId carries the assistant; Vapi refuses both together.
		req.PreviousChatID = prev
	} else {
		req.AssistantID = v.agentID
	}
	var out vapiChat
	if err := v.do(ctx, http.MethodPost, "/chat", v.bearer(), req, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", v.err(0, false, "the chat answer has no id")
	}
	v.mu.Lock()
	v.chats[sessionID] = out.ID
	v.mu.Unlock()
	var parts []string
	for _, m := range out.Output {
		if m.Role != "assistant" {
			continue
		}
		if t := contentText(m.Content); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n"), nil
}

func (v *vapi) End(_ context.Context, sessionID string) error {
	v.mu.Lock()
	delete(v.chats, sessionID)
	v.mu.Unlock()
	v.locks.Delete(sessionID)
	return nil
}

// contentText reads an OpenAI-style content: a string or a list of parts
// with text.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var out []string
		for _, p := range parts {
			if p.Text != "" {
				out = append(out, p.Text)
			}
		}
		return strings.TrimSpace(strings.Join(out, ""))
	}
	return ""
}
