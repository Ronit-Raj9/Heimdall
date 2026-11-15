package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

func init() {
	suiteSubcommands["canary"] = runSuiteCanary
	suiteSubcommands["canary-check"] = runSuiteCanaryCheck
}

const suiteCanaryUsage = `usage: bifrost-bench suite canary [-config deploy/bench.yaml | -root benchmarks] [-stamp] <suite id>
       bifrost-bench suite canary-check [-config deploy/bench.yaml | -root benchmarks] [-json] -text <file>|-

suite canary sets a contamination canary GUID in suite.yaml when it has none.
-stamp also writes the canary line into every task instruction file; that
changes instruction hashes, so run suite lock and suite admit afterwards.
canary-check exits 0 when the text holds no canary, 1 when it holds one,
2 on a usage error and 4 when the text or the suites cannot be read.`

// Exit codes of `suite canary-check`. They follow grep: 0 nothing found, 1
// found, 2 usage; an unreadable input or benchmarks folder is exitInfra.
const (
	canaryCheckFound = 1
	canaryCheckUsage = 2
)

// maxCanaryCheckBytes caps how much text canary-check reads.
const maxCanaryCheckBytes = 64 << 20

// runSuiteCanary is `suite canary`: it keeps or sets the suite's canary in
// suite.yaml and, with -stamp, writes the canary line into every task
// instruction file. Stamping is opt-in because it changes the instruction
// hashes the suite lock and every admission rest on.
func runSuiteCanary(args []string) int {
	fs := flag.NewFlagSet("suite canary", flag.ContinueOnError)
	root := fs.String("root", "", "benchmarks folder (default: benchmarks_root of the config, else benchmarks)")
	config := fs.String("config", defaultConfig, "bench config")
	stamp := fs.Bool("stamp", false, "also write the canary line into every task instruction file (changes instruction hashes: re-lock and re-admit)")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, suiteCanaryUsage)
		return exitUsage
	}
	benchRoot, _, err := benchmarksRoot(fs, *root, *config)
	if err != nil {
		return fail("%v", err)
	}
	s, err := loadSuite(context.Background(), benchRoot, pos[0])
	if err != nil {
		return fail("load suite %s: %v", pos[0], err)
	}
	guid, created, err := suite.EnsureCanary(s.Root)
	if err != nil {
		return fail("suite canary: %v", err)
	}
	how := "kept from"
	if created {
		how = "written to"
	}
	fmt.Printf("suite %s: canary %s (%s %s)\n", s.ID, guid, how, filepath.Join(s.Root, suite.SuiteFile))
	if !*stamp {
		fmt.Println("instruction files were not stamped; pass -stamp to write the canary line into them (that changes instruction hashes: suite lock and suite admit are needed after)")
		return exitOK
	}
	res, err := suite.StampCanary(s, guid)
	changed := 0
	for _, r := range res {
		state := "unchanged"
		if r.Changed {
			state = "stamped  "
			changed++
		}
		fmt.Printf("%s %s %s\n", state, r.Task, r.Path)
	}
	if err != nil {
		return fail("suite canary -stamp: %v", err)
	}
	if changed == 0 {
		fmt.Printf("all %d instruction files already carry canary %s; nothing changed\n", len(res), guid)
		return exitOK
	}
	fmt.Fprintf(os.Stderr, `
WARNING: %d instruction file(s) changed. Their hashes changed, so suite.lock
no longer matches the suite and every stamped task must be admitted again
before it can be planned. Run, in order:

    bifrost-bench suite lock %s
    bifrost-bench suite admit %s

Never stamp a committed suite casually: existing locks, manifests and
leaderboard entries refer to the unstamped instructions.
`, changed, s.ID, s.ID)
	return exitOK
}

// canaryCheckReport is `suite canary-check -json`.
type canaryCheckReport struct {
	Found  bool              `json:"found"`
	Known  int               `json:"known"` // canaries of suites under the benchmarks root
	Hits   []utils.CanaryHit `json:"hits"`
	Source string            `json:"source"`
}

// runSuiteCanaryCheck is `suite canary-check`: it reports whether a text (a
// model's output, a web page, a corpus shard) holds the canary of any suite
// under the benchmarks root, or a canary line with a GUID no local suite
// owns. A hit means the text copied stamped instruction text verbatim; no
// hit proves nothing.
func runSuiteCanaryCheck(args []string) int {
	fs := flag.NewFlagSet("suite canary-check", flag.ContinueOnError)
	root := fs.String("root", "", "benchmarks folder (default: benchmarks_root of the config, else benchmarks)")
	config := fs.String("config", defaultConfig, "bench config")
	text := fs.String("text", "", "file to check, or - for stdin")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 || *text == "" {
		fmt.Fprintln(os.Stderr, suiteCanaryUsage)
		return canaryCheckUsage
	}
	benchRoot, _, err := benchmarksRoot(fs, *root, *config)
	if err != nil {
		return fail("%v", err)
	}
	known, err := utils.SuiteCanaries(benchRoot)
	if err != nil {
		return fail("canary-check: %v", err)
	}
	data, err := readCheckText(*text)
	if err != nil {
		return fail("canary-check: %v", err)
	}
	rep := canaryCheckReport{Known: len(known), Hits: utils.FindCanaries(string(data), known), Source: *text}
	rep.Found = len(rep.Hits) > 0
	if *asJSON {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return fail("canary-check: %v", err)
		}
		fmt.Println(string(b))
	} else {
		printCanaryHits(rep)
	}
	if rep.Found {
		return canaryCheckFound
	}
	return exitOK
}

// readCheckText reads the text canary-check looks at, from a file or stdin,
// up to maxCanaryCheckBytes.
func readCheckText(name string) ([]byte, error) {
	var r io.Reader = os.Stdin
	if name != "-" {
		f, err := os.Open(name)
		if err != nil {
			return nil, fmt.Errorf("read text: %w", err)
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, maxCanaryCheckBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	if len(b) > maxCanaryCheckBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", name, maxCanaryCheckBytes)
	}
	return b, nil
}

// printCanaryHits prints one line per canary found and a closing verdict.
func printCanaryHits(rep canaryCheckReport) {
	for _, h := range rep.Hits {
		owner := "unknown suite (canary line with a GUID no local suite owns)"
		if h.Known {
			owner = "suite " + h.Suite
		}
		fmt.Printf("canary %s  %s  first at line %d, %d occurrence(s)\n", h.GUID, owner, h.Line, h.Count)
	}
	if rep.Found {
		fmt.Printf("FOUND: %s holds %d canary GUID(s); it copied stamped benchmark text verbatim\n", rep.Source, len(rep.Hits))
		return
	}
	fmt.Printf("no canary found in %s (checked %d known suite canaries and the canary line pattern); this proves nothing about paraphrased or unstamped text\n", rep.Source, rep.Known)
}
