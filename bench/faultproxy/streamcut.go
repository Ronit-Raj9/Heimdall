package faultproxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
)

// This file is the stream_cut fault (Safety probes area, S1): a quiet model
// fault. The proxy relays a streamed answer to the matching conversation
// request event by event until the first event that carries a tool call's
// arguments, flushes that event, and then cuts the connection: no stop event,
// no [DONE], no final chunk. The agent gets half a tool call with status
// 200. A stream with no tool call passes whole, and a request that does not
// stream is never touched.

// errStreamCut stops the copy of an answer once the stream is cut.
var errStreamCut = errors.New("stream cut by the stream_cut fault")

// streamCutter wraps the client's writer: it passes complete server-sent
// events through and stops after the first tool-call delta.
type streamCutter struct {
	w      http.ResponseWriter
	format string
	buf    bytes.Buffer
	cut    bool // the tool-call delta went out and the stream was cut
}

// newStreamCutter returns a cutter writing to w for a provider format.
func newStreamCutter(w http.ResponseWriter, format string) *streamCutter {
	return &streamCutter{w: w, format: format}
}

// Header implements http.ResponseWriter.
func (c *streamCutter) Header() http.Header { return c.w.Header() }

// WriteHeader implements http.ResponseWriter.
func (c *streamCutter) WriteHeader(code int) { c.w.WriteHeader(code) }

// Write implements http.ResponseWriter. It buffers until an event is
// complete, writes it, and returns errStreamCut after writing the event
// that carries the first tool-call delta.
func (c *streamCutter) Write(p []byte) (int, error) {
	if c.cut {
		return 0, errStreamCut
	}
	c.buf.Write(p)
	for {
		ev, ok := nextEvent(&c.buf)
		if !ok {
			return len(p), nil
		}
		if _, err := c.w.Write(ev); err != nil {
			return 0, err
		}
		if toolDelta(c.format, ev) {
			_ = http.NewResponseController(c.w).Flush()
			c.cut = true
			return len(p), errStreamCut
		}
	}
}

// Flush implements http.Flusher.
func (c *streamCutter) Flush() {
	_ = http.NewResponseController(c.w).Flush()
}

// Unwrap lets http.ResponseController reach the client's writer.
func (c *streamCutter) Unwrap() http.ResponseWriter { return c.w }

// finish writes whatever is buffered when the stream ended without a cut.
func (c *streamCutter) finish() {
	if !c.cut && c.buf.Len() > 0 {
		_, _ = c.w.Write(c.buf.Bytes())
		c.buf.Reset()
	}
}

// nextEvent takes one complete event (ending in a blank line) off buf.
func nextEvent(buf *bytes.Buffer) ([]byte, bool) {
	b := buf.Bytes()
	i, n := bytes.Index(b, []byte("\n\n")), 2
	if j := bytes.Index(b, []byte("\r\n\r\n")); j >= 0 && (i < 0 || j < i) {
		i, n = j, 4
	}
	if i < 0 {
		return nil, false
	}
	ev := append([]byte(nil), b[:i+n]...)
	buf.Next(i + n)
	return ev, true
}

// toolDelta reports whether one event carries a tool call's arguments:
// Anthropic input_json_delta, OpenAI chat delta.tool_calls or a Responses
// function_call_arguments delta, Gemini functionCall.
func toolDelta(format string, ev []byte) bool {
	for _, line := range bytes.Split(ev, []byte("\n")) {
		data, ok := bytes.CutPrefix(bytes.TrimRight(line, "\r"), []byte("data:"))
		if !ok {
			continue
		}
		data = bytes.TrimSpace(data)
		if len(data) == 0 || data[0] != '{' {
			continue
		}
		if format == FormatGemini {
			return bytes.Contains(data, []byte(`"functionCall"`))
		}
		var m struct {
			Type    string          `json:"type"`
			Delta   json.RawMessage `json:"delta"`
			Choices []struct {
				Delta struct {
					ToolCalls []json.RawMessage `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Type {
		case "response.function_call_arguments.delta":
			return true
		case "content_block_delta":
			var d struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(m.Delta, &d) == nil && d.Type == "input_json_delta" {
				return true
			}
		}
		for _, ch := range m.Choices {
			if len(ch.Delta.ToolCalls) > 0 {
				return true
			}
		}
	}
	return false
}
