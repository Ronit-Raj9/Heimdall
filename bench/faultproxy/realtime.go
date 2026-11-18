package faultproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// X8 voice agents (docs/voice-agents.md): the OpenAI Realtime WebSocket
// (GET /v1/realtime, an Audio pattern of the openai provider) relayed to the
// provider's real upstream, so a speech-to-speech agent runs through the
// proxy like any other call. The proxy swaps the slot placeholder for the
// real key, writes one ledger line per session (kind audio, the model from
// ?model=, status 101 and the session's length as upstream_ms), and a
// model-filtered plan refuses the session with its status (a Realtime
// outage). Frames are relayed byte for byte and never parsed, so a fault
// inside a session is not supported. The scripted model has no Realtime
// session: it answers 501.

// realtimeDialTimeout bounds the upstream dial and handshake.
const realtimeDialTimeout = 15 * time.Second

// webSocketHeaders are the client's handshake headers passed upstream as
// they are; the provider's auth header is replaced, every other header is
// dropped.
var webSocketHeaders = []string{"Sec-WebSocket-Key", "Sec-WebSocket-Version", "Sec-WebSocket-Protocol", "Sec-WebSocket-Extensions", "Openai-Beta", "User-Agent"}

// isWebSocketUpgrade reports whether r asks to switch to WebSocket.
func isWebSocketUpgrade(r *http.Request) bool {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	for _, v := range r.Header.Values("Connection") {
		for _, tok := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), "upgrade") {
				return true
			}
		}
	}
	return false
}

// relayWebSocket opens the session upstream and, when the upstream switches
// protocols, hijacks the client connection and copies bytes both ways until
// either side closes. A refused upstream handshake is passed back as an
// ordinary HTTP answer.
func (s *Server) relayWebSocket(w http.ResponseWriter, r *http.Request, p ProviderSpec, up upstream, e *schemas.LedgerEntry) {
	base, err := url.Parse(up.base)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") {
		e.UpstreamError = "upstream base url is not http or https"
		s.writeError(w, p.Format, http.StatusBadGateway, errUpstream, "fault proxy: the upstream cannot take a WebSocket")
		return
	}
	q := r.URL.Query()
	for _, a := range s.providers.auths {
		if a.Query != "" {
			q.Del(a.Query)
		}
	}
	target := *base
	target.Path = strings.TrimSuffix(base.Path, "/") + r.URL.Path
	target.RawQuery = q.Encode()
	out, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		e.UpstreamError = "build request: " + redactURLError(err)
		s.writeError(w, p.Format, http.StatusBadGateway, errUpstream, "fault proxy could not build the upstream request")
		return
	}
	for _, name := range webSocketHeaders {
		if v := r.Header.Values(name); len(v) > 0 {
			out.Header[http.CanonicalHeaderKey(name)] = v
		}
	}
	out.Header.Set("Upgrade", "websocket")
	out.Header.Set("Connection", "Upgrade")
	if up.key != "" {
		p.Auth.apply(out, up.key)
	}
	upConn, err := dialUpstream(r.Context(), base)
	if err != nil {
		e.UpstreamError = redactURLError(err)
		s.writeError(w, p.Format, http.StatusBadGateway, errUpstream, utils.ProxyUnreachable)
		return
	}
	_ = upConn.SetDeadline(time.Now().Add(realtimeDialTimeout))
	if err := out.Write(upConn); err != nil {
		upConn.Close()
		e.UpstreamError = "write handshake: " + redactURLError(err)
		s.writeError(w, p.Format, http.StatusBadGateway, errUpstream, utils.ProxyUnreachable)
		return
	}
	upReader := bufio.NewReader(upConn)
	resp, err := http.ReadResponse(upReader, out)
	if err != nil {
		upConn.Close()
		e.UpstreamError = "read handshake: " + redactURLError(err)
		s.writeError(w, p.Format, http.StatusBadGateway, errUpstream, utils.ProxyUnreachable)
		return
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer upConn.Close()
		defer resp.Body.Close()
		for name, values := range resp.Header {
			if !droppedResponseHeaders[strings.ToLower(name)] {
				w.Header()[name] = values
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
		return
	}
	_ = upConn.SetDeadline(time.Time{})
	client, clientBuf, err := http.NewResponseController(w).Hijack()
	if err != nil {
		upConn.Close()
		e.UpstreamError = "hijack: " + err.Error()
		s.writeError(w, p.Format, http.StatusInternalServerError, errUpstream, "fault proxy could not take over the connection")
		return
	}
	if sw, ok := w.(*statusWriter); ok {
		sw.code = http.StatusSwitchingProtocols
	}
	_ = client.SetDeadline(time.Time{})
	head := "HTTP/1.1 101 Switching Protocols\r\n"
	for name, values := range resp.Header {
		ln := strings.ToLower(name)
		if ln == "set-cookie" || ln == "openai-organization" || ln == "openai-project" {
			continue
		}
		for _, v := range values {
			head += name + ": " + v + "\r\n"
		}
	}
	if _, err := io.WriteString(client, head+"\r\n"); err != nil {
		client.Close()
		upConn.Close()
		return
	}
	started := time.Now()
	relayBoth(client, clientBuf.Reader, upConn, upReader)
	e.UpstreamMS = float64(time.Since(started).Microseconds()) / 1000
}

// dialUpstream connects to the upstream host, with TLS for https.
func dialUpstream(ctx context.Context, base *url.URL) (net.Conn, error) {
	host := base.Host
	if base.Port() == "" {
		if base.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	d := &net.Dialer{Timeout: realtimeDialTimeout}
	if base.Scheme == "https" {
		return (&tls.Dialer{NetDialer: d, Config: &tls.Config{ServerName: base.Hostname(), MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", host)
	}
	return d.DialContext(ctx, "tcp", host)
}

// relayBoth copies client to upstream and upstream to client, starting with
// whatever each reader already buffered, and closes both when either ends.
func relayBoth(client net.Conn, clientR io.Reader, upstream net.Conn, upstreamR io.Reader) {
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			client.Close()
			upstream.Close()
		})
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, clientR)
		closeBoth()
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(client, upstreamR)
		closeBoth()
	}()
	wg.Wait()
}
