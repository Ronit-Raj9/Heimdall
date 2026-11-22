package handlers

// Y03 (G1 P0-7): "Test my endpoint" for an A2A agent. The test fetches the
// agent's card from its card URL and sends one message straight to the
// JSON-RPC endpoint the card names, from the server, with the same address
// rules as the other endpoint tests (public https only on hosted servers).
// A card at protocol 1.0 gets SendMessage (A2A-Version: 1.0); an earlier
// card, or a 1.0 call refused as an unknown method, gets the 0.x
// message/send. A task that is not finished is polled until the test's
// time runs out. Runs still go through the worker's agent gateway; this
// test only proves the agent answers.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// a2aCard is what the test reads from an agent card, 1.0 and 0.x.
type a2aCard struct {
	Name               string `json:"name"`
	URL                string `json:"url"`
	ProtocolVersion    string `json:"protocolVersion"`
	PreferredTransport string `json:"preferredTransport"`
	Interfaces         []struct {
		URL             string `json:"url"`
		ProtocolBinding string `json:"protocolBinding"`
		Transport       string `json:"transport"`
		ProtocolVersion string `json:"protocolVersion"`
	} `json:"supportedInterfaces"`
	Additional []struct {
		URL       string `json:"url"`
		Transport string `json:"transport"`
	} `json:"additionalInterfaces"`
}

// a2aEndpoint picks the card's JSON-RPC URL and protocol version. It is
// pure.
func a2aEndpoint(c *a2aCard) (endpoint, version string) {
	for _, i := range c.Interfaces {
		b := strings.ToUpper(i.ProtocolBinding + i.Transport)
		if strings.Contains(b, "JSONRPC") && i.URL != "" {
			v := i.ProtocolVersion
			if v == "" {
				v = c.ProtocolVersion
			}
			if v == "" {
				v = "1.0"
			}
			return i.URL, v
		}
	}
	if c.URL != "" && (c.PreferredTransport == "" || strings.EqualFold(c.PreferredTransport, "JSONRPC")) {
		return c.URL, c.ProtocolVersion
	}
	for _, i := range c.Additional {
		if strings.EqualFold(i.Transport, "JSONRPC") && i.URL != "" {
			return i.URL, c.ProtocolVersion
		}
	}
	return "", c.ProtocolVersion
}

// a2aLegacy reports whether a protocol version is before 1.0 (an empty one
// counts as 0.x: 1.0 cards always state it).
func a2aLegacy(v string) bool {
	v = strings.TrimSpace(v)
	return v == "" || strings.HasPrefix(v, "0.")
}

func (h *agentTestHandler) runA2A(ctx context.Context, a *schemas.A2AAgent, res *EndpointTestResult, step func(string, string, ...any), fail func(string, ...any) *EndpointTestResult, start time.Time) *EndpointTestResult {
	res.Adapter = "a2a"
	if a == nil || a.CardURL == "" {
		return fail("this A2A agent names an existing gateway registration, not a card URL, so the test cannot reach it directly: use Try one real task on the agent's page")
	}
	res.URL = redactEndpoint(a.CardURL)
	if err := h.checkURL(a.CardURL); err != nil {
		return fail("%v", err)
	}
	wait := endpointTimeout(a.TimeoutS)
	deadline := start.Add(wait)
	client := h.client(h.env.Services.Hosted, wait)
	creq, err := http.NewRequestWithContext(ctx, http.MethodGet, a.CardURL, nil)
	if err != nil {
		return fail("build the card request: %v", err)
	}
	creq.Header.Set("Accept", "application/json")
	step("request", "GET %s (agent card)", res.URL)
	resp, raw, err := endpointDo(client, creq)
	if err != nil {
		return fail("the agent card did not load: %s", etScrubURL(err, a.CardURL))
	}
	step("response", "%d %s, %d bytes", resp.StatusCode, resp.Header.Get("Content-Type"), len(raw))
	if resp.StatusCode != http.StatusOK {
		return fail("the agent card URL answered %d", resp.StatusCode)
	}
	var card a2aCard
	if err := json.Unmarshal(raw, &card); err != nil {
		return fail("the agent card is not JSON: %v", err)
	}
	endpoint, version := a2aEndpoint(&card)
	if endpoint == "" {
		return fail("the agent card names no JSON-RPC endpoint (supportedInterfaces with protocolBinding JSONRPC, or url)")
	}
	if err := h.checkURL(endpoint); err != nil {
		return fail("the card's endpoint %s: %v", redactEndpoint(endpoint), err)
	}
	legacy := a2aLegacy(version)
	step("note", "card %q, protocol %s, endpoint %s", etCap(card.Name, 80), etCap(version, 20), redactEndpoint(endpoint))
	if legacy {
		res.Notes = append(res.Notes, "This agent speaks A2A before 1.0. Heimdall's gateway speaks 1.0; runs fall back to the older message/send call when the agent refuses SendMessage.")
	}
	call := func(method string, params any) (map[string]any, *a2aRPCError, error) {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "heimdall-test-" + randHex(4), "method": method, "params": params})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		if !legacy {
			req.Header.Set("A2A-Version", "1.0")
		}
		step("request", "POST %s %s, %d byte body", redactEndpoint(endpoint), method, len(body))
		resp, raw, err := endpointDo(client, req)
		if err != nil {
			return nil, nil, fmt.Errorf("%s", etScrubURL(err, endpoint))
		}
		res.Status = resp.StatusCode
		step("response", "%d, %d bytes", resp.StatusCode, len(raw))
		if resp.StatusCode != http.StatusOK {
			return nil, nil, fmt.Errorf("%s answered %d", method, resp.StatusCode)
		}
		var doc struct {
			Result map[string]any `json:"result"`
			Error  *a2aRPCError   `json:"error"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, nil, fmt.Errorf("the %s answer is not JSON-RPC", method)
		}
		return doc.Result, doc.Error, nil
	}
	msgID := "heimdall-test-" + randHex(6)
	message := map[string]any{"messageId": msgID, "role": "ROLE_USER", "parts": []map[string]any{{"text": EndpointTestInstruction}}}
	legacyMessage := map[string]any{"kind": "message", "messageId": msgID, "role": "user", "parts": []map[string]any{{"kind": "text", "text": EndpointTestInstruction}}}
	var result map[string]any
	var rpcErr *a2aRPCError
	if !legacy {
		if result, rpcErr, err = call("SendMessage", map[string]any{"message": message}); err != nil {
			return fail("%v", err)
		}
		if rpcErr != nil && rpcErr.Code == -32601 {
			step("note", "SendMessage is unknown to the agent; trying the A2A 0.x message/send")
			legacy, rpcErr = true, nil
			res.Notes = append(res.Notes, "The agent refused the A2A 1.0 SendMessage call and answered the older message/send. Runs use the same fallback.")
		}
	}
	if legacy && result == nil {
		if result, rpcErr, err = call("message/send", map[string]any{"message": legacyMessage}); err != nil {
			return fail("%v", err)
		}
	}
	if rpcErr != nil {
		return fail("the agent returned a JSON-RPC error: %d %s", rpcErr.Code, etCap(rpcErr.Message, 200))
	}
	answer, taskID, state := a2aResult(result)
	for taskID != "" && !a2aDone(state) {
		if h.now().After(deadline) {
			return fail("the agent's task was still %s after %s", state, wait.Round(time.Second))
		}
		select {
		case <-ctx.Done():
			return fail("the test ran out of time")
		case <-time.After(2 * time.Second):
		}
		method := "GetTask"
		if legacy {
			method = "tasks/get"
		}
		r, e, err := call(method, map[string]any{"id": taskID})
		if err != nil {
			return fail("%v", err)
		}
		if e != nil {
			return fail("%s returned a JSON-RPC error: %d %s", method, e.Code, etCap(e.Message, 200))
		}
		if _, ok := r["task"]; !ok { // GetTask and tasks/get answer the task itself
			r = map[string]any{"task": r}
		}
		answer, _, state = a2aResult(r)
	}
	res.LatencyMS = h.now().Sub(start).Milliseconds()
	if state != "" && !strings.HasSuffix(strings.ToUpper(state), "COMPLETED") {
		return fail("the agent's task ended %s", state)
	}
	return finishEndpointTest(res, answer, step)
}

type a2aRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// a2aDone reports whether a task state (1.0 TASK_STATE_* or 0.x lowercase)
// is final for the test: terminal, or stopped for input the test cannot
// give.
func a2aDone(state string) bool {
	s := strings.ToLower(strings.TrimPrefix(strings.ToUpper(state), "TASK_STATE_"))
	switch strings.ReplaceAll(s, "_", "-") {
	case "completed", "failed", "canceled", "rejected", "input-required", "auth-required":
		return true
	}
	return false
}

// a2aResult reads the answer text, task id and state from a SendMessage
// result in the 1.0 shape ({task} or {message}) or the 0.x shape (a task or
// message with "kind"). It is pure.
func a2aResult(r map[string]any) (answer, taskID, state string) {
	if r == nil {
		return "", "", ""
	}
	if k, _ := r["kind"].(string); k == "task" {
		r = map[string]any{"task": r}
	} else if k == "message" {
		r = map[string]any{"message": r}
	}
	if m, ok := r["message"].(map[string]any); ok {
		return a2aText(m["parts"]), "", ""
	}
	t, _ := r["task"].(map[string]any)
	if t == nil {
		return "", "", ""
	}
	taskID, _ = t["id"].(string)
	var texts []string
	if arts, ok := t["artifacts"].([]any); ok {
		for _, a := range arts {
			if am, ok := a.(map[string]any); ok {
				if s := a2aText(am["parts"]); s != "" {
					texts = append(texts, s)
				}
			}
		}
	}
	if st, ok := t["status"].(map[string]any); ok {
		state, _ = st["state"].(string)
		if len(texts) == 0 {
			if m, ok := st["message"].(map[string]any); ok {
				texts = append(texts, a2aText(m["parts"]))
			}
		}
	}
	return strings.Join(texts, "\n"), taskID, state
}

func a2aText(parts any) string {
	ps, _ := parts.([]any)
	var out []string
	for _, p := range ps {
		if pm, ok := p.(map[string]any); ok {
			if s, ok := pm["text"].(string); ok {
				out = append(out, s)
			}
		}
	}
	return strings.Join(out, "\n")
}
