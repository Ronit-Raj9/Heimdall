package main

// `bifrost-bench issuebench work` (lane W05): one issuebench.work job run
// by hand, for debugging and the end to end harness. It uses an own
// worker's dependencies (plain Docker, the public gateway URL). Secrets come
// as one JSON object on stdin, never as flags, so they stay out of the
// process list and the shell history.

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/work"
	"github.com/maximhq/bifrost/bench/secrets"
)

const issuebenchUsage = "usage: bifrost-bench issuebench work -request <request.json> -result <result.json> [-config path] (secrets as a JSON object on stdin)"

func init() {
	register("issuebench", "issues to benchmark: issuebench work -request f -result f (secrets as JSON on stdin)", runIssuebenchCLI)
}

func runIssuebenchCLI(args []string) int {
	if len(args) == 0 || args[0] != issuebench.WorkSubcommand {
		fmt.Fprintln(os.Stderr, issuebenchUsage)
		return exitUsage
	}
	fs := flag.NewFlagSet("issuebench work", flag.ContinueOnError)
	reqPath := fs.String("request", "", "the work request (issuebench-job/request.json)")
	resPath := fs.String("result", "", "where the work result is written")
	config := fs.String("config", defaultConfig, "bench config (for the price snapshot)")
	if err := fs.Parse(args[1:]); err != nil || *reqPath == "" || *resPath == "" || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, issuebenchUsage)
		return exitUsage
	}
	sec, err := readStdinSecrets(os.Stdin)
	if err != nil {
		return fail("issuebench work: %v", err)
	}
	redact := func(s string) string { return secrets.Redact(issuebench.RedactValues(s, sec)) }
	req, err := readWorkRequest(*reqPath)
	if err != nil {
		return fail("issuebench work: %s", redact(err.Error()))
	}
	scratch, err := os.MkdirTemp("", "heimdall-issuebench-*")
	if err != nil {
		return fail("issuebench work: %v", err)
	}
	defer os.RemoveAll(scratch)
	cfg, _ := loadConfig(*config) // no config: the job runs without prices
	x := newOwnIssuebenchExecutor(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	deps := x.deps(scratch, req)
	deps.Log = func(format string, args ...any) { fmt.Fprintln(os.Stderr, redact(fmt.Sprintf(format, args...))) }
	res, runErr := work.Run(ctx, deps, req, sec)
	raw, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fail("issuebench work: encode the result: %v", err)
	}
	if len(raw) > issuebench.MaxWorkResultBytes {
		return fail("issuebench work: the result is %d bytes, over the %d byte cap", len(raw), issuebench.MaxWorkResultBytes)
	}
	if err := os.WriteFile(*resPath, []byte(redact(string(raw))+"\n"), 0o600); err != nil {
		return fail("issuebench work: write the result: %v", err)
	}
	fmt.Printf("result %s\n", *resPath)
	if runErr != nil {
		return fail("issuebench work: %s", redact(runErr.Error()))
	}
	return exitOK
}

// readStdinSecrets reads an optional JSON object of secret names to values
// from stdin. A terminal or an empty stdin is no secrets.
func readStdinSecrets(f *os.File) (map[string]string, error) {
	if f == nil {
		return nil, nil
	}
	if st, err := f.Stat(); err != nil || st.Mode()&os.ModeCharDevice != 0 {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(f, 64<<10+1))
	if err != nil {
		return nil, fmt.Errorf("read secrets from stdin: %w", err)
	}
	if len(data) > 64<<10 {
		return nil, fmt.Errorf("the secrets on stdin are over 64 KiB")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var sec map[string]string
	if err := json.Unmarshal(data, &sec); err != nil {
		return nil, fmt.Errorf("the secrets on stdin must be one JSON object of strings") // never echo the input
	}
	return sec, nil
}
