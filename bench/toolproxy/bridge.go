package toolproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// maxStdioLine bounds one JSON-RPC line from a stdio server.
const maxStdioLine = 16 << 20

// Bridge serves an MCP server that only speaks stdio as a streamable HTTP
// endpoint, so it can run as a service and sit behind the tool proxy. It runs
// inside the service's own image (`bifrost-bench toolproxy bridge -- <argv>`).
// Each POST is written to the server's stdin one message per line and
// answered with the matching responses as plain JSON. Requests the server
// sends to the client (roots, sampling, elicitation) are refused with a
// JSON-RPC error, so the server falls back to its command-line setup;
// notifications from the server are dropped.
type Bridge struct {
	path   string
	logger schemas.Logger

	cmd   *exec.Cmd
	stdin io.WriteCloser

	writeMu sync.Mutex // one line at a time on stdin
	mu      sync.Mutex
	waiters map[string]chan json.RawMessage
	exited  chan struct{}
	exitErr error
	readErr error // why the only stdout reader stopped, if it failed
}

// StartBridge starts the stdio server argv and returns a bridge serving its
// MCP endpoint at path. The server is stopped when ctx ends.
func StartBridge(ctx context.Context, argv []string, path string, logger schemas.Logger) (*Bridge, error) {
	if len(argv) == 0 {
		return nil, errors.New("bridge needs the stdio server command")
	}
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	if path == "" {
		path = DefaultMCPPath
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("bridge stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("bridge stdout: %w", err)
	}
	cmd.Stderr = stderrLogger{logger}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", argv[0], err)
	}
	b := &Bridge{path: path, logger: logger, cmd: cmd, stdin: stdin, waiters: map[string]chan json.RawMessage{}, exited: make(chan struct{})}
	go b.readLoop(stdout)
	go func() {
		err := cmd.Wait()
		b.mu.Lock()
		b.exitErr = err
		for k, ch := range b.waiters {
			close(ch)
			delete(b.waiters, k)
		}
		b.mu.Unlock()
		close(b.exited)
	}()
	return b, nil
}

// Exited is closed when the stdio server has exited.
func (b *Bridge) Exited() <-chan struct{} { return b.exited }

// ExitErr is the stdio server's exit error, once it has exited.
func (b *Bridge) ExitErr() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.readErr != nil {
		return b.readErr
	}
	return b.exitErr
}

// ServeHTTP implements http.Handler: GET /healthz, and POST, GET and DELETE
// on the MCP path.
func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		select {
		case <-b.exited:
			http.Error(w, "stdio server exited", http.StatusServiceUnavailable)
		default:
			_, _ = io.WriteString(w, "ok\n")
		}
		return
	}
	if r.URL.Path != b.path {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPost:
		b.servePost(w, r)
	case http.MethodDelete:
		w.WriteHeader(http.StatusOK)
	default:
		// No server-initiated stream: the spec lets a server answer 405.
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// servePost writes every message of the body to the server and answers with
// the responses to the requests among them, in request order.
func (b *Bridge) servePost(w http.ResponseWriter, r *http.Request) {
	body, err := readUpTo(r.Body, maxStdioLine)
	if err != nil {
		http.Error(w, "read request: "+err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	items, batch, ok := parseRPC(body)
	if !ok {
		writeJSON(w, http.StatusBadRequest, rpcError(nil, -32700, "parse error"))
		return
	}
	type wait struct {
		id json.RawMessage
		ch chan json.RawMessage
	}
	var waits []wait
	var early []json.RawMessage
	// Every id this request registered is released on every exit (answer,
	// cancel, write failure), so none stays reserved against later calls.
	defer func() {
		for _, wt := range waits {
			b.unregister(idKey(wt.id), wt.ch)
		}
	}()
	for _, it := range items {
		key := idKey(it.msg.ID)
		if it.msg.Method != "" && key != "" {
			ch, err := b.register(key)
			if err != nil {
				early = append(early, rpcError(it.msg.ID, -32600, err.Error()))
				continue
			}
			waits = append(waits, wait{id: it.msg.ID, ch: ch})
		}
		if err := b.send(it.raw); err != nil {
			http.Error(w, "stdio server: "+err.Error(), http.StatusBadGateway)
			return
		}
	}
	if len(waits) == 0 && len(early) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	out := early
	for _, wt := range waits {
		select {
		case resp, open := <-wt.ch:
			if !open {
				resp = rpcError(wt.id, -32603, "stdio server exited")
			}
			out = append(out, resp)
		case <-r.Context().Done():
			return
		}
	}
	if batch {
		writeJSON(w, http.StatusOK, encodeBatch(out))
		return
	}
	writeJSON(w, http.StatusOK, out[0])
}

// register reserves a request id until its response arrives.
func (b *Bridge) register(key string) (chan json.RawMessage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	select {
	case <-b.exited:
		return nil, errors.New("stdio server exited")
	default:
	}
	if _, busy := b.waiters[key]; busy {
		return nil, fmt.Errorf("request id %s is already in flight", key)
	}
	ch := make(chan json.RawMessage, 1)
	b.waiters[key] = ch
	return ch, nil
}

// unregister releases key when it is still reserved by ch, and leaves a
// later registration of the same id alone.
func (b *Bridge) unregister(key string, ch chan json.RawMessage) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.waiters[key] == ch {
		delete(b.waiters, key)
	}
}

// send writes one message as one line on the server's stdin.
func (b *Bridge) send(raw json.RawMessage) error {
	var line bytes.Buffer
	if err := json.Compact(&line, raw); err != nil {
		line.Reset()
		line.Write(bytes.ReplaceAll(raw, []byte("\n"), []byte(" ")))
	}
	line.WriteByte('\n')
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if _, err := b.stdin.Write(line.Bytes()); err != nil {
		return fmt.Errorf("write stdin: %w", err)
	}
	return nil
}

// readLoop routes every line the server writes: responses to their waiting
// request, requests to an immediate refusal, the rest to the debug log.
func (b *Bridge) readLoop(stdout io.Reader) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64<<10), maxStdioLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m rpcMessage
		if err := json.Unmarshal(line, &m); err != nil {
			b.logger.Debug("stdio server wrote a non-JSON line", "line", clip(line, 200))
			continue
		}
		key := idKey(m.ID)
		switch {
		case m.Method != "" && key != "":
			if err := b.send(rpcError(m.ID, -32601, "the client does not support "+m.Method)); err != nil {
				b.logger.Warn("refuse server request", "method", m.Method, "error", err.Error())
			}
		case m.Method != "":
			b.logger.Debug("stdio server notification dropped", "method", m.Method)
		default:
			b.mu.Lock()
			ch, ok := b.waiters[key]
			delete(b.waiters, key)
			b.mu.Unlock()
			if ok {
				ch <- append(json.RawMessage(nil), line...)
			}
		}
	}
	err := sc.Err()
	if err == nil || errors.Is(err, os.ErrClosed) {
		// stdout closed: normally the server is exiting and Wait reports
		// it. A server that stays up without stdout can never answer, so
		// after a short grace it is stopped like an unreadable one.
		select {
		case <-b.exited:
			return
		case <-time.After(stdoutEOFGrace):
		}
		err = errors.New("stdout closed while the server kept running")
	}
	// Nothing else reads stdout, so no response can arrive any more. Stop
	// the server: the exit path then fails pending and future requests and
	// health turns unhealthy, instead of calls hanging on a live child.
	b.logger.Warn("stdio server output unreadable; stopping it", "error", err.Error())
	b.mu.Lock()
	select {
	case <-b.exited:
	default:
		b.readErr = fmt.Errorf("stdio server output unreadable: %w", err)
	}
	b.mu.Unlock()
	if b.cmd.Process != nil {
		_ = b.cmd.Process.Kill()
	}
}

// stdoutEOFGrace is how long a stdio server may keep running after closing
// its stdout before the bridge stops it.
var stdoutEOFGrace = 2 * time.Second

// ServeBridge runs a bridge's HTTP server until ctx ends or the stdio server
// exits.
func ServeBridge(ctx context.Context, b *Bridge, listen string) error {
	srv := &http.Server{Addr: listen, Handler: b, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	var err error
	select {
	case err = <-errc:
		return fmt.Errorf("bridge on %s: %w", listen, err)
	case <-b.Exited():
		err = fmt.Errorf("stdio server exited: %v", b.ExitErr())
	case <-ctx.Done():
	}
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shut)
	return err
}

// rpcError builds a JSON-RPC error response.
func rpcError(id json.RawMessage, code int, msg string) json.RawMessage {
	if len(bytes.TrimSpace(id)) == 0 {
		id = json.RawMessage("null")
	}
	b, _ := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   any             `json:"error"`
	}{"2.0", id, map[string]any{"code": code, "message": msg}})
	return b
}

// writeJSON writes a JSON body with a status.
func writeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// stderrLogger passes the stdio server's stderr to the logger line by line.
type stderrLogger struct{ l schemas.Logger }

func (s stderrLogger) Write(p []byte) (int, error) {
	for _, line := range bytes.Split(bytes.TrimRight(p, "\n"), []byte("\n")) {
		if len(line) > 0 {
			s.l.Info("stdio server: " + string(line))
		}
	}
	return len(p), nil
}
