package k8s

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// A minimal RFC 6455 WebSocket client, only what the Kubernetes exec and
// attach subresources need: the client side of the opening handshake through
// net/http (a 101 response's body is the raw connection), masked binary
// frames out, unmasked frames in, ping answered with pong, fragmented
// messages joined, and the close handshake. No extensions, no compression.

// wsGUID is the fixed key suffix of RFC 6455 section 1.3.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// WebSocket opcodes (RFC 6455 section 5.2).
const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

// wsMaxMessage caps one incoming message. Kubernetes sends stream data in
// frames of at most 32 KiB, so this only guards against a broken peer.
const wsMaxMessage = 16 << 20

// wsConn is one client WebSocket connection.
type wsConn struct {
	rw       io.ReadWriteCloser
	br       *bufio.Reader
	wmu      sync.Mutex
	protocol string // the subprotocol the server picked
	closed   bool
}

// wsKey returns a fresh Sec-WebSocket-Key and the Sec-WebSocket-Accept the
// server must answer with.
func wsKey() (string, string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", err
	}
	key := base64.StdEncoding.EncodeToString(b[:])
	return key, wsAccept(key), nil
}

// wsAccept is the Sec-WebSocket-Accept value for key. It is pure.
func wsAccept(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// wsDial upgrades req (a GET with an http or https URL) to a WebSocket that
// speaks one of protocols. The client must not negotiate HTTP/2, which
// cannot upgrade; Client.wsHTTP is built that way.
func wsDial(hc *http.Client, req *http.Request, protocols []string) (*wsConn, error) {
	key, accept, err := wsKey()
	if err != nil {
		return nil, fmt.Errorf("websocket key: %w", err)
	}
	// No Accept header: the upgrade negotiates by Sec-WebSocket-Protocol,
	// and Kubernetes 1.37 refuses an exec or attach upgrade that asks for
	// application/json with 406 Not Acceptable.
	req.Header.Del("Accept")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", key)
	for _, p := range protocols {
		req.Header.Add("Sec-WebSocket-Protocol", p)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		return nil, readAPIError(resp)
	}
	rw, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		resp.Body.Close()
		return nil, errors.New("websocket: the HTTP client did not hand over the connection")
	}
	if resp.Header.Get("Sec-WebSocket-Accept") != accept {
		rw.Close()
		return nil, errors.New("websocket: the server answered with a wrong Sec-WebSocket-Accept")
	}
	return &wsConn{rw: rw, br: bufio.NewReaderSize(rw, 64<<10), protocol: resp.Header.Get("Sec-WebSocket-Protocol")}, nil
}

// writeFrame sends one masked, unfragmented frame. Safe for concurrent use.
func (c *wsConn) writeFrame(op byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return errors.New("websocket: connection closed")
	}
	frame, err := encodeFrame(op, payload, true)
	if err != nil {
		return err
	}
	_, err = c.rw.Write(frame)
	return err
}

// encodeFrame builds one final frame. masked frames get a fresh random key,
// as RFC 6455 requires of every client frame. It is pure apart from the key.
func encodeFrame(op byte, payload []byte, masked bool) ([]byte, error) {
	n := len(payload)
	hdr := []byte{0x80 | op}
	maskBit := byte(0)
	if masked {
		maskBit = 0x80
	}
	switch {
	case n < 126:
		hdr = append(hdr, maskBit|byte(n))
	case n <= 0xFFFF:
		hdr = append(hdr, maskBit|126, 0, 0)
		binary.BigEndian.PutUint16(hdr[2:], uint16(n))
	default:
		hdr = append(hdr, maskBit|127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(hdr[2:], uint64(n))
	}
	out := make([]byte, 0, len(hdr)+4+n)
	out = append(out, hdr...)
	if !masked {
		return append(out, payload...), nil
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return nil, err
	}
	out = append(out, mask[:]...)
	for i, b := range payload {
		out = append(out, b^mask[i%4])
	}
	return out, nil
}

// readMessage returns the next data message (text or binary), joining
// fragments, answering pings and returning io.EOF on a close frame.
func (c *wsConn) readMessage() ([]byte, error) {
	var msg []byte
	inMessage := false
	for {
		fin, op, payload, err := readFrame(c.br)
		if err != nil {
			return nil, err
		}
		switch op {
		case opPing:
			if err := c.writeFrame(opPong, payload); err != nil {
				return nil, err
			}
			continue
		case opPong:
			continue
		case opClose:
			_ = c.writeFrame(opClose, closePayload(payload))
			return nil, io.EOF
		case opText, opBinary:
			if inMessage {
				return nil, errors.New("websocket: new message inside a fragmented one")
			}
			msg, inMessage = payload, true
		case opContinuation:
			if !inMessage {
				return nil, errors.New("websocket: continuation without a message")
			}
			msg = append(msg, payload...)
		default:
			return nil, fmt.Errorf("websocket: unknown opcode %d", op)
		}
		if len(msg) > wsMaxMessage {
			return nil, fmt.Errorf("websocket: message over %d bytes", wsMaxMessage)
		}
		if fin {
			return msg, nil
		}
	}
}

// closePayload echoes the status code of a received close frame.
func closePayload(p []byte) []byte {
	if len(p) >= 2 {
		return p[:2]
	}
	return nil
}

// readFrame reads one frame. A masked frame from the server is accepted and
// unmasked, although servers do not send them.
func readFrame(r io.Reader) (bool, byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return false, 0, nil, err
	}
	fin, op := h[0]&0x80 != 0, h[0]&0x0F
	if h[0]&0x70 != 0 {
		return false, 0, nil, errors.New("websocket: reserved bits set; no extension was negotiated")
	}
	masked := h[1]&0x80 != 0
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return false, 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return false, 0, nil, err
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > wsMaxMessage {
		return false, 0, nil, fmt.Errorf("websocket: frame of %d bytes is over the cap", n)
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return false, 0, nil, err
		}
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return fin, op, payload, nil
}

// close sends a normal close frame and closes the connection.
func (c *wsConn) close() error {
	_ = c.writeFrame(opClose, []byte{0x03, 0xE8})
	c.wmu.Lock()
	c.closed = true
	c.wmu.Unlock()
	return c.rw.Close()
}
