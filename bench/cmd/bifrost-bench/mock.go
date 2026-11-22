package main

// Stateful mocks from OpenAPI (lane Y14): `mock openapi` writes a task
// service that serves a customer's API shape over seeded, exported state;
// `mock fixtures` records a run's calls of that service as fixtures it
// answers first (record and replay).

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/taskchecks"
)

func init() {
	register("mock", "stateful API mocks: mock openapi|fixtures (a task service from an OpenAPI file, record and replay)", runMock)
}

const mockUsage = `usage: bifrost-bench mock <openapi|fixtures> ...
  mock openapi -suite <suite> -id <service id> -spec <openapi.yaml|json> [-force] [-json]
      write services/<id>/ (server, image, mock.json) into the suite and print the services: entry and seed a task adds
  mock fixtures -suite <suite> -id <service id> -run <run folder> [-replace]
      add the run's recorded calls of the service (tools.jsonl) to its mock.json as fixtures, answered first`

func runMock(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, mockUsage)
		return exitUsage
	}
	switch args[0] {
	case "openapi":
		return runMockOpenAPI(args[1:])
	case "fixtures":
		return runMockFixtures(args[1:])
	}
	fmt.Fprintln(os.Stderr, mockUsage)
	return exitUsage
}

func runMockOpenAPI(args []string) int {
	fs := flag.NewFlagSet("mock openapi", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	suiteID := fs.String("suite", "", "suite id (a folder under benchmarks/)")
	id := fs.String("id", "", "service id of the mock")
	spec := fs.String("spec", "", "OpenAPI 3 file (YAML or JSON)")
	force := fs.Bool("force", false, "replace an existing services/<id>/")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	if _, err := parseArgs(fs, args); err != nil || *suiteID == "" || *id == "" || *spec == "" {
		fmt.Fprintln(os.Stderr, mockUsage)
		return exitUsage
	}
	root, _, err := benchmarksRoot(fs, "", projectConfig(*config, flagSet(fs, "config")))
	if err != nil {
		return fail("%v", err)
	}
	data, err := os.ReadFile(*spec)
	if err != nil {
		return fail("%v", err)
	}
	dir := filepath.Join(root, *suiteID)
	if _, err := os.Stat(filepath.Join(dir, "suite.yaml")); err != nil {
		return fail("suite %s: %v", *suiteID, err)
	}
	res, err := scaffold.WriteMock(dir, *id, data, filepath.Base(*spec), *force)
	if err != nil {
		return fail("%v", err)
	}
	if *asJSON {
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
		return 0
	}
	fmt.Printf("wrote %s: %d operations over %d collections\n", res.Dir, len(res.Mock.Operations), len(res.Mock.Collections()))
	for _, op := range res.Mock.Operations {
		fmt.Printf("  %-28s %-6s %-40s %s\n", op.Name, op.Method, op.Path, op.Kind)
	}
	for _, w := range res.Warnings {
		fmt.Println("note:", w)
	}
	fmt.Printf("\nAdd to a task's services: in task.yaml\n%s\nand seed it with seed/%s.json, for example:\n%s", res.Service, *id, res.Seed)
	return 0
}

func runMockFixtures(args []string) int {
	fs := flag.NewFlagSet("mock fixtures", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	suiteID := fs.String("suite", "", "suite id")
	id := fs.String("id", "", "service id of the mock")
	run := fs.String("run", "", "run folder holding tools.jsonl")
	replace := fs.Bool("replace", false, "replace the fixtures instead of adding to them")
	if _, err := parseArgs(fs, args); err != nil || *suiteID == "" || *id == "" || *run == "" {
		fmt.Fprintln(os.Stderr, mockUsage)
		return exitUsage
	}
	root, _, err := benchmarksRoot(fs, "", projectConfig(*config, flagSet(fs, "config")))
	if err != nil {
		return fail("%v", err)
	}
	svc := filepath.Join(root, *suiteID, "services", *id)
	m, err := scaffold.ReadMock(svc)
	if err != nil {
		return fail("%s: %v", svc, err)
	}
	rows, err := taskchecks.ReadLedger(filepath.Join(*run, "tools.jsonl"))
	if err != nil {
		return fail("%v", err)
	}
	fx := scaffold.FixturesFromLedger(m, *id, rows)
	if len(fx) == 0 {
		return fail("the run made no calls of service %s", *id)
	}
	if *replace {
		m.Fixtures = fx
	} else {
		m.Fixtures = append(m.Fixtures, fx...)
	}
	if err := scaffold.SaveMock(svc, m); err != nil {
		return fail("%v", err)
	}
	fmt.Printf("added %d fixtures to %s (now %d); rebuild the service image before the next run\n", len(fx), filepath.Join(svc, "mock.json"), len(m.Fixtures))
	return 0
}
