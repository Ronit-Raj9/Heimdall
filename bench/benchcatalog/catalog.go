// Package benchcatalog is the benchmark catalog of the web UI: the example
// projects (examples/acme-*), the suites bundled with the server
// (benchmarks/*, including imported hd-* suites when present) and the public
// benchmarks the importers convert (Terminal-Bench 2, LiveCodeBench,
// SWE-rebench, ImpossibleBench, GAIA, ...). It also seeds every new org with
// the starter agents and suites.
//
// The catalog reads a source root: the server's own bench folder (agents/,
// benchmarks/, examples/). Adding an entry copies files from the source root
// into the org's project folder; it never overwrites a file the org already
// has.
package benchcatalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/importers"
	"gopkg.in/yaml.v3"
)

// Kinds of catalog entries.
const (
	KindExample  = "example"  // a complete example project (examples/acme-*)
	KindBundled  = "bundled"  // a suite folder shipped with the server (benchmarks/<id>)
	KindImporter = "importer" // a public benchmark converted by an importer from local data
)

// Entry is one catalog item.
type Entry struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Kind       string            `json:"kind"`
	Summary    string            `json:"summary"`
	Track      string            `json:"track,omitempty"`
	Grading    string            `json:"grading,omitempty"`
	License    string            `json:"license,omitempty"`
	Source     string            `json:"source,omitempty"` // where upstream data comes from
	Benchmarks []string          `json:"benchmarks,omitempty"`
	Needs      []string          `json:"needs,omitempty"`
	Tags       []string          `json:"tags,omitempty"`
	Importer   string            `json:"importer,omitempty"` // importer format name
	SuiteID    string            `json:"suite_id,omitempty"` // suite an import or bundled copy creates
	Suites     []string          `json:"suites,omitempty"`   // suites an example adds
	Agents     []string          `json:"agents,omitempty"`   // agents an example adds
	Extra      map[string]string `json:"extra,omitempty"`    // preset importer settings
	// Available: Add works on this server now (files present, or an importer
	// that only needs a data path).
	Available bool `json:"available"`
	// Installed: every suite this entry adds is already in the org project.
	Installed bool `json:"installed"`
	// NeedsData: Add needs the path of a local copy of the upstream data.
	NeedsData bool   `json:"needs_data"`
	Hint      string `json:"hint,omitempty"`
	// Data options of importer entries (see DataSpec).
	UploadExpects string `json:"upload_expects,omitempty"` // what "Upload dataset" takes
	FetchAllowed  bool   `json:"fetch_allowed,omitempty"`  // "Fetch from URL" can work
	FetchURL      string `json:"fetch_url,omitempty"`      // known public URL to prefill
	DataNote      string `json:"data_note,omitempty"`      // extra step the import needs
	DatasetPath   string `json:"dataset_path,omitempty"`   // project path data lands in
}

// recipe is a named public benchmark mapped to an importer, with the suite
// id the laptop build used (the hd-* suites).
type recipe struct {
	id, title, importer, suite, benchmark, source string
	extra                                         map[string]string
	tags                                          []string
}

// recipes are the headline benchmarks. Each maps to a registered importer;
// the hd-* suite ids match the suites imported on the reference laptop
// (bench/benchmarks/hd-*), so a server that ships them offers a direct copy.
var recipes = []recipe{
	{id: "terminal-bench-2", title: "Terminal-Bench 2.0", importer: "harbor", suite: "hd-harbor-tb2", benchmark: "terminal-bench@2.0",
		source: "git clone --depth 1 https://github.com/laude-institute/terminal-bench-2 datasets/terminal-bench-2", tags: []string{"terminal", "coding"}},
	{id: "skillsbench", title: "SkillsBench 1.0", importer: "harbor", suite: "hd-harbor-skills", benchmark: "skillsbench@1.0",
		source: "git clone --depth 1 --branch v1.0 https://github.com/benchflow-ai/skillsbench datasets/skillsbench", tags: []string{"terminal", "skills"}},
	{id: "livecodebench", title: "LiveCodeBench (ImpossibleBench variant)", importer: "impossiblebench", suite: "hd-swe-ib-lcb", benchmark: "impossible_livecodebench",
		source: "https://huggingface.co/datasets/fjzzq2002/impossible_livecodebench (export a split to JSONL, see docs/importing-swe.md)", tags: []string{"coding", "integrity"}},
	{id: "swe-rebench", title: "SWE-rebench", importer: "swebench", suite: "hd-swe-rebench", benchmark: "nebius/SWE-rebench-leaderboard",
		source: "https://huggingface.co/datasets/nebius/SWE-rebench-leaderboard (export rows to JSONL, see docs/importing-swe.md)", tags: []string{"coding", "repository"}},
	{id: "impossible-swebench", title: "ImpossibleBench (SWE-bench variant)", importer: "swebench", suite: "hd-swe-ib-swe", benchmark: "impossible_swebench",
		source: "https://huggingface.co/datasets/fjzzq2002/impossible_swebench (export a split to JSONL, see docs/importing-swe.md)", tags: []string{"coding", "integrity"}},
}

// Source is where the catalog reads from.
type Source struct {
	Root string // server bench folder holding agents/, benchmarks/, examples/
}

var (
	rootOnce sync.Once
	rootVal  string
)

// SourceRoot finds the server's bench folder: HEIMDALL_CATALOG_ROOT when
// set, else the first of the working directory and its parents that holds
// agents/ and benchmarks/. Empty when none does.
func SourceRoot() string {
	rootOnce.Do(func() {
		if v := strings.TrimSpace(os.Getenv("HEIMDALL_CATALOG_ROOT")); v != "" {
			if abs, err := filepath.Abs(v); err == nil {
				rootVal = abs
			}
			return
		}
		wd, err := os.Getwd()
		if err != nil {
			return
		}
		for d := wd; ; d = filepath.Dir(d) {
			for _, c := range []string{d, filepath.Join(d, "bench")} {
				if isDir(filepath.Join(c, "agents")) && isDir(filepath.Join(c, "benchmarks")) {
					rootVal = c
					return
				}
			}
			if filepath.Dir(d) == d {
				return
			}
		}
	})
	return rootVal
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// List builds the catalog for the org project at project (may be empty).
func (s Source) List(project string) []Entry {
	var out []Entry
	seen := map[string]bool{}
	add := func(e Entry) {
		if seen[e.ID] {
			return
		}
		seen[e.ID] = true
		out = append(out, e)
	}
	installed := func(suites ...string) bool {
		if project == "" || len(suites) == 0 {
			return false
		}
		for _, id := range suites {
			if !isDir(filepath.Join(project, "benchmarks", id)) {
				return false
			}
		}
		return true
	}
	infos := map[string]importers.Info{}
	for _, in := range importers.All() {
		infos[in.Name] = in
	}

	// 1. Headline benchmarks (recipes): a bundled copy when the server
	// ships the suite, else an import from local data.
	for _, r := range recipes {
		in := infos[r.importer]
		e := Entry{ID: r.id, Title: r.title, Importer: r.importer, SuiteID: r.suite, Benchmarks: []string{r.benchmark},
			Source: r.source, Track: in.Track, Grading: in.Grading, License: in.License, Needs: in.Needs, Tags: r.tags, Extra: r.extra}
		e.Summary = fmt.Sprintf("%s through the %s importer (suite %s). %s", r.benchmark, r.importer, r.suite, in.Summary)
		if s.Root != "" && isDir(filepath.Join(s.Root, "benchmarks", r.suite)) {
			e.Kind, e.Available = KindBundled, true
			e.Hint = "Bundled with this server: Add copies the suite into your project."
		} else {
			e.Kind, e.NeedsData = KindImporter, true
			e.Available = in.Name != ""
			e.Hint = "Fetch the data first (" + r.source + "), then give its path."
			if !e.Available {
				e.Hint = "The " + r.importer + " importer is not linked into this server build."
			}
			applyData(&e)
		}
		e.Installed = installed(r.suite)
		add(e)
	}

	// 2. Every importer (GAIA, LongMemEval, LoCoMo, tau2, KernelBench, MARBLE, Harbor, ...).
	for _, in := range importers.All() {
		e := Entry{ID: "import-" + in.Name, Title: importerTitle(in.Name), Kind: KindImporter, Importer: in.Name,
			SuiteID: in.Name, Summary: in.Summary, Track: in.Track, Grading: in.Grading, License: in.License,
			Source: in.Source, Benchmarks: in.Benchmarks, Needs: in.Needs, Available: true, NeedsData: true,
			Hint: "Fetch the data first (" + in.Source + "), then give its path."}
		applyData(&e)
		e.Installed = installed(e.SuiteID)
		add(e)
	}

	// 3. Example projects.
	if s.Root != "" {
		dirs, _ := filepath.Glob(filepath.Join(s.Root, "examples", "acme-*"))
		sort.Strings(dirs)
		for _, d := range dirs {
			if !isDir(d) {
				continue
			}
			name := filepath.Base(d)
			title, summary := readmeHead(filepath.Join(d, "README.md"))
			if title == "" {
				title = name
			}
			e := Entry{ID: "example-" + name, Title: title, Kind: KindExample, Summary: summary, Available: true,
				Suites: subdirs(filepath.Join(d, "benchmarks")), Agents: yamlNames(filepath.Join(d, "agents")),
				Tags: []string{"example"}, Hint: "Adds the example's suites, agents, experiments and policies to your project."}
			e.Installed = installed(e.Suites...)
			add(e)
		}

		// 4. Other suites bundled with the server (coding-core, hd-* not named above).
		for _, id := range subdirs(filepath.Join(s.Root, "benchmarks")) {
			if seen["suite-"+id] || recipeSuite(id) {
				continue
			}
			meta := readSuiteMeta(filepath.Join(s.Root, "benchmarks", id, "suite.yaml"))
			e := Entry{ID: "suite-" + id, Title: id, Kind: KindBundled, SuiteID: id, Summary: meta.Description,
				Importer: meta.Imported.Importer, Source: meta.Imported.Source, Available: true,
				Hint: "Bundled with this server: Add copies the suite into your project."}
			if meta.Imported.Importer != "" {
				e.Tags = []string{"imported"}
			}
			e.Installed = installed(id)
			add(e)
		}
	}
	return out
}

// Find returns the entry with id.
func (s Source) Find(project, id string) (Entry, bool) {
	for _, e := range s.List(project) {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

func recipeSuite(id string) bool {
	for _, r := range recipes {
		if r.suite == id {
			return true
		}
	}
	return false
}

var titles = map[string]string{
	"harbor": "Harbor task folders", "gaia": "GAIA", "longmemeval": "LongMemEval", "locomo": "LoCoMo",
	"tau2": "tau2-bench", "kernelbench": "KernelBench", "marble": "MARBLE (MultiAgentBench)",
	"impossiblebench": "ImpossibleBench", "swebench": "SWE-bench family", "agentdojo": "AgentDojo",
	"mcpmark": "MCPMark", "browsecomp-plus": "BrowseComp-Plus",
}

func importerTitle(name string) string {
	if t, ok := titles[name]; ok {
		return t
	}
	return name
}

type suiteMeta struct {
	Description string `yaml:"description"`
	Imported    struct {
		Importer string `yaml:"importer"`
		Source   string `yaml:"source"`
	} `yaml:"imported"`
}

func readSuiteMeta(path string) suiteMeta {
	var m suiteMeta
	b, err := readSmall(path, 1<<20)
	if err == nil {
		_ = yaml.Unmarshal(b, &m)
	}
	return m
}

func readSmall(path string, max int64) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > max {
		return nil, errors.New("not a small regular file")
	}
	return os.ReadFile(path)
}

// readmeHead is the first "# " heading and the first paragraph after it.
func readmeHead(path string) (title, summary string) {
	b, err := readSmall(path, 1<<20)
	if err != nil {
		return "", ""
	}
	var para []string
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case title == "" && strings.HasPrefix(t, "# "):
			title = strings.TrimSpace(strings.TrimPrefix(t, "# "))
		case title == "":
		case t == "" && len(para) > 0:
			return title, strings.Join(para, " ")
		case t == "" || strings.HasPrefix(t, "#"):
		default:
			para = append(para, t)
		}
	}
	return title, strings.Join(para, " ")
}

func subdirs(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out
}

func yamlNames(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if n := e.Name(); !e.IsDir() && strings.HasSuffix(n, ".yaml") {
			out = append(out, strings.TrimSuffix(n, ".yaml"))
		}
	}
	return out
}

// dockerfileRe finds an agent's build dockerfile (agents/images/x/Dockerfile).
var dockerfileRe = regexp.MustCompile(`(?m)^\s*dockerfile:\s*["']?([A-Za-z0-9_./-]+)["']?\s*$`)
