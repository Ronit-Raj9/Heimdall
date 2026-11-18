// Command bifrost-bench runs agent release checks through a Bifrost gateway:
// it plans and runs experiments, grades runs, decides verdicts and serves the
// web UI. Each subcommand lives in its own file; this file only dispatches.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// command is one subcommand.
type command struct {
	summary string
	run     func(args []string) int
}

// commands is the subcommand table. Exit codes: 0 ok, 1-3 verdict codes for
// `check`, 4 infrastructure error, 64 usage error.
var commands = map[string]command{}

const (
	exitOK    = 0
	exitInfra = 4
	exitUsage = 64
)

func register(name, summary string, run func(args []string) int) {
	commands[name] = command{summary: summary, run: run}
}

func main() {
	progName = programName(os.Args[0])                      // Heimdall binary name (progname.go)
	flushStderr := renameUsageStderr(progName)              // Heimdall binary name: usage lines print the started name
	exit := func(code int) { flushStderr(); os.Exit(code) } // Heimdall binary name
	if len(os.Args) < 2 {
		usage()
		exit(exitUsage)
	}
	name := os.Args[1]
	if name == "-h" || name == "--help" || name == "help" {
		usage()
		exit(exitOK)
	}
	cmd, ok := commands[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "%s: unknown command %q\n\n", progName, name)
		usage()
		exit(exitUsage)
	}
	exit(cmd.run(os.Args[2:]))
}

func usage() {
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("usage: " + progName + " <command> [flags]\n\ncommands:\n")
	for _, n := range names {
		fmt.Fprintf(&b, "  %-10s %s\n", n, commands[n].summary)
	}
	fmt.Fprint(os.Stderr, b.String())
}

// fail prints an error and returns the infrastructure exit code.
func fail(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	return exitInfra
}
