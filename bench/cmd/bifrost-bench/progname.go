package main

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Heimdall binary name: the same program builds as `bifrost-bench` and as
// `heimdall` (make heimdall). Usage text prints the name it was started as;
// `bifrost-bench` stays valid everywhere.

// progName is the name the binary was started as: "heimdall" when the file
// is called heimdall, else "bifrost-bench".
var progName = "bifrost-bench"

// programName maps argv[0] to the name usage text prints. Any file name
// starting with "heimdall" (heimdall, heimdall.exe, heimdall-dev) is
// heimdall; everything else, including a `go run` temp binary, is
// bifrost-bench.
func programName(argv0 string) string {
	if strings.HasPrefix(strings.ToLower(filepath.Base(argv0)), "heimdall") {
		return "heimdall"
	}
	return "bifrost-bench"
}

// usageLineRE matches the program name where usage text names it: after
// "usage: " or at the start of an indented continuation line.
var usageLineRE = regexp.MustCompile(`(?m)^(usage: |[ \t]+)bifrost-bench `)

// renameUsage rewrites usage lines that name bifrost-bench to name prog.
// Image names (bench/bifrost-bench:dev) and paths are left alone: only a
// line-leading "usage: bifrost-bench " or an indented "bifrost-bench " is
// rewritten.
func renameUsage(text, prog string) string {
	if prog == "bifrost-bench" {
		return text
	}
	return usageLineRE.ReplaceAllString(text, "${1}"+prog+" ")
}

// renameUsageStderr makes every usage line written to os.Stderr name prog,
// so the subcommands' usage constants need no edit. It returns the function
// main calls before exiting, which flushes what is still in the pipe. Under
// the bifrost-bench name it changes nothing.
func renameUsageStderr(prog string) func() {
	if prog == "bifrost-bench" {
		return func() {}
	}
	r, w, err := os.Pipe()
	if err != nil {
		return func() {}
	}
	real := os.Stderr
	os.Stderr = w
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64<<10)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				_, _ = io.WriteString(real, renameUsage(string(buf[:n]), prog))
			}
			if err != nil {
				return
			}
		}
	}()
	return func() {
		os.Stderr = real
		_ = w.Close()
		<-done
	}
}
