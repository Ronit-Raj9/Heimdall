// Command tavily-record records a research suite's Tavily cassette through
// the real tool proxy code in record mode (docs/research-tasks.md).
//
// From the stand-in (no key, no network; what the committed cassettes of
// examples/acme-research come from):
//
//	go run ./research/cmd/tavily-record -suite examples/acme-research/benchmarks/acme-research -corpus-urls
//
// From Tavily itself (the key only in the environment, never on the command
// line; the proxy injects it as "Authorization: Bearer <key>" and redacts it
// from the cassette):
//
//	read -rs TAVILY_API_KEY && export TAVILY_API_KEY
//	go run ./research/cmd/tavily-record -suite <suite folder> -upstream https://api.tavily.com
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/research/recording"
)

func main() {
	fs := flag.NewFlagSet("tavily-record", flag.ExitOnError)
	suiteDir := fs.String("suite", "", "suite folder (benchmarks/<suite>); its cassettes/queries.json lists what to record")
	queries := fs.String("queries", "", "recording list (default <suite>/cassettes/queries.json)")
	out := fs.String("out", "", "cassette to write (default <suite>/cassettes/tavily.cassette.jsonl)")
	upstream := fs.String("upstream", "", "Tavily base URL, e.g. https://api.tavily.com (default: the embedded stand-in)")
	keyEnv := fs.String("key-env", "TAVILY_API_KEY", "environment variable holding the Tavily key (real upstream only)")
	corpusURLs := fs.Bool("corpus-urls", false, "also write the stand-in corpus URL list into every task's verifier/tests (stand-in only)")
	_ = fs.Parse(os.Args[1:])
	if *suiteDir == "" {
		fmt.Fprintln(os.Stderr, "usage: tavily-record -suite <suite folder> [-upstream https://api.tavily.com] [-queries file] [-out file] [-corpus-urls]")
		os.Exit(2)
	}
	if *queries == "" {
		*queries = filepath.Join(*suiteDir, "cassettes", "queries.json")
	}
	if *out == "" {
		*out = filepath.Join(*suiteDir, "cassettes", "tavily.cassette.jsonl")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	q, err := recording.LoadQueries(*queries)
	if err != nil {
		fail(err)
	}
	res, err := recording.Record(ctx, q, recording.Options{Upstream: *upstream, KeyEnv: *keyEnv, Out: *out})
	if err != nil {
		fail(err)
	}
	fmt.Printf("recorded %d searches and %d fetches from %s into %s\n", res.Searches, res.Extracts, res.Upstream, res.Cassette)
	for _, f := range res.Failed {
		fmt.Fprintln(os.Stderr, "not 200:", f)
	}
	if *corpusURLs {
		if *upstream != "" {
			fail(fmt.Errorf("-corpus-urls lists the stand-in corpus; it does not apply to a real upstream"))
		}
		files, err := recording.WriteCorpusURLs(*suiteDir)
		if err != nil {
			fail(err)
		}
		fmt.Printf("wrote the corpus URL list into %d task verifier folders\n", len(files))
	}
	if len(res.Failed) > 0 {
		fmt.Fprintf(os.Stderr, "tavily-record: incomplete recording; %s left unchanged, partial cassette in %s\n", *out, res.Cassette)
		os.Exit(1)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "tavily-record:", err)
	os.Exit(1)
}
