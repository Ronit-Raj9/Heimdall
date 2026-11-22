package voiceconnect

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// retell is the Retell chat connector: POST /create-chat once per session
// (the chat agent id), then POST /create-chat-completion per user turn; the
// reply is the agent messages the completion generated. End calls PATCH
// /end-chat/{chat_id}.
type retell struct {
	base
	mu    sync.Mutex
	chats map[string]string // session id -> chat id
}

type retellChat struct {
	ChatID     string `json:"chat_id"`
	ChatStatus string `json:"chat_status"`
}

type retellCompletion struct {
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

func (r *retell) Opening(context.Context, string) (string, error) { return "", nil }

func (r *retell) chat(ctx context.Context, sessionID string) (string, error) {
	r.mu.Lock()
	id := r.chats[sessionID]
	r.mu.Unlock()
	if id != "" {
		return id, nil
	}
	var out retellChat
	body := map[string]any{"agent_id": r.agentID, "metadata": map[string]string{"heimdall_session": sessionID}}
	if err := r.do(ctx, http.MethodPost, "/create-chat", r.bearer(), body, &out); err != nil {
		return "", err
	}
	if out.ChatID == "" {
		return "", r.err(0, false, "create-chat answered without a chat_id")
	}
	r.mu.Lock()
	r.chats[sessionID] = out.ChatID
	r.mu.Unlock()
	return out.ChatID, nil
}

func (r *retell) Converse(ctx context.Context, sessionID, userTurn string) (string, error) {
	if err := checkTurn(r.platform, sessionID, userTurn); err != nil {
		return "", err
	}
	defer r.lock(sessionID)()
	id, err := r.chat(ctx, sessionID)
	if err != nil {
		return "", err
	}
	var out retellCompletion
	if err := r.do(ctx, http.MethodPost, "/create-chat-completion", r.bearer(), map[string]string{"chat_id": id, "content": userTurn}, &out); err != nil {
		return "", err
	}
	var parts []string
	for _, m := range out.Messages {
		if m.Role == "agent" && strings.TrimSpace(m.Content) != "" {
			parts = append(parts, strings.TrimSpace(m.Content))
		}
	}
	return strings.Join(parts, "\n"), nil
}

func (r *retell) End(ctx context.Context, sessionID string) error {
	r.mu.Lock()
	id := r.chats[sessionID]
	delete(r.chats, sessionID)
	r.mu.Unlock()
	r.locks.Delete(sessionID)
	if id == "" {
		return nil
	}
	return r.do(ctx, http.MethodPatch, "/end-chat/"+url.PathEscape(id), r.bearer(), nil, nil)
}
