package main

// Drain mode (Z11, INFRA_I4 item 3a): the server writes its drain token at
// start, the hosted platform router serves /admin/drain, and `bifrost-bench
// drain` turns drain mode on, waits until no lease is in flight or the
// timeout passes, and exits. deploy/hosted/upgrade.sh runs it inside the
// running server container after the new images are built, so the restart
// stops no run.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/drain"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/server"
)

func init() {
	register("drain", "stop new leases on the running server and wait for runs in flight (upgrades)", runDrain)
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		return handlers.NewDrainHandler(env.base.cfg.Server.Mode, true, nil, env.base.logger), nil
	})
}

// writeDrainToken writes the drain token under a's runs root. A server that
// cannot write it still serves; drain then needs a server admin.
func writeDrainToken(a *app) {
	p := drain.TokenPath(a.cfg.Path(a.cfg.RunsRoot))
	if err := drain.WriteToken(p); err != nil {
		a.logger.Warn("drain token not written: drain mode needs a server admin", "error", err.Error())
	}
}

// Exit codes of drain: 0 drained (or the state was changed or shown), 5 the
// timeout passed with leases still in flight, 4 the server could not be
// reached.
const exitDrainTimeout = 5

func runDrain(args []string) int {
	fs := flag.NewFlagSet("drain", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config (finds the server address and the drain token)")
	serverURL := fs.String("server", "", "server base URL (default: from the config's server.listen)")
	tokenFile := fs.String("token-file", "", "drain token file (default: <runs root>/"+drain.TokenFile+")")
	timeout := fs.Duration("timeout", 10*time.Minute, "how long to wait for runs in flight")
	every := fs.Duration("every", 2*time.Second, "poll interval")
	off := fs.Bool("off", false, "turn drain mode off: leases are handed out again")
	status := fs.Bool("status", false, "print the drain state and exit")
	noWait := fs.Bool("no-wait", false, "turn drain mode on and exit without waiting")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	base, tokPath := *serverURL, *tokenFile
	if base == "" || tokPath == "" {
		cfg, err := loadConfig(*config)
		if err != nil {
			return fail("drain: %v", err)
		}
		if base == "" {
			base = localServerURL(cfg.Server.Listen)
		}
		if tokPath == "" {
			tokPath = drain.TokenPath(cfg.Path(cfg.RunsRoot))
		}
	}
	tok, err := drain.ReadToken(tokPath)
	if err != nil {
		return fail("drain: %v (run this on the server host, inside the server container)", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c := drainClient{base: base, token: tok, http: &http.Client{Timeout: 30 * time.Second}}
	switch {
	case *status:
		st, err := c.get(ctx)
		if err != nil {
			return fail("drain: %v", err)
		}
		printDrain(st)
		return exitOK
	case *off:
		st, err := c.set(ctx, false)
		if err != nil {
			return fail("drain: %v", err)
		}
		printDrain(st)
		return exitOK
	}
	st, err := c.set(ctx, true)
	if err != nil {
		return fail("drain: %v", err)
	}
	printDrain(st)
	if *noWait || st.Drained {
		return exitOK
	}
	st, err = c.wait(ctx, *timeout, *every)
	switch {
	case err != nil:
		return fail("drain: %v", err)
	case !st.Drained:
		fmt.Fprintf(os.Stderr, "drain: %d lease(s) still in flight after %s; drain mode stays on\n", st.InFlight, *timeout)
		return exitDrainTimeout
	}
	fmt.Println("drained: no lease in flight; the server can restart without stopping a run")
	return exitOK
}

// localServerURL turns a listen address into a loopback URL: a wildcard
// host (0.0.0.0, ::, empty) is reached on 127.0.0.1.
func localServerURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func printDrain(st drain.Status) {
	state := "off"
	if st.Draining {
		state = "on"
	}
	fmt.Printf("drain mode %s, %d lease(s) in flight\n", state, st.InFlight)
}

type drainClient struct {
	base, token string
	http        *http.Client
}

func (c drainClient) do(ctx context.Context, method string, body any) (drain.Status, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return drain.Status{}, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+handlers.APIPrefix+"/admin/drain", rd)
	if err != nil {
		return drain.Status{}, err
	}
	req.Header.Set(drain.TokenHeader, c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return drain.Status{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return drain.Status{}, fmt.Errorf("%s %s: %d %s", method, req.URL.Path, resp.StatusCode, bytes.TrimSpace(raw))
	}
	var st drain.Status
	if err := json.Unmarshal(raw, &st); err != nil {
		return drain.Status{}, fmt.Errorf("decode drain status: %w", err)
	}
	return st, nil
}

func (c drainClient) get(ctx context.Context) (drain.Status, error) {
	return c.do(ctx, http.MethodGet, nil)
}

func (c drainClient) set(ctx context.Context, on bool) (drain.Status, error) {
	return c.do(ctx, http.MethodPost, handlers.DrainRequest{On: on})
}

// wait polls until no lease is in flight or the timeout passes. A poll that
// fails is retried until the timeout: the answer it needs is the count.
func (c drainClient) wait(ctx context.Context, timeout, every time.Duration) (drain.Status, error) {
	deadline := time.Now().Add(timeout)
	var last drain.Status
	var lastErr error
	shown := -1
	for {
		st, err := c.get(ctx)
		if err == nil {
			last, lastErr = st, nil
			if !st.Draining {
				return st, errors.New("drain mode was turned off while waiting")
			}
			if st.Drained {
				return st, nil
			}
			if st.InFlight != shown {
				fmt.Printf("waiting: %d lease(s) in flight\n", st.InFlight)
				shown = st.InFlight
			}
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			if lastErr != nil && last.InFlight == 0 && !last.Draining {
				return last, lastErr
			}
			return last, nil
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(every):
		}
	}
}
