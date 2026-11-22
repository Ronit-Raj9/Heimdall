package voiceconnect

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// elevenLabs is the ElevenLabs Agents connector in text mode: one
// conversation WebSocket per session, opened with the text_only override.
// A private agent is reached through a signed URL (GET
// /v1/convai/conversation/get-signed-url with xi-api-key); a public agent by
// its id. Each user turn is a user_message; the reply is the agent_response
// events that follow it. Pings are answered with pong.
type elevenLabs struct {
	base
	mu    sync.Mutex
	convs map[string]*elConv
}

type elConv struct {
	ws       *wsConn
	replies  chan string
	done     chan struct{}
	err      error // set before done is closed
	greeting string
	id       string
}

type elEvent struct {
	Type     string `json:"type"`
	Metadata *struct {
		ConversationID string `json:"conversation_id"`
	} `json:"conversation_initiation_metadata_event"`
	AgentResponse *struct {
		AgentResponse string `json:"agent_response"`
	} `json:"agent_response_event"`
	Ping *struct {
		EventID int `json:"event_id"`
	} `json:"ping_event"`
}

func (e *elevenLabs) socketURL(ctx context.Context) (string, error) {
	if e.key != "" {
		var out struct {
			SignedURL string `json:"signed_url"`
		}
		h := http.Header{}
		h.Set("xi-api-key", e.key)
		if err := e.do(ctx, http.MethodGet, "/v1/convai/conversation/get-signed-url?agent_id="+url.QueryEscape(e.agentID), h, nil, &out); err != nil {
			return "", err
		}
		if !strings.HasPrefix(out.SignedURL, "wss://") && !strings.HasPrefix(out.SignedURL, "ws://") {
			return "", e.err(0, false, "get-signed-url answered without a signed_url")
		}
		return out.SignedURL, nil
	}
	ws := strings.Replace(strings.Replace(e.api, "https://", "wss://", 1), "http://", "ws://", 1)
	return ws + "/v1/convai/conversation?agent_id=" + url.QueryEscape(e.agentID), nil
}

// open starts the session's conversation and waits for its metadata and
// the agent's first message (up to GreetingWait).
func (e *elevenLabs) open(ctx context.Context, sessionID string) (*elConv, error) {
	e.mu.Lock()
	c := e.convs[sessionID]
	e.mu.Unlock()
	if c != nil {
		return c, nil
	}
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	u, err := e.socketURL(ctx)
	if err != nil {
		return nil, err
	}
	ws, err := wsDial(ctx, e.hc, u, nil)
	if err != nil {
		if pe, ok := err.(*Error); ok {
			pe.Platform = e.platform
		}
		return nil, err
	}
	c = &elConv{ws: ws, replies: make(chan string, 64), done: make(chan struct{})}
	init := map[string]any{"type": "conversation_initiation_client_data",
		"conversation_config_override": map[string]any{"conversation": map[string]any{"text_only": true}},
		"dynamic_variables":            map[string]any{}, "user_id": sessionID}
	raw, _ := json.Marshal(init)
	if err := ws.writeText(string(raw)); err != nil {
		ws.close()
		return nil, e.err(0, true, "sending the conversation start: "+scrub(err.Error()))
	}
	ready := make(chan string, 1)
	go e.read(c, ready)
	select {
	case id := <-ready:
		c.id = id
	case <-c.done:
		return nil, c.err
	case <-ctx.Done():
		ws.close()
		return nil, e.err(0, true, "no conversation_initiation_metadata before the timeout")
	}
	select {
	case g := <-c.replies:
		c.greeting = g + e.settle(c)
	case <-time.After(e.opt.GreetingWait):
	case <-c.done:
		return nil, c.err
	}
	e.mu.Lock()
	e.convs[sessionID] = c
	e.mu.Unlock()
	return c, nil
}

// read pumps the socket: metadata to ready, agent replies to c.replies,
// pings answered.
func (e *elevenLabs) read(c *elConv, ready chan<- string) {
	defer close(c.done)
	for {
		op, msg, err := c.ws.readMessage()
		if err != nil {
			if err == io.EOF {
				c.err = e.err(0, true, "the conversation was closed by ElevenLabs")
			} else {
				c.err = e.err(0, true, "conversation socket: "+scrub(err.Error()))
			}
			return
		}
		if op != opText {
			continue
		}
		var ev elEvent
		if json.Unmarshal(msg, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "conversation_initiation_metadata":
			id := ""
			if ev.Metadata != nil {
				id = ev.Metadata.ConversationID
			}
			select {
			case ready <- id:
			default:
			}
		case "agent_response":
			if ev.AgentResponse != nil && strings.TrimSpace(ev.AgentResponse.AgentResponse) != "" {
				c.replies <- strings.TrimSpace(ev.AgentResponse.AgentResponse)
			}
		case "ping":
			id := 0
			if ev.Ping != nil {
				id = ev.Ping.EventID
			}
			raw, _ := json.Marshal(map[string]any{"type": "pong", "event_id": id})
			_ = c.ws.writeText(string(raw))
		}
	}
}

// settle collects further replies of the same turn that arrive within
// Settle of each other, joined by newlines.
func (e *elevenLabs) settle(c *elConv) string {
	var more strings.Builder
	for {
		select {
		case r := <-c.replies:
			more.WriteString("\n" + r)
		case <-time.After(e.opt.Settle):
			return more.String()
		}
	}
}

func (e *elevenLabs) Opening(ctx context.Context, sessionID string) (string, error) {
	if sessionID == "" {
		return "", e.err(0, false, "a turn needs a session id")
	}
	defer e.lock(sessionID)()
	c, err := e.open(ctx, sessionID)
	if err != nil {
		return "", err
	}
	g := c.greeting
	c.greeting = ""
	return g, nil
}

func (e *elevenLabs) Converse(ctx context.Context, sessionID, userTurn string) (string, error) {
	if err := checkTurn(e.platform, sessionID, userTurn); err != nil {
		return "", err
	}
	defer e.lock(sessionID)()
	c, err := e.open(ctx, sessionID)
	if err != nil {
		return "", err
	}
	for drained := false; !drained; { // replies of an earlier turn that came late
		select {
		case <-c.replies:
		default:
			drained = true
		}
	}
	raw, _ := json.Marshal(map[string]string{"type": "user_message", "text": userTurn})
	if err := c.ws.writeText(string(raw)); err != nil {
		e.drop(sessionID)
		return "", e.err(0, true, "sending the user turn: "+scrub(err.Error()))
	}
	t := time.NewTimer(e.timeout)
	defer t.Stop()
	select {
	case r := <-c.replies:
		return r + e.settle(c), nil
	case <-c.done:
		e.drop(sessionID)
		return "", c.err
	case <-t.C:
		return "", e.err(0, true, "no agent_response within the turn timeout")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (e *elevenLabs) drop(sessionID string) {
	e.mu.Lock()
	c := e.convs[sessionID]
	delete(e.convs, sessionID)
	e.mu.Unlock()
	if c != nil {
		c.ws.close()
	}
}

func (e *elevenLabs) End(_ context.Context, sessionID string) error {
	e.drop(sessionID)
	e.locks.Delete(sessionID)
	return nil
}
