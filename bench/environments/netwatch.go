package environments

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// NetWatchFile is the run file the network observer writes: one JSON line
// per distinct NetWatchRecord. Only `agent check` turns the observer on.
const NetWatchFile = "netwatch.jsonl"

// Network observer limits.
const (
	netWatchReady   = 30 * time.Second
	netWatchStop    = 10 * time.Second
	netWatchMaxFile = 4 << 20
)

// NetWatchRecord kinds.
const (
	NetWatchReady     = "ready"
	NetWatchDNS       = "dns"        // a DNS query the agent sent
	NetWatchDNSAnswer = "dns_answer" // the A records answered for a name
	NetWatchSYN       = "syn"        // a TCP connection attempt
	NetWatchUDP       = "udp"        // a UDP datagram that is not DNS
	NetWatchHTTP      = "http"       // a plain HTTP request: request line and header names
	NetWatchError     = "error"      // the observer failed or could not start
)

// netwatchScript is the observer, run with python3 from the helper image.
//
//go:embed netwatch.py
var netwatchScript string

// NetWatchRecord is one line of netwatch.jsonl.
type NetWatchRecord struct {
	T       string   `json:"t"`
	Name    string   `json:"name,omitempty"`
	QType   int      `json:"qtype,omitempty"`
	IPs     []string `json:"ips,omitempty"`
	Dst     string   `json:"dst,omitempty"`
	Port    int      `json:"port,omitempty"`
	Method  string   `json:"method,omitempty"`
	Path    string   `json:"path,omitempty"`
	Host    string   `json:"host,omitempty"`
	Headers []string `json:"headers,omitempty"`
	Session string   `json:"session,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// ReadNetWatch reads a netwatch.jsonl file. A line that does not decode is
// skipped; a missing file is an error the caller reports as "not observed".
func ReadNetWatch(path string) ([]NetWatchRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	var out []NetWatchRecord
	for _, line := range bytes.Split(b, []byte("\n")) {
		var r NetWatchRecord
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &r) != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// missingExecRe matches the docker error for an entrypoint the image does
// not have.
var missingExecRe = regexp.MustCompile(`exec: "([^"]+)": executable file not found`)

// missingExecutable returns the program a docker error says is not in the
// image ("exec: \"python3\": executable file not found in $PATH"), or "".
// The observer runs python3 and its holder runs sleep from the helper image,
// so a toolchain image set as the helper fails here first.
func missingExecutable(msg string) string {
	if m := missingExecRe.FindStringSubmatch(msg); m != nil {
		return m[1]
	}
	return ""
}

// netWatch is one running observer: a holder container that owns the
// network namespace on the evaluation network, and the observer process that
// shares it. The agent joins the holder's namespace, so the observer sees
// every packet the agent sends from its first one.
type netWatch struct {
	d       utils.Docker
	logger  schemas.Logger
	holder  string
	watcher string
	cmd     *exec.Cmd
	out     *os.File
	done    chan struct{}
	stderr  bytes.Buffer
	once    sync.Once
}

// startNetWatch starts the holder on network and the observer in its
// namespace, and returns once the observer has opened its socket. The holder
// is created, joined to every network in extra (the run's service and fault
// networks) and only then started: under gVisor (runsc) a container never
// sees a network joined after it started. Every container is labelled with
// the run, so Destroy removes leftovers.
func (e *dockerWorkspace) startNetWatch(ctx context.Context, rc *schemas.RunContext, expID, runID, network string, extra []string) (*netWatch, error) {
	image := e.helperImage(rc)
	w := &netWatch{d: e.cfg.Docker, logger: e.cfg.Logger, holder: helperName(runID, "net"), watcher: helperName(runID, "watch"), done: make(chan struct{})}
	e.removeContainers(ctx, w.holder, w.watcher)
	labels := utils.LabelArgs(expID, runID)
	hold := append([]string{"create", "--name", w.holder}, labels...)
	hold = append(hold, "--network", network, "--user", "65534:65534", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges", "--read-only", "--pids-limit", "16", "--memory", "64m",
		"--log-driver", "none", "--entrypoint", "sleep", image, "infinity")
	// gVisor: the agent shares the holder's /etc/hosts (container: network),
	// so the holder takes the names on all its networks at create time
	// (gvisorhosts.go).
	hold = e.sandboxHosts(ctx, hold, network, extra...)
	if _, err := e.cfg.Docker.MustRun(ctx, hold...); err != nil {
		w.remove(ctx)
		return nil, fmt.Errorf("start network holder: %w", err)
	}
	for _, n := range extra {
		if _, err := e.cfg.Docker.MustRun(ctx, "network", "connect", n, w.holder); err != nil {
			w.remove(ctx)
			return nil, fmt.Errorf("connect network holder to %s: %w", n, err)
		}
	}
	if _, err := e.cfg.Docker.MustRun(ctx, "start", w.holder); err != nil {
		w.remove(ctx)
		if tool := missingExecutable(err.Error()); tool != "" {
			return nil, fmt.Errorf("start network holder: %s", utils.HelperLacks(image, tool))
		}
		return nil, fmt.Errorf("start network holder: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(rc.Dir, NetWatchFile), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		w.remove(ctx)
		return nil, fmt.Errorf("open %s: %w", NetWatchFile, err)
	}
	w.out = f
	watch := append([]string{"run", "--name", w.watcher}, labels...)
	watch = append(watch, "--network", "container:"+w.holder, "--user", "0", "--cap-drop", "ALL", "--cap-add", "NET_RAW",
		"--security-opt", "no-new-privileges", "--read-only", "--pids-limit", "16", "--memory", "128m",
		"--entrypoint", "python3", image, "-u", "-c", netwatchScript)
	w.cmd = e.cfg.Docker.Command(context.Background(), watch...)
	w.cmd.Stderr = &w.stderr
	stdout, err := w.cmd.StdoutPipe()
	if err != nil {
		w.close(ctx)
		return nil, fmt.Errorf("observer stdout: %w", err)
	}
	if err := w.cmd.Start(); err != nil {
		w.close(ctx)
		return nil, fmt.Errorf("start observer: %w", err)
	}
	ready := make(chan struct{})
	go w.copy(stdout, ready)
	select {
	case <-ready:
		return w, nil
	case <-w.done:
		msg := strings.TrimSpace(w.stderr.String())
		if tool := missingExecutable(msg); tool != "" {
			msg = utils.HelperLacks(image, tool)
		}
		err = fmt.Errorf("observer exited before it was ready: %s", msg)
	case <-time.After(netWatchReady):
		err = fmt.Errorf("observer not ready after %s", netWatchReady)
	case <-ctx.Done():
		err = ctx.Err()
	}
	w.close(ctx)
	return nil, err
}

// copy writes the observer's lines after the ready line to the run file,
// capped, and closes ready on the ready line and done when stdout ends.
func (w *netWatch) copy(r io.Reader, ready chan<- struct{}) {
	defer close(w.done)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	isReady, written := false, 0
	for sc.Scan() {
		line := sc.Bytes()
		if !isReady {
			var rec NetWatchRecord
			if json.Unmarshal(line, &rec) == nil && rec.T == NetWatchReady {
				isReady = true
				close(ready)
			}
			continue
		}
		if written+len(line)+1 > netWatchMaxFile {
			continue
		}
		if _, err := w.out.Write(append(append([]byte(nil), line...), '\n')); err != nil {
			w.logger.Warn("write network observer line", "error", err.Error())
			return
		}
		written += len(line) + 1
	}
}

// close stops the observer (SIGTERM, then removal), waits for its output to
// drain into the run file and removes both containers. It is safe to call
// more than once.
func (w *netWatch) close(ctx context.Context) {
	w.once.Do(func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), netWatchStop+killGrace)
		defer cancel()
		if w.cmd != nil && w.cmd.Process != nil {
			if _, err := w.d.Run(cctx, "kill", "--signal", "TERM", w.watcher); err != nil {
				w.logger.Warn("stop network observer", "container", w.watcher, "error", err.Error())
			}
			select {
			case <-w.done:
			case <-time.After(netWatchStop):
				w.logger.Warn("network observer did not stop; removing it", "container", w.watcher)
			}
			w.remove(cctx)
			if err := w.cmd.Wait(); err != nil {
				w.logger.Debug("network observer ended", "container", w.watcher, "error", err.Error())
			}
		} else {
			w.remove(cctx)
		}
		if w.out != nil {
			if err := errors.Join(w.out.Sync(), w.out.Close()); err != nil {
				w.logger.Warn("close network observer file", "error", err.Error())
			}
		}
	})
}

// remove force-removes the observer and the holder.
func (w *netWatch) remove(ctx context.Context) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), killGrace)
	defer cancel()
	if _, err := w.d.Run(cctx, "rm", "-f", w.watcher, w.holder); err != nil {
		w.logger.Warn("remove network observer", "error", err.Error())
	}
}

// writeNetWatchError records why the observer could not run, so the check
// reports "not observed" with the reason instead of "no traffic".
func writeNetWatchError(dir string, cause error) error {
	b, err := json.Marshal(NetWatchRecord{T: NetWatchError, Error: cause.Error()})
	if err != nil {
		return fmt.Errorf("encode observer error: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, NetWatchFile), append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", NetWatchFile, err)
	}
	return nil
}
