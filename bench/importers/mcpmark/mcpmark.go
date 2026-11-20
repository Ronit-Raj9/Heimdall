package mcpmark

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Name is the import format name.
const Name = "mcpmark"

// Importer settings (-set key=value).
const (
	SetFamilies   = "families"   // comma-separated task families; only filesystem is built
	SetDifficulty = "difficulty" // all (default), easy or standard
	SetStateDir   = "state_dir"  // extracted test environments; default <from>/test_environments
	SetRef        = "ref"        // upstream commit, when -from is not a git clone
	SetReferences = "references" // folder of reference controls named <task id>.sh
)

const (
	upstreamRepo    = "https://github.com/eval-sys/mcpmark"
	upstreamLicense = "Apache-2.0"
	familyFS        = "filesystem"
	serviceID       = "fs"
	servicePort     = 8080
	// fsServerVersion is the @modelcontextprotocol/server-filesystem version
	// MCPMark pins in src/agents/base_agent.py.
	fsServerVersion = "2025.12.18"
	workspaceImage  = "python:3.12-slim"
	suiteVersion    = "1.0.0"
	taskVersion     = "1.0.0"
	agentTimeoutS   = 1800
	verifierTimeout = 600
	minExportBytes  = 16 << 20
)

// otherFamilies says why each MCPMark family other than filesystem is not
// imported. The bench never reaches the network at run time, so a family
// that needs a hosted account or site cannot run here as it does upstream.
var otherFamilies = map[string]string{
	"postgres":            "needs a PostgreSQL service restored from upstream's database dumps and a verifier with a live database; not built yet (filesystem only)",
	"github":              "needs a GitHub account, a token and the network at run time",
	"notion":              "needs a Notion workspace, a token and the network at run time",
	"playwright":          "drives a browser against a hosted site (eval-web.mcpmark.ai), so it needs the network at run time",
	"playwright_webarena": "needs the WebArena site containers and a browser service; not built",
	"supabase":            "needs a Supabase backend; not built",
	"insforge":            "needs an InsForge backend; not built",
}

//go:embed image/fs.Dockerfile
var fsDockerfile string

//go:embed image/runverify.py
var runVerify string

func init() {
	importers.Register(&importer{now: time.Now})
}

// importer converts MCPMark's locally runnable task families into an mcp
// suite whose tasks run the family's MCP server as a service and are graded
// on its exported state by the upstream verify script, offline.
type importer struct {
	now func() time.Time
}

// Info implements importers.Importer.
func (im *importer) Info() importers.Info {
	return importers.Info{
		Name:       Name,
		Benchmarks: []string{"MCPMark filesystem (easy and standard)"},
		Source:     "git clone --depth 1 " + upstreamRepo + ".git datasets/mcpmark, then unzip the filesystem test environments into datasets/mcpmark/test_environments (docs/importing-mcpmark.md)",
		License:    upstreamLicense,
		Track:      schemas.TrackMCP,
		Grading:    schemas.GradingVerified,
		Needs: []string{
			"network while the service image builds (npm installs the filesystem MCP server)",
			"services, state export, the tool proxy, and the bench image (images.toolproxy) with the toolproxy bridge command",
			"a reference control per task before admission: MCPMark ships none",
		},
		Summary: "MCPMark filesystem tasks: the upstream MCP server over the task's files, graded offline by the upstream verify script on the exported folder",
	}
}

// settings are the parsed -set values of one import.
type settings struct {
	difficulty string
	stateDir   string
	ref        string
	references string
}

func parseSettings(from string, extra map[string]string) (settings, error) {
	s := settings{difficulty: "all", stateDir: filepath.Join(from, "test_environments")}
	known := []string{SetFamilies, SetDifficulty, SetStateDir, SetRef, SetReferences}
	for k := range extra {
		if !slices.Contains(known, k) {
			return s, fmt.Errorf("unknown setting %q (known: %s)", k, strings.Join(known, ", "))
		}
	}
	if v := extra[SetFamilies]; v != "" {
		for _, f := range strings.Split(v, ",") {
			f = strings.TrimSpace(f)
			if f == familyFS {
				continue
			}
			if why, ok := otherFamilies[f]; ok {
				return s, fmt.Errorf("family %s is not supported: %s", f, why)
			}
			return s, fmt.Errorf("unknown family %q", f)
		}
	}
	switch v := extra[SetDifficulty]; v {
	case "", "all":
	case "easy", "standard":
		s.difficulty = v
	default:
		return s, fmt.Errorf("%s=%q: want all, easy or standard", SetDifficulty, v)
	}
	if v := extra[SetStateDir]; v != "" {
		s.stateDir = v
	}
	s.ref = extra[SetRef]
	s.references = extra[SetReferences]
	return s, nil
}

// candidate is one upstream task chosen for import.
type candidate struct {
	task upTask
	id   string // bench task id
}

// Import implements importers.Importer.
func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	if opts.SuiteID == "" || opts.From == "" || opts.BenchRoot == "" {
		return nil, errors.New("mcpmark: bench root, -from and -suite are required")
	}
	set, err := parseSettings(opts.From, opts.Extra)
	if err != nil {
		return nil, fmt.Errorf("mcpmark: %w", err)
	}
	up, err := openUpstream(opts.From, set.ref)
	if err != nil {
		return nil, fmt.Errorf("mcpmark: %w", err)
	}
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: opts.SuiteDir()}
	picked, skips, err := selectTasks(up, set, opts.IDs)
	if err != nil {
		return nil, fmt.Errorf("mcpmark: %w", err)
	}
	res.Skipped = append(res.Skipped, skips...)
	if opts.Limit > 0 && len(picked) > opts.Limit {
		for _, c := range picked[opts.Limit:] {
			res.Skipped = append(res.Skipped, importers.Skip{ID: c.id, Reason: fmt.Sprintf("over -limit %d", opts.Limit)})
		}
		picked = picked[:opts.Limit]
	}
	w := &writer{up: up, set: set, suiteDir: res.SuiteDir, states: map[string]*stateInfo{}}
	for _, c := range picked {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("mcpmark: %w", err)
		}
		dir := filepath.Join(res.SuiteDir, "tasks", c.id)
		if _, err := os.Stat(dir); err == nil && !opts.Force {
			res.Skipped = append(res.Skipped, importers.Skip{ID: c.id, Reason: "task folder exists (pass -force to replace it)"})
			continue
		}
		if err := w.writeTask(dir, c); err != nil {
			return nil, fmt.Errorf("mcpmark: task %s: %w", c.id, err)
		}
		res.Tasks = append(res.Tasks, c.id)
	}
	rec := schemas.ImportRecord{
		Importer:  Name,
		Benchmark: "mcpmark filesystem (" + set.difficulty + ")",
		Source:    upstreamRepo,
		Revision:  up.revision,
		License:   upstreamLicense,
		At:        im.now().UTC().Truncate(time.Second),
	}
	if err := w.writeSuite(opts.SuiteID, res.Tasks, rec); err != nil {
		return nil, fmt.Errorf("mcpmark: %w", err)
	}
	if err := importers.Finish(res.SuiteDir); err != nil {
		return nil, err
	}
	res.Notes = []string{
		"the first run builds each category's service image with network (npm installs @modelcontextprotocol/server-filesystem@" + fsServerVersion + ") on top of the bench image, which must carry the toolproxy bridge command",
		fmt.Sprintf("MCPMark ships no reference solutions: %d of %d task(s) have controls/reference.sh that refuses until you write one (or pass -set %s=<folder>)", w.missingRefs, len(res.Tasks), SetReferences),
		"bifrost-bench suite lint " + opts.SuiteID,
		"bifrost-bench suite lock " + opts.SuiteID,
		"bifrost-bench suite admit " + opts.SuiteID,
	}
	return res, nil
}

// selectTasks lists the filesystem tasks to import and one skip line per
// other family, with the reason it is not imported.
func selectTasks(up *upstream, set settings, ids []string) ([]candidate, []importers.Skip, error) {
	families := []string{familyFS}
	for f := range otherFamilies {
		families = append(families, f)
	}
	sort.Strings(families)
	all, err := up.tasks(families)
	if err != nil {
		return nil, nil, err
	}
	var out []candidate
	var skips []importers.Skip
	others := map[string]int{}
	for _, t := range all {
		if t.Service != familyFS {
			others[t.Service]++
			continue
		}
		id := taskID(t)
		if len(ids) > 0 && !slices.Contains(ids, id) && !slices.Contains(ids, t.upstreamID()) {
			continue
		}
		if set.difficulty != "all" && t.Difficulty != set.difficulty {
			continue
		}
		if st, err := os.Stat(filepath.Join(set.stateDir, t.category())); err != nil || !st.IsDir() {
			skips = append(skips, importers.Skip{ID: id, Reason: fmt.Sprintf("test environment %s not found in %s (download https://storage.mcpmark.ai/filesystem/%s.zip and unzip it there)", t.category(), set.stateDir, t.category())})
			continue
		}
		out = append(out, candidate{task: t, id: id})
	}
	fams := make([]string, 0, len(others))
	for f := range others {
		fams = append(fams, f)
	}
	sort.Strings(fams)
	for _, f := range fams {
		skips = append(skips, importers.Skip{ID: f + "/*", Reason: fmt.Sprintf("%d task(s): %s", others[f], otherFamilies[f])})
	}
	return out, skips, nil
}

var badIDChars = regexp.MustCompile(`[^a-z0-9.-]+`)

// taskID is the bench task id of an upstream task, e.g.
// fs-standard-file-context-uppercase.
func taskID(t upTask) string {
	clean := func(s string) string {
		return strings.Trim(badIDChars.ReplaceAllString(strings.ToLower(s), "-"), "-")
	}
	return "fs-" + clean(t.Difficulty) + "-" + clean(t.Category) + "-" + clean(t.Name)
}
