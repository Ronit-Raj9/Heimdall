package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

// The exec and attach subresources over WebSocket, protocol
// v5.channel.k8s.io (Kubernetes 1.30 and later): every message starts with
// a stream byte, 0 stdin, 1 stdout, 2 stderr, 3 the final Status, and 255
// closes a stream (v5 only), which is how stdin reaches EOF. v4 has no
// close, so a call that sends stdin needs v5; one without stdin works on v4.
const (
	protoV5 = "v5.channel.k8s.io"
	protoV4 = "v4.channel.k8s.io"

	streamStdin  = 0
	streamStdout = 1
	streamStderr = 2
	streamError  = 3
	streamClose  = 255

	stdinChunk = 32 << 10
)

// StreamOptions are the streams of one exec or attach call. A nil writer
// discards that stream; a nil Stdin sends none.
type StreamOptions struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// ExitError is a command that ran and exited non-zero.
type ExitError struct {
	Code    int
	Message string
}

func (e *ExitError) Error() string { return fmt.Sprintf("command exited %d: %s", e.Code, e.Message) }

// Exec runs argv in a container of a running pod and waits for it. It
// returns the exit code; err is set only when the command could not be run
// or its result could not be read (an exit code above 0 is not an error).
func (c *Client) Exec(ctx context.Context, ns, pod, container string, argv []string, o StreamOptions) (int, error) {
	q := url.Values{}
	for _, a := range argv {
		q.Add("command", a)
	}
	code, err := c.stream(ctx, "exec", ns, pod, container, q, o)
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code, nil
	}
	return code, err
}

// Attach attaches to a running container's main process. The container
// must have stdin (and stdinOnce) set when o.Stdin is given. It returns
// when the process's streams end; the exit code is read from the pod.
func (c *Client) Attach(ctx context.Context, ns, pod, container string, o StreamOptions) error {
	_, err := c.stream(ctx, "attach", ns, pod, container, url.Values{}, o)
	var ee *ExitError
	if errors.As(err, &ee) {
		return nil
	}
	return err
}

// stream runs one exec or attach session to its end.
func (c *Client) stream(ctx context.Context, sub, ns, pod, container string, q url.Values, o StreamOptions) (int, error) {
	q.Set("container", container)
	q.Set("stdout", "true")
	q.Set("stderr", "true")
	if o.Stdin != nil {
		q.Set("stdin", "true")
	}
	path := "/api/v1/namespaces/" + url.PathEscape(ns) + "/pods/" + url.PathEscape(pod) + "/" + sub
	req, err := c.newRequest(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return -1, err
	}
	conn, err := wsDial(c.ws, req, []string{protoV5, protoV4})
	if err != nil {
		return -1, fmt.Errorf("%s %s/%s %s: %w", sub, ns, pod, container, err)
	}
	defer conn.close()
	if o.Stdin != nil && conn.protocol != protoV5 {
		return -1, fmt.Errorf("%s %s/%s: the API server speaks %q, which cannot close stdin; Kubernetes 1.30 or later is needed", sub, ns, pod, conn.protocol)
	}
	stop := context.AfterFunc(ctx, func() { conn.rw.Close() })
	defer stop()
	var wg sync.WaitGroup
	var stdinErr error
	if o.Stdin != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stdinErr = pumpStdin(conn, o.Stdin)
		}()
	}
	code, err := readStreams(conn, o)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	conn.rw.Close()
	wg.Wait()
	if err == nil && stdinErr != nil && !isClosedErr(stdinErr) {
		err = fmt.Errorf("send stdin: %w", stdinErr)
	}
	return code, err
}

// pumpStdin sends r on the stdin stream, then closes the stream.
func pumpStdin(conn *wsConn, r io.Reader) error {
	buf := make([]byte, stdinChunk+1)
	buf[0] = streamStdin
	for {
		n, err := r.Read(buf[1:])
		if n > 0 {
			if werr := conn.writeFrame(opBinary, buf[:n+1]); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) {
			return conn.writeFrame(opBinary, []byte{streamClose, streamStdin})
		}
		if err != nil {
			return err
		}
	}
}

// readStreams copies stdout and stderr until the connection ends and
// returns the exit code from the Status on stream 3. A connection that ends
// without a Status is an error: nobody knows how the command ended.
func readStreams(conn *wsConn, o StreamOptions) (int, error) {
	code := -1
	var status []byte
	var werr error
	write := func(w io.Writer, p []byte) {
		if w != nil && werr == nil && len(p) > 0 {
			_, werr = w.Write(p)
		}
	}
	for {
		msg, err := conn.readMessage()
		if err != nil {
			if errors.Is(err, io.EOF) || isClosedErr(err) {
				break
			}
			return code, err
		}
		if len(msg) == 0 {
			continue
		}
		switch msg[0] {
		case streamStdout:
			write(o.Stdout, msg[1:])
		case streamStderr:
			write(o.Stderr, msg[1:])
		case streamError:
			status = append(status, msg[1:]...)
		}
	}
	if werr != nil {
		return code, fmt.Errorf("write output: %w", werr)
	}
	if len(status) == 0 {
		return code, errors.New("the stream ended without a status")
	}
	return parseExitStatus(status)
}

// parseExitStatus reads the Status of stream 3: Success is exit 0; a
// NonZeroExitCode failure carries the code in details.causes; any other
// failure (the command could not start, say) is an error. It is pure.
func parseExitStatus(raw []byte) (int, error) {
	var st struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Reason  string `json:"reason"`
		Details struct {
			Causes []struct {
				Reason  string `json:"reason"`
				Message string `json:"message"`
			} `json:"causes"`
		} `json:"details"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return -1, fmt.Errorf("parse exec status %q: %w", clip(string(raw), 200), err)
	}
	if st.Status == "Success" {
		return 0, nil
	}
	if st.Reason == "NonZeroExitCode" {
		for _, c := range st.Details.Causes {
			if c.Reason == "ExitCode" {
				n, err := strconv.Atoi(c.Message)
				if err != nil {
					return -1, fmt.Errorf("exec status: exit code %q: %w", c.Message, err)
				}
				return n, &ExitError{Code: n, Message: st.Message}
			}
		}
	}
	return -1, fmt.Errorf("exec failed: %s", strings.TrimSpace(st.Message))
}

func isClosedErr(err error) bool {
	return err != nil && (errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(err.Error(), "use of closed network connection") ||
		strings.Contains(err.Error(), "connection reset by peer") || strings.Contains(err.Error(), "websocket: connection closed"))
}

// ExecOutput runs argv and returns its stdout, capped at limit bytes (the
// rest is drained and dropped), with stderr kept for the error. A non-zero
// exit is an *ExitError carrying stderr.
func (c *Client) ExecOutput(ctx context.Context, ns, pod, container string, argv []string, stdin io.Reader, limit int64) ([]byte, error) {
	var out, errb capBuffer
	out.limit, errb.limit = limit, 64<<10
	code, err := c.Exec(ctx, ns, pod, container, argv, StreamOptions{Stdin: stdin, Stdout: &out, Stderr: &errb})
	if err != nil {
		return out.Bytes(), err
	}
	if code != 0 {
		return out.Bytes(), &ExitError{Code: code, Message: strings.TrimSpace(errb.String())}
	}
	if out.cut {
		return out.Bytes(), fmt.Errorf("output over %d bytes", limit)
	}
	return out.Bytes(), nil
}

// capBuffer keeps at most limit bytes and records whether it cut.
type capBuffer struct {
	b     []byte
	limit int64
	cut   bool
}

func (c *capBuffer) Write(p []byte) (int, error) {
	room := c.limit - int64(len(c.b))
	if int64(len(p)) > room {
		c.b = append(c.b, p[:max(room, 0)]...)
		c.cut = true
		return len(p), nil
	}
	c.b = append(c.b, p...)
	return len(p), nil
}

func (c *capBuffer) Bytes() []byte  { return c.b }
func (c *capBuffer) String() string { return string(c.b) }
