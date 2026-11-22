package remotemap

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strings"
)

// Streamed answers: server-sent events (text/event-stream, most agent
// frameworks) and the AWS event stream (Bedrock InvokeAgent). Each event's
// text is picked with the stream path and the pieces are joined in order.

// MaxStreamEvents caps the events read from one streamed answer.
const MaxStreamEvents = 20000

// StreamReader turns one stream event's payload into text.
type StreamReader struct {
	Path   *Path  // nil: a JSON string event, or the raw text of a non-JSON event
	Decode string // "base64" decodes each picked piece
}

// piece is the text one event payload contributes.
func (r StreamReader) piece(payload []byte) (string, error) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return "", nil
	}
	doc, err := Decode(payload)
	var text string
	switch {
	case err != nil && r.Path == nil:
		text = string(payload)
	case err != nil:
		return "", nil // not JSON: a keep-alive or a comment the path cannot read
	case r.Path == nil:
		if s, ok := doc.(string); ok {
			text = s
		}
	default:
		text = Text(r.Path.Eval(doc), "")
	}
	if r.Decode == "base64" && text != "" {
		b, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return "", fmt.Errorf("stream piece is not base64: %v", err)
		}
		text = string(b)
	}
	return text, nil
}

// ReadSSE reads a text/event-stream body to its end (or a "[DONE]" event)
// and returns the joined text and the number of events.
func ReadSSE(body io.Reader, r StreamReader) (string, int, error) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var out strings.Builder
	var data []string
	events := 0
	flush := func() (bool, error) {
		if len(data) == 0 {
			return false, nil
		}
		payload := strings.Join(data, "\n")
		data = data[:0]
		events++
		if events > MaxStreamEvents {
			return true, fmt.Errorf("more than %d stream events", MaxStreamEvents)
		}
		if strings.TrimSpace(payload) == "[DONE]" {
			return true, nil
		}
		p, err := r.piece([]byte(payload))
		if err != nil {
			return true, err
		}
		out.WriteString(p)
		return false, nil
	}
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			if done, err := flush(); done || err != nil {
				return out.String(), events, err
			}
			continue
		}
		if v, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(v, " "))
		}
	}
	if err := sc.Err(); err != nil {
		return out.String(), events, fmt.Errorf("read the event stream: %v", err)
	}
	_, err := flush()
	return out.String(), events, err
}

// ReadAWSEventStream reads an application/vnd.amazon.eventstream body.
// An exception message fails with its text.
func ReadAWSEventStream(body io.Reader, r StreamReader) (string, int, error) {
	var out strings.Builder
	events := 0
	prelude := make([]byte, 12)
	for {
		if _, err := io.ReadFull(body, prelude); err != nil {
			if errors.Is(err, io.EOF) {
				return out.String(), events, nil
			}
			return out.String(), events, fmt.Errorf("read the event stream: %v", err)
		}
		total := binary.BigEndian.Uint32(prelude[0:4])
		hlen := binary.BigEndian.Uint32(prelude[4:8])
		if crc32.ChecksumIEEE(prelude[:8]) != binary.BigEndian.Uint32(prelude[8:12]) {
			return out.String(), events, errors.New("event stream prelude checksum mismatch")
		}
		if total < 16 || total > 16<<20 || hlen > total-16 {
			return out.String(), events, fmt.Errorf("event stream message of %d bytes is malformed", total)
		}
		rest := make([]byte, total-12)
		if _, err := io.ReadFull(body, rest); err != nil {
			return out.String(), events, fmt.Errorf("read the event stream: %v", err)
		}
		msg := append(append([]byte{}, prelude...), rest[:len(rest)-4]...)
		if crc32.ChecksumIEEE(msg) != binary.BigEndian.Uint32(rest[len(rest)-4:]) {
			return out.String(), events, errors.New("event stream message checksum mismatch")
		}
		headers, err := eventHeaders(rest[:hlen])
		if err != nil {
			return out.String(), events, err
		}
		payload := rest[hlen : len(rest)-4]
		events++
		if events > MaxStreamEvents {
			return out.String(), events, fmt.Errorf("more than %d stream events", MaxStreamEvents)
		}
		if mt := headers[":message-type"]; mt == "exception" || mt == "error" {
			msg := headers[":exception-type"] + headers[":error-code"]
			var e struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(payload, &e) == nil && e.Message != "" {
				msg += ": " + e.Message
			}
			return out.String(), events, fmt.Errorf("the agent's stream reported %s", strings.TrimPrefix(msg, ": "))
		}
		p, err := r.piece(payload)
		if err != nil {
			return out.String(), events, err
		}
		out.WriteString(p)
	}
}

// eventHeaders decodes the string headers of one event stream message;
// other header types are skipped.
func eventHeaders(b []byte) (map[string]string, error) {
	out := map[string]string{}
	bad := errors.New("event stream headers are malformed")
	for len(b) > 0 {
		n := int(b[0])
		if len(b) < 2+n {
			return nil, bad
		}
		name := string(b[1 : 1+n])
		typ := b[1+n]
		b = b[2+n:]
		size := map[byte]int{0: 0, 1: 0, 2: 1, 3: 2, 4: 4, 5: 8, 8: 8, 9: 16}
		switch typ {
		case 6, 7:
			if len(b) < 2 {
				return nil, bad
			}
			l := int(binary.BigEndian.Uint16(b))
			if len(b) < 2+l {
				return nil, bad
			}
			if typ == 7 {
				out[name] = string(b[2 : 2+l])
			}
			b = b[2+l:]
		default:
			s, ok := size[typ]
			if !ok || len(b) < s {
				return nil, bad
			}
			b = b[s:]
		}
	}
	return out, nil
}

// EncodeAWSEvent builds one event stream message (tests and fixtures).
func EncodeAWSEvent(headers map[string]string, payload []byte) []byte {
	var h bytes.Buffer
	for _, k := range sortedKeys(headers) {
		h.WriteByte(byte(len(k)))
		h.WriteString(k)
		h.WriteByte(7)
		_ = binary.Write(&h, binary.BigEndian, uint16(len(headers[k])))
		h.WriteString(headers[k])
	}
	total := uint32(12 + h.Len() + len(payload) + 4)
	var m bytes.Buffer
	_ = binary.Write(&m, binary.BigEndian, total)
	_ = binary.Write(&m, binary.BigEndian, uint32(h.Len()))
	_ = binary.Write(&m, binary.BigEndian, crc32.ChecksumIEEE(m.Bytes()[:8]))
	m.Write(h.Bytes())
	m.Write(payload)
	_ = binary.Write(&m, binary.BigEndian, crc32.ChecksumIEEE(m.Bytes()))
	return m.Bytes()
}
