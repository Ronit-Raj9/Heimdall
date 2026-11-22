package voiceconnect

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// A minimal RFC 6455 client: the opening handshake through net/http (a 101
// response's body is the raw connection), masked frames out, ping answered,
// fragments joined, the close handshake. Text messages only are used by the
// platforms here; no extensions. k8s/ws.go holds the same client for the
// Kubernetes exec stream; this copy adds wss through any http.Client.

const (
	wsGUID       = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	opCont       = 0x0
	opText       = 0x1
	opBinary     = 0x2
	opClose      = 0x8
	opPing       = 0x9
	opPong       = 0xA
	wsMaxMessage = 8 << 20
)

type wsConn struct {
	rw     io.ReadWriteCloser
	br     *bufio.Reader
	wmu    sync.Mutex
	closed bool
}

func wsAccept(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// upgradeClient is an http.Client that never negotiates HTTP/2 (which cannot
// upgrade) on base's dialer.
func upgradeClient(base *http.Client) *http.Client {
	tr := &http.Transport{ForceAttemptHTTP2: false, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if base != nil {
		if t, ok := base.Transport.(*http.Transport); ok {
			tr = t.Clone()
			tr.ForceAttemptHTTP2 = false
			tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
			if tr.TLSClientConfig != nil {
				tr.TLSClientConfig.NextProtos = nil
			}
		}
	}
	return &http.Client{Transport: tr}
}

// wsDial opens a WebSocket to a ws:// or wss:// URL with extra headers.
func wsDial(ctx context.Context, hc *http.Client, rawURL string, header http.Header) (*wsConn, error) {
	u := rawURL
	switch {
	case strings.HasPrefix(u, "wss://"):
		u = "https://" + strings.TrimPrefix(u, "wss://")
	case strings.HasPrefix(u, "ws://"):
		u = "http://" + strings.TrimPrefix(u, "ws://")
	default:
		return nil, fmt.Errorf("websocket: want a ws:// or wss:// URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(b[:])
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", key)
	resp, err := upgradeClient(hc).Do(req)
	if err != nil {
		return nil, &Error{Infra: true, Msg: "connect: " + scrub(err.Error())}
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, statusError(resp.StatusCode, body)
	}
	rw, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		resp.Body.Close()
		return nil, errors.New("websocket: the HTTP client did not hand over the connection")
	}
	if resp.Header.Get("Sec-WebSocket-Accept") != wsAccept(key) {
		rw.Close()
		return nil, errors.New("websocket: wrong Sec-WebSocket-Accept")
	}
	return &wsConn{rw: rw, br: bufio.NewReaderSize(rw, 64<<10)}, nil
}

func (c *wsConn) writeFrame(op byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return errors.New("websocket: connection closed")
	}
	_, err := c.rw.Write(encodeFrame(op, payload, true))
	return err
}

func (c *wsConn) writeText(s string) error { return c.writeFrame(opText, []byte(s)) }

// encodeFrame builds one final frame, masked with a fresh key when masked.
func encodeFrame(op byte, payload []byte, masked bool) []byte {
	n := len(payload)
	hdr := []byte{0x80 | op}
	mb := byte(0)
	if masked {
		mb = 0x80
	}
	switch {
	case n < 126:
		hdr = append(hdr, mb|byte(n))
	case n <= 0xFFFF:
		hdr = append(hdr, mb|126, 0, 0)
		binary.BigEndian.PutUint16(hdr[2:], uint16(n))
	default:
		hdr = append(hdr, mb|127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(hdr[2:], uint64(n))
	}
	out := append([]byte{}, hdr...)
	if !masked {
		return append(out, payload...)
	}
	var mask [4]byte
	_, _ = rand.Read(mask[:])
	out = append(out, mask[:]...)
	for i, b := range payload {
		out = append(out, b^mask[i%4])
	}
	return out
}

// readMessage returns the next data message, answering pings; io.EOF on close.
func (c *wsConn) readMessage() (byte, []byte, error) {
	var msg []byte
	var first byte
	in := false
	for {
		fin, op, payload, err := readFrame(c.br)
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case opPing:
			if err := c.writeFrame(opPong, payload); err != nil {
				return 0, nil, err
			}
			continue
		case opPong:
			continue
		case opClose:
			_ = c.writeFrame(opClose, nil)
			return 0, nil, io.EOF
		case opText, opBinary:
			if in {
				return 0, nil, errors.New("websocket: new message inside a fragmented one")
			}
			msg, first, in = payload, op, true
		case opCont:
			if !in {
				return 0, nil, errors.New("websocket: continuation without a message")
			}
			msg = append(msg, payload...)
		default:
			return 0, nil, fmt.Errorf("websocket: unknown opcode %d", op)
		}
		if len(msg) > wsMaxMessage {
			return 0, nil, fmt.Errorf("websocket: message over %d bytes", wsMaxMessage)
		}
		if fin {
			return first, msg, nil
		}
	}
}

func readFrame(r io.Reader) (bool, byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return false, 0, nil, err
	}
	fin, op := h[0]&0x80 != 0, h[0]&0x0F
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
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range p {
			p[i] ^= mask[i%4]
		}
	}
	return fin, op, p, nil
}

func (c *wsConn) close() error {
	_ = c.writeFrame(opClose, []byte{0x03, 0xE8})
	c.wmu.Lock()
	c.closed = true
	c.wmu.Unlock()
	return c.rw.Close()
}
