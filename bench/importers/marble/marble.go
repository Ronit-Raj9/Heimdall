package marble

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Name is the import format name.
const Name = "marble"

// Upstream facts, checked against the repository at commit eae9f15e5b9f
// (16 Nov 2026) and the paper (arXiv 2503.01935v1) on 18 Nov 2026.
const (
	upstreamRepo  = "https://github.com/ulab-uiuc/MARBLE"
	paperURL      = "https://arxiv.org/abs/2503.01935"
	license       = "MIT"
	benchmarkName = "multiagentbench"
)

// Scenarios as upstream names them (the multiagentbench/ folders).
const (
	scenarioDatabase   = "database"
	scenarioResearch   = "research"
	scenarioCoding     = "coding"
	scenarioBargaining = "bargaining"
	scenarioMinecraft  = "minecraft"
	scenarioWerewolf   = "werewolf"
)

// importable says how a scenario is imported: verified (a deterministic
// verifier decides), judged (only a judge model can score it), or refused
// with the reason.
type importable struct {
	grading string // schemas.GradingVerified or GradingJudged; empty when refused
	refused string // why the scenario is not imported
}

var scenarios = map[string]importable{
	scenarioDatabase: {grading: schemas.GradingVerified},
	scenarioResearch: {grading: schemas.GradingJudged},
	scenarioCoding:   {grading: schemas.GradingJudged},
	scenarioBargaining: {refused: "competitive scenario: buyer and seller pursue conflicting goals and upstream rates each side apart with an LLM judge (effectiveness of strategies, progress and outcome, interaction dynamics), " +
		"so there is no shared team deliverable to grade and no team outcome to verify"},
	scenarioMinecraft: {refused: "needs an external simulator: a Minecraft Java 1.19.2 server in offline mode and Node.js mineflayer bots for the agents, " +
		"which a closed, containerised team run does not provide"},
	scenarioWerewolf: {refused: "competitive social-deduction game with its own engine and entry point (marble/environments/werewolf_env.py, scripts/werewolf), " +
		"not in the multiagentbench/ task files; no shared team deliverable to grade"},
}

// scenarioOrder is the order scenarios are reported in.
var scenarioOrder = []string{scenarioDatabase, scenarioResearch, scenarioCoding, scenarioBargaining, scenarioMinecraft, scenarioWerewolf}

// Settings (-set key=value).
const (
	setScenario   = "scenario"   // database (default), research or coding
	setLead       = "lead"       // agent id of the lead; default the first agent
	setRevision   = "revision"   // upstream commit, when -from is not a git clone
	setVersion    = "version"    // suite version, default 1.0.0
	setAgentS     = "agent_s"    // agent wall time per task, seconds
	setReferences = "references" // judged scenarios: folder of <task id>.answer reference answers
	setPostgres   = "postgres_image"
)

const (
	defaultSuiteVersion = "1.0.0"
	taskVersion         = "1.0.0"
	defaultAgentS       = 1800
	defaultPostgres     = "postgres:18"
	baseImage           = "bench/coding-core-base:1"
)

var versionRe = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// settings are the parsed -set values.
type settings struct {
	scenario   string
	lead       string
	revision   string
	version    string
	agentS     int
	references string
	postgres   string
}

func parseSettings(extra map[string]string) (settings, error) {
	s := settings{scenario: scenarioDatabase, version: defaultSuiteVersion, agentS: defaultAgentS, postgres: defaultPostgres}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := strings.TrimSpace(extra[k])
		switch k {
		case setScenario:
			s.scenario = v
		case setLead:
			s.lead = v
		case setRevision:
			s.revision = v
		case setVersion:
			if !versionRe.MatchString(v) {
				return s, fmt.Errorf("-set %s=%s: want MAJOR.MINOR.PATCH", k, v)
			}
			s.version = v
		case setAgentS:
			n, err := strconv.Atoi(v)
			if err != nil || n < 60 || n > 14400 {
				return s, fmt.Errorf("-set %s=%s: want seconds between 60 and 14400", k, v)
			}
			s.agentS = n
		case setReferences:
			s.references = v
		case setPostgres:
			if v == "" || strings.ContainsAny(v, " \t\n\"") {
				return s, fmt.Errorf("-set %s=%q: want an image reference", k, v)
			}
			s.postgres = v
		default:
			return s, fmt.Errorf("unknown -set %s (known: %s, %s, %s, %s, %s, %s, %s)", k, setScenario, setLead, setRevision, setVersion, setAgentS, setReferences, setPostgres)
		}
	}
	sc, ok := scenarios[s.scenario]
	switch {
	case !ok:
		return s, fmt.Errorf("-set %s=%s: unknown scenario (MultiAgentBench has %s)", setScenario, s.scenario, strings.Join(scenarioOrder, ", "))
	case sc.refused != "":
		return s, fmt.Errorf("-set %s=%s: not imported: %s (docs/importers-marble.md)", setScenario, s.scenario, sc.refused)
	case s.references != "" && sc.grading != schemas.GradingJudged:
		return s, fmt.Errorf("-set %s applies to the judged scenarios (research, coding) only: the %s scenario's reference is built from its labels", setReferences, s.scenario)
	}
	return s, nil
}

func init() {
	importers.Register(&importer{now: time.Now})
}

type importer struct {
	now func() time.Time
}

// Info implements importers.Importer.
func (im *importer) Info() importers.Info {
	return importers.Info{
		Name:       Name,
		Benchmarks: []string{"multiagentbench database (verified)", "multiagentbench research, coding (judged)"},
		Source:     "git clone " + upstreamRepo + " (multiagentbench/<scenario>/<scenario>_main.jsonl); paper " + paperURL,
		License:    license,
		Track:      schemas.TrackToolUse,
		Grading:    schemas.GradingVerified,
		Needs: []string{
			"gateway key slots for the whole team: every database task has 5 agents, so a team run takes 5 slots (gateway.key_slots)",
			"database: the " + defaultPostgres + " image (Docker Hub, official) for the PostgreSQL service",
			"research, coding (-set scenario=...): a judge model pinned in suite.yaml (grading judged); upstream ships no reference answers",
		},
		Summary: "MultiAgentBench (MARBLE) team tasks: upstream agents become team members, the relationship graph the mailbox routes; minecraft and bargaining refused",
	}
}

// Import implements importers.Importer.
func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	set, err := parseSettings(opts.Extra)
	if err != nil {
		return nil, err
	}
	src, err := openSource(opts.From, set.revision)
	if err != nil {
		return nil, err
	}
	recs, err := src.records(ctx, set.scenario)
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		where := src.file
		if src.root != "" {
			where = scenarioFile(src.root, set.scenario)
		}
		return nil, fmt.Errorf("no %s tasks in %s (pick another with -set %s=...)", set.scenario, where, setScenario)
	}
	suiteDir := importers.SuiteDir(opts.BenchRoot, opts.SuiteID)
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: suiteDir}
	picked, missing := pick(recs, opts.IDs, opts.Limit)
	for _, id := range missing {
		res.Skipped = append(res.Skipped, importers.Skip{ID: id, Reason: "not a " + set.scenario + " task in the source"})
	}
	if err := checkSuite(suiteDir, set.scenario); err != nil {
		return nil, err
	}
	w := &writer{set: set, src: src, suiteID: opts.SuiteID, suiteDir: suiteDir}
	sizes := map[int]int{}
	for _, r := range picked {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rt, skip := w.check(r)
		if skip != "" {
			res.Skipped = append(res.Skipped, importers.Skip{ID: r.upstreamID(), Reason: skip})
			continue
		}
		dir := filepath.Join(suiteDir, "tasks", r.benchID())
		if _, err := os.Stat(dir); err == nil && !opts.Force {
			res.Skipped = append(res.Skipped, importers.Skip{ID: r.upstreamID(), Reason: "task folder exists (use -force to replace it)"})
			continue
		}
		if err := w.writeTask(dir, r, rt); err != nil {
			return nil, fmt.Errorf("task %s: %w", r.benchID(), err)
		}
		res.Tasks = append(res.Tasks, r.benchID())
		if rt != nil {
			sizes[len(rt.Members)]++
		}
	}
	if len(res.Tasks) == 0 {
		return res, nil
	}
	if err := w.writeSuite(res.Tasks, im.now()); err != nil {
		return nil, err
	}
	if err := importers.Finish(suiteDir); err != nil {
		return nil, err
	}
	res.Notes = w.notes(ctx, res.Tasks, sizes)
	return res, nil
}

// pick keeps the records named in ids (all when empty), at most limit of
// them, in file order. An id is an upstream task id (7) or a bench task id
// (database-7). missing lists the ids that match no record.
func pick(recs []record, ids []string, limit int) ([]record, []string) {
	want := map[string]bool{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	found := map[string]bool{}
	var out []record
	for _, r := range recs {
		if len(want) > 0 {
			u, b := strconv.Itoa(r.TaskID), r.benchID()
			if !want[u] && !want[b] {
				continue
			}
			found[u], found[b] = true, true
		}
		out = append(out, r)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	var missing []string
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && !found[id] && (limit == 0 || len(out) < limit) {
			missing = append(missing, id)
		}
	}
	slices.Sort(missing)
	return out, slices.Compact(missing)
}

// checkSuite refuses to write into a suite that holds another scenario or
// was not written by this importer: a suite is verified or judged, and one
// scenario's tasks share its services and verifier.
func checkSuite(suiteDir, scenario string) error {
	b, err := os.ReadFile(filepath.Join(suiteDir, "suite.yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	text := string(b)
	if !strings.Contains(text, "importer: "+Name+"\n") {
		return fmt.Errorf("suite %s exists and was not written by the %s importer", filepath.Base(suiteDir), Name)
	}
	if !strings.Contains(text, "\n# scenario: "+scenario+"\n") {
		return fmt.Errorf("suite %s holds another MultiAgentBench scenario; import %s into a new suite id", filepath.Base(suiteDir), scenario)
	}
	return nil
}
