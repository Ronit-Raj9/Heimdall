package marble

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

//go:embed assets/verify.py
var verifyScript string

//go:embed assets/teamref.py
var teamRefScript string

//go:embed assets/dbtool.py
var dbToolScript string

// Database task layout.
const (
	artifactID     = "diagnosis"
	artifactName   = "diagnosis.md"
	dbService      = "db"
	toolService    = "dbtool"
	toolPort       = 8080
	probeSQL       = "SELECT count(*) FROM pg_stat_statements"
	teamFile       = "marble-team.json"
	maxArtifact    = 256 * 1024
	referenceWaitS = 150
)

// replicas are the anomalies the database tool can replay
// (assets/dbtool.py REPLICAS); upstream's MISSING_INDEXES and the two
// combined anomalies have none and their tasks are skipped.
var replicas = []string{"INSERT_LARGE_DATA", "LOCK_CONTENTION", "VACUUM", "REDUNDANT_INDEX", "FETCH_LARGE_DATA"}

// anomalyScale is how far the replica scales the upstream trigger down:
// upstream runs each statement from `threads` (100) concurrent threads for
// 60 seconds with nrow 100 (db_env.py passes only threads, ncolumn and
// colsize, so main.py's default nrow of 100 applies); the replica runs it
// `repeats` times in a row, and holds one row lock against `waiters`
// sessions for `hold_s` seconds for lock contention.
var anomalyScale = map[string]any{"table": "table1", "nrow": 100, "repeats": 200, "vacuum_repeats": 20, "waiters": 8, "hold_s": 3, "orders": 10000}

// profileLabel reads which root cause a database agent's profile assigns
// it ("agent1 will explore the possibility of INSERT_LARGE_DATA as a root
// cause").
var profileLabel = regexp.MustCompile(`possibility of ([A-Z][A-Z_,]*[A-Z]) as a root cause`)

// writer writes one import's task folders and its suite.
type writer struct {
	set      settings
	src      *source
	suiteID  string
	suiteDir string
}

// check decides whether a record can be imported and builds its routes. It
// returns a skip reason when it cannot.
func (w *writer) check(r record) (*routes, string) {
	if strings.TrimSpace(r.Task.Content) == "" {
		return nil, "task content is empty"
	}
	switch r.CoordinateMode {
	case "", "graph", "star", "tree", "chain":
	default:
		return nil, fmt.Sprintf("coordinate_mode %q is not one of graph, star, tree, chain", r.CoordinateMode)
	}
	rt, skip := buildRoutes(r, w.set.lead)
	if skip != "" {
		return nil, skip
	}
	if term := leakTerm(r); term != "" {
		return nil, fmt.Sprintf("its text mentions %q, which suite lint refuses in an instruction (it would tell the agent how it is graded); upstream text is not rewritten", term)
	}
	if far := rt.unreachable(); len(far) > 0 {
		return nil, fmt.Sprintf("member(s) %s have no route to the lead %s through the relationship graph, so they could not report to it through the mailbox", strings.Join(far, ", "), rt.Lead)
	}
	if w.set.scenario != scenarioDatabase {
		return rt, ""
	}
	t := r.Task
	switch {
	case len(rt.Members) < 2:
		return nil, "a database task needs at least two agents for a team"
	case len(t.Labels) == 0 || len(t.RootCauses) == 0:
		return nil, "no labels or root causes"
	case t.LabelsPred < len(t.RootCauses):
		return nil, fmt.Sprintf("number_of_labels_pred %d is below the %d true root causes", t.LabelsPred, len(t.RootCauses))
	case strings.TrimSpace(r.Environment.InitSQL) == "":
		return nil, "no init_sql"
	case len(r.Environment.Anomalies) == 0:
		return nil, "no anomalies to trigger"
	}
	for _, c := range t.RootCauses {
		if !slices.Contains(t.Labels, c) {
			return nil, fmt.Sprintf("root cause %s is not one of the task's labels", c)
		}
	}
	for _, a := range r.Environment.Anomalies {
		if !slices.Contains(replicas, a.Anomaly) {
			return nil, fmt.Sprintf("anomaly %s has no replica in the database tool (replayed: %s)", a.Anomaly, strings.Join(replicas, ", "))
		}
		if a.NColumn < 1 || a.NColumn > 100 || a.ColSize < 1 || a.ColSize > 1000 {
			return nil, fmt.Sprintf("anomaly %s: ncolumn %d or colsize %d out of range", a.Anomaly, a.NColumn, a.ColSize)
		}
	}
	return rt, ""
}

// leakTerms mirror suite/lint.go: words an instruction must not hold.
var leakTerms = []string{"hidden test", "grader", "agents.md", "claude.md"}

// leakTerm is the first leak term in the upstream text that goes into the
// instruction (task, output format, profiles), or empty.
func leakTerm(r record) string {
	parts := []string{r.Task.Content, r.Task.OutputFormat}
	for _, a := range r.Agents {
		parts = append(parts, a.Profile)
	}
	text := strings.ToLower(strings.Join(parts, "\n"))
	for _, t := range leakTerms {
		if strings.Contains(text, t) {
			return t
		}
	}
	return ""
}

// writeTask writes one task folder, replacing what was there.
func (w *writer) writeTask(dir string, r record, rt *routes) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	var files map[string]string
	var err error
	if w.set.scenario == scenarioDatabase {
		files, err = w.databaseFiles(r, rt)
	} else {
		files, err = w.judgedFiles(r, rt)
	}
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		p := filepath.Join(dir, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(n, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(files[n]), mode); err != nil {
			return err
		}
	}
	return nil
}

// teamDoc is the team file in the workspace (marble-team.json): who is on
// the team, who leads, who may message whom, and each member's upstream
// profile.
func teamDoc(r record, rt *routes) (string, error) {
	profiles := map[string]string{}
	for _, a := range r.Agents {
		profiles[a.AgentID] = strings.TrimSpace(a.Profile)
	}
	b, err := json.MarshalIndent(map[string]any{
		"members":         rt.Members,
		"lead":            rt.Lead,
		"may_message":     rt.Allowed,
		"coordinate_mode": rt.Mode,
		"relationships":   rt.Edges,
		"profiles":        profiles,
	}, "", "  ")
	return string(b) + "\n", err
}

// databaseFiles are the files of one database task.
func (w *writer) databaseFiles(r record, rt *routes) (map[string]string, error) {
	team, err := teamDoc(r, rt)
	if err != nil {
		return nil, err
	}
	findings := map[string]map[string]any{}
	for _, a := range r.Agents {
		f := map[string]any{}
		if m := profileLabel.FindStringSubmatch(a.Profile); m != nil && slices.Contains(r.Task.Labels, m[1]) {
			f["label"], f["present"] = m[1], slices.Contains(r.Task.RootCauses, m[1])
		}
		findings[a.AgentID] = f
	}
	paths := map[string][]string{}
	for _, m := range rt.Members {
		paths[m] = rt.pathToLead(m)
	}
	plan, err := json.Marshal(map[string]any{
		"members": rt.Members, "lead": rt.Lead, "allowed": rt.Allowed, "paths": paths,
		"findings": findings, "root_causes": r.Task.RootCauses, "artifact": artifactName,
		"tool": toolService, "probe_sql": probeSQL, "wait_s": referenceWaitS,
	})
	if err != nil {
		return nil, err
	}
	ref, err := hereDoc("Reference control: the task's true root causes, reached as a team through the mailbox (or alone), after one query on the database tool.", teamRefScript, string(plan))
	if err != nil {
		return nil, err
	}
	expected, err := json.MarshalIndent(map[string]any{
		"scenario": scenarioDatabase, "task_id": r.TaskID, "labels": r.Task.Labels, "root_causes": r.Task.RootCauses,
		"max_labels": r.Task.LabelsPred, "artifact_id": artifactID, "artifact_name": artifactName,
		"routes": map[string]any{"members": rt.Members, "lead": rt.Lead, "allowed": rt.Allowed},
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	setup, err := json.MarshalIndent(map[string]any{"anomalies": r.Environment.Anomalies, "scale": anomalyScale}, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"task.yaml":                       w.databaseTaskYAML(r, rt),
		"instruction.md":                  databaseInstruction(r, rt),
		"repo/README.md":                  "Workspace of MultiAgentBench database task " + fmt.Sprint(r.TaskID) + ". The team's file is " + teamFile + "; write the diagnosis to " + artifactName + ".\n",
		"repo/" + teamFile:                team,
		"service/init.sql":                r.Environment.InitSQL + "\n",
		"service/setup.json":              string(setup) + "\n",
		"service/dbtool.py":               dbToolScript,
		"verifier/tests/verify.py":        verifyScript,
		"verifier/tests/expected.json":    string(expected) + "\n",
		"verifier/requirements.md":        databaseRequirements(r),
		"solution/reference.sh":           ref,
		"controls/empty.sh":               "#!/bin/sh\n# Empty control (must fail): writes no diagnosis.\nexit 0\n",
		"controls/exploit.sh":             writeScript("Exploit control (must fail): names every label, so every true root cause is among them.", "ROOT_CAUSES: "+strings.Join(r.Task.Labels, ", ")),
		"controls/mutant-missed-cause.sh": writeScript("Mutant control (a near miss, must fail): one true root cause swapped for a wrong label.", "ROOT_CAUSES: "+strings.Join(missedCause(r.Task.RootCauses, r.Task.Labels), ", ")),
	}, nil
}

// missedCause is the root causes with the last one replaced by the first
// label that is not a true root cause.
func missedCause(gold, labels []string) []string {
	out := append([]string(nil), gold...)
	for _, l := range labels {
		if !slices.Contains(gold, l) {
			out[len(out)-1] = l
			return out
		}
	}
	return out[:len(out)-1]
}

// writeScript is a control that writes one diagnosis line.
func writeScript(purpose, line string) string {
	return "#!/bin/sh\n# " + purpose + "\nset -eu\nprintf '%s\\n' '# Diagnosis' '' " + shQuote(line) + " > " + artifactName + "\n"
}

// shQuote quotes s for a POSIX shell.
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// hereDoc is a POSIX shell control that runs script with python3, plan on
// its stdin, both in here-documents.
func hereDoc(purpose, script, plan string) (string, error) {
	if strings.Contains(script, "\nPY\n") || strings.Contains(plan, "\n") {
		return "", errors.New("control script or plan holds a here-document marker or a newline")
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# " + purpose + "\n# Runs in the agent's place with the services up; see docs/importers-marble.md.\n")
	b.WriteString("set -eu\nref=$(mktemp)\ntrap 'rm -f \"$ref\"' EXIT\ncat > \"$ref\" <<'PY'\n")
	b.WriteString(script)
	if !strings.HasSuffix(script, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("PY\npython3 -I \"$ref\" <<'JSON'\n" + plan + "\nJSON\n")
	return b.String(), nil
}

// databaseTaskYAML is the task file of a database task.
func (w *writer) databaseTaskYAML(r record, rt *routes) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Imported from MultiAgentBench (MARBLE) by `bifrost-bench import marble`; see docs/importers-marble.md.\n")
	p("id: %s\nversion: %s\n", r.benchID(), taskVersion)
	p("title: %s\n", q(fmt.Sprintf("MultiAgentBench database task %d: find the root cause of a PostgreSQL anomaly as a team", r.TaskID)))
	p("project: %s\nkind: performance\ninstruction: instruction.md\nrepo_dir: repo\n", q("marble-database"))
	p("output:\n  kind: artifact\n  artifacts:\n    - {id: %s, path: %s, required: true, max_bytes: %d}\n", artifactID, artifactName, maxArtifact)
	p("verifier:\n  tests: verifier/tests\n  required: []\n")
	p("protected: [%s]\n", teamFile)
	p("controls:\n  reference: solution/reference.sh\n  empty: controls/empty.sh\n  mutants: [controls/mutant-missed-cause.sh]\n  exploit: controls/exploit.sh\n")
	p("timeouts:\n  agent_s: %d\n", w.set.agentS)
	tags := []string{"marble", "multi_agent", scenarioDatabase}
	for _, a := range r.Environment.Anomalies {
		tags = append(tags, strings.ToLower(a.Anomaly))
	}
	p("tags: [%s]\n", strings.Join(uniq(tags), ", "))
	p("capabilities: [tool_use, collaboration]\n")
	p("provenance:\n  source: %s\n  base_commit: %s\n  license: %s\n  imported_from: %s\n  created_by: %s\n",
		q(upstreamRepo), q(w.src.revisionOr("unknown")), license, q(provenanceText(r)), q("bifrost-bench import marble"))
	p("team:\n")
	for _, m := range rt.team() {
		p("  - {id: %s, role: %s}\n", m.ID, m.Role)
	}
	p("services:\n")
	p("  # PostgreSQL with pg_stat_statements, as upstream's db_env_docker compose file starts it. Hidden: only the database tool reaches it.\n")
	p("  - id: %s\n    role: data\n    image: %s\n    hidden: true\n", dbService, q(w.set.postgres))
	p("    argv: [postgres, -c, shared_preload_libraries=pg_stat_statements, -c, pg_stat_statements.track=all, -c, max_connections=100]\n")
	p("    env: {POSTGRES_USER: test, POSTGRES_DB: sysbench, POSTGRES_HOST_AUTH_METHOD: trust}\n")
	p("    port: 5432\n")
	p("    ready:\n      argv: [pg_isready, -h, 127.0.0.1, -p, \"5432\", -U, test, -d, sysbench]\n      timeout_s: 120\n")
	p("  # The agents' only tool, upstream's query_db action, behind the tool proxy. Ready once the init SQL and the anomaly replica have run.\n")
	p("  - id: %s\n    role: tool\n    image: %s\n", toolService, q(baseImage))
	p("    argv: [python3, -u, -I, /opt/marble/dbtool.py, --setup, /opt/marble/setup.json, --init-sql, /opt/marble/init.sql, --port, \"%d\"]\n", toolPort)
	p("    files:\n      /opt/marble/dbtool.py: service/dbtool.py\n      /opt/marble/setup.json: service/setup.json\n      /opt/marble/init.sql: service/init.sql\n")
	p("    port: %d\n", toolPort)
	p("    ready:\n      http: /healthz\n      timeout_s: 240\n")
	return b.String()
}

// revisionOr is the upstream revision, else fallback.
func (s *source) revisionOr(fallback string) string {
	if s.revision != "" {
		return s.revision
	}
	return fallback
}

// provenanceText names the upstream record exactly: file, line, task id
// and the sha256 of the line.
func provenanceText(r record) string {
	return fmt.Sprintf("%s/%s/%s line %d (task_id %d, sha256 %s)", benchmarkName, r.Scenario, r.file, r.line, r.TaskID, r.sha256()[:16])
}

// q quotes s as a YAML double-quoted scalar (JSON string syntax, which YAML
// reads the same way).
func q(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

func uniq(in []string) []string {
	var out []string
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// teamSection is the part of every instruction that describes the team:
// the members and their upstream profiles, the lead, who may message whom,
// and how the mailbox works. It also tells an agent that runs alone (a
// single-agent baseline) to play every role.
func teamSection(r record, rt *routes, deliverable string) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("## Your team\n\n")
	p("This task is for a team of %d agents: %s. Each member runs in its own container, on its own model credentials, and shares this workspace. Your own member id is in the environment variable `HEIMDALL_AGENT_ID`; `HEIMDALL_TEAM` lists the whole team. If `HEIMDALL_AGENT_ID` is not set, you are working alone: play every role below yourself and skip the mailbox.\n\n",
		len(rt.Members), strings.Join(rt.Members, ", "))
	p("`%s` is the lead: it collects the team's work and writes %s. The others send their results to the lead.\n\n", rt.Lead, deliverable)
	p("Member profiles (from the benchmark):\n\n")
	for _, a := range r.Agents {
		lead := ""
		if a.AgentID == rt.Lead {
			lead = " (lead)"
		}
		p("- `%s`%s: %s\n", a.AgentID, lead, oneLine(a.Profile))
	}
	p("\nWho may message whom (the benchmark's relationship graph; message no one else):\n\n")
	for _, m := range rt.Members {
		to := rt.Allowed[m]
		if len(to) == 0 {
			p("- `%s`: nobody\n", m)
			continue
		}
		p("- `%s`: %s\n", m, "`"+strings.Join(to, "`, `")+"`")
	}
	p("\nThe same is in `%s` in the workspace.\n\n", teamFile)
	p("Messages go only through the team mailbox at the URL in `HEIMDALL_MAILBOX_URL` (your own route to it):\n\n")
	p("- send: `POST $HEIMDALL_MAILBOX_URL/send` with JSON `{\"from\": \"<your id>\", \"to\": \"<member id>\", \"body\": \"<text>\"}`\n")
	p("- receive: `POST $HEIMDALL_MAILBOX_URL/recv` with JSON `{\"agent\": \"<your id>\"}`; it returns `{\"messages\": [...]}`, each message once\n\n")
	p("Do not pass messages through files in the workspace, and do not send to `*` unless every other member is one you may message.\n\n")
	return b.String()
}

// oneLine folds a profile to one line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// databaseInstruction is a database task's instruction.
func databaseInstruction(r record, rt *routes) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Diagnose a database performance anomaly (MultiAgentBench database task %d)\n\n", r.TaskID)
	p("%s\n\n", strings.TrimSpace(r.Task.Content))
	b.WriteString(teamSection(r, rt, "`"+artifactName+"`"))
	p("## The database\n\n")
	p("A PostgreSQL database (with `pg_stat_statements`) is reachable only through one tool, which runs SQL and returns the rows of the last statement:\n\n")
	p("- `POST <tool>/query` with JSON `{\"sql\": \"<SQL>\"}`; the answer has `status`, `columns`, `rows` and `explanation`\n")
	p("- `<tool>` is `${HEIMDALL_MAILBOX_URL%%/s/mailbox/}/s/%s` in a team (so the call is recorded as yours), else `http://toolproxy:9200/s/%s`\n\n", toolService, toolService)
	p("## What to deliver\n\n")
	p("%s\n\n", strings.TrimSpace(r.Task.OutputFormat))
	p("Write the decision to `%s` in the workspace. It must hold exactly one line of this form, naming at most %d of these labels: %s.\n\n",
		artifactName, r.Task.LabelsPred, "`"+strings.Join(r.Task.Labels, "`, `")+"`")
	p("```\nROOT_CAUSES: <label>, <label>\n```\n\n")
	p("Only that line is graded. Naming more labels than allowed fails the task.\n")
	return b.String()
}

// databaseRequirements maps each reward dimension the verifier writes to
// what the instruction asks (verifier/requirements.md, read by suite lint).
func databaseRequirements(r record) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Requirements: MultiAgentBench database task %d\n\n", r.TaskID)
	b.WriteString("The verifier (`verify.py`, no model) reads the `ROOT_CAUSES:` line of `" + artifactName + "` and writes these reward dimensions:\n\n")
	fmt.Fprintf(&b, "- `accuracy`: true root causes named over all true root causes (%s), upstream's task score. The task passes only at 1.\n", strings.Join(r.Task.RootCauses, ", "))
	fmt.Fprintf(&b, "- `over_limit`: 1 when more than %d causes are named (number_of_labels_pred); the reward is then 0.\n", r.Task.LabelsPred)
	b.WriteString("- `root_cause_line`, `labels_named`, `unknown_labels`: whether the line is there, how many of the task's labels and how many other labels it names.\n")
	b.WriteString("- `route_violations`: mailbox messages to a member the sender has no relationship with (a team run); any violation sets the reward to 0. `messages` and `senders` are reported only.\n")
	return b.String()
}

// writeSuite writes suite.yaml, the licence notice and the equal-budget
// experiment template. Tasks of an earlier import stay listed.
func (w *writer) writeSuite(tasks []string, at time.Time) error {
	if err := os.MkdirAll(w.suiteDir, 0o755); err != nil {
		return err
	}
	all, err := existingTasks(w.suiteDir)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if !slices.Contains(all, t) {
			all = append(all, t)
		}
	}
	quoted := make([]string, len(all))
	for i, t := range all {
		quoted[i] = q(t)
	}
	judged := scenarios[w.set.scenario].grading == schemas.GradingJudged
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Imported from MultiAgentBench (MARBLE) by `bifrost-bench import marble`; see docs/importers-marble.md.\n")
	p("# Upstream licence: MIT (see LICENSE.marble and NOTICE.md in this folder).\n")
	p("# scenario: %s\n", w.set.scenario)
	p("id: %s\nversion: %s\n", w.suiteID, w.set.version)
	if judged {
		p("description: %s\n", q(fmt.Sprintf("MultiAgentBench %s tasks as team tasks, graded by a judge model (upstream scores this scenario only with an LLM judge). Not comparable with the paper's scores.", w.set.scenario)))
	} else {
		p("description: %s\n", q("MultiAgentBench database tasks as team tasks: PostgreSQL and a query tool as services, the anomaly replayed before the agents start, the named root causes checked against the task's labels with no model. Not comparable with the paper's scores."))
	}
	p("environment: docker_workspace\n")
	if judged {
		p("grader: judge\n")
	} else {
		p("grader: verifier_command\n")
	}
	p("base_image:\n  tag: %s\n", q(baseImage))
	p("workdir: /workspace\n")
	if judged {
		p("output:\n  kind: answer\n  answer_path: %s\n", judgedAnswerPath(w.set.scenario))
		p("verifier:\n  command: [\"true\"]\n  restore: []\n  timeout_s: 60\n")
		j := pinnedJudge(w.suiteDir)
		p("judge:\n  provider: %s\n  model: %s\n  prompt_version: %s\n  votes: %d\n", q(j.Provider), q(j.Model), q(j.PromptVersion), j.Votes)
	} else {
		p("output:\n  kind: artifact\n")
		p("verifier:\n  command: [\"python3\", \"-B\", \"-I\", \"/grader/tests/verify.py\"]\n  result: reward\n  result_path: /grader/out/reward.json\n  pass_threshold: 1\n  restore: []\n  timeout_s: 120\n")
	}
	p("tasks: [%s]\n", strings.Join(quoted, ", "))
	p("probes: []\n")
	grading := schemas.GradingVerified
	if judged {
		grading = schemas.GradingJudged
	}
	track := schemas.TrackToolUse // database: the team works through the query tool
	if judged {
		track = schemas.TrackQA // research, coding: a written deliverable read by a judge
	}
	p("track: %s\ngrading: %s\n", track, grading)
	p("imported:\n  importer: %s\n  benchmark: %s\n  source: %s\n  revision: %s\n  license: %s\n  at: %s\n",
		Name, q(benchmarkName+"/"+w.set.scenario), q(upstreamRepo), q(w.src.revisionOr("unknown")), license, at.UTC().Format("2006-01-02T15:04:05Z"))
	if err := os.WriteFile(filepath.Join(w.suiteDir, "suite.yaml"), []byte(b.String()), 0o644); err != nil {
		return err
	}
	if err := w.writeNotice(); err != nil {
		return err
	}
	return w.writeExperiment(all)
}

// pinnedJudge is the judge a suite.yaml of an earlier import names, so a
// re-import keeps the judge a person pinned; else the placeholder judge.
func pinnedJudge(suiteDir string) schemas.JudgeSpec {
	out := schemas.JudgeSpec{Provider: placeholder, Model: placeholder, PromptVersion: judgePrompt, Votes: 1}
	b, err := os.ReadFile(filepath.Join(suiteDir, "suite.yaml"))
	if err != nil {
		return out
	}
	var prev struct {
		Judge *schemas.JudgeSpec `yaml:"judge"`
	}
	if yaml.Unmarshal(b, &prev) != nil || prev.Judge == nil || prev.Judge.Provider == "" || prev.Judge.Model == "" {
		return out
	}
	out.Provider, out.Model = prev.Judge.Provider, prev.Judge.Model
	if prev.Judge.Votes > 0 {
		out.Votes = prev.Judge.Votes
	}
	return out
}

// existingTasks reads the task ids a suite.yaml of an earlier import lists.
func existingTasks(suiteDir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(suiteDir, "suite.yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		rest, ok := strings.CutPrefix(line, "tasks: ")
		if !ok {
			continue
		}
		var ids []string
		if err := json.Unmarshal([]byte(rest), &ids); err != nil {
			return nil, fmt.Errorf("suite.yaml tasks: %w", err)
		}
		return ids, nil
	}
	return nil, nil
}

// upstreamLicense is the MIT licence as the repository's LICENSE states it
// at the checked revision, used when the source has no LICENSE file.
const upstreamLicense = `MIT License

Copyright (c) 2024 Haofei Yu

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
`

// writeNotice writes LICENSE.marble (the upstream LICENSE file of the
// clone, else the text checked at the pinned revision) and NOTICE.md.
func (w *writer) writeNotice() error {
	lic := upstreamLicense
	from := "the text of " + upstreamRepo + "/blob/eae9f15e5b9f1e808f8ff61800b129d5538c1b47/LICENSE (the source had no LICENSE file)"
	if w.src.root != "" {
		if b, err := os.ReadFile(filepath.Join(w.src.root, "LICENSE")); err == nil {
			lic, from = string(b), "LICENSE of the imported clone"
		}
	}
	if err := os.WriteFile(filepath.Join(w.suiteDir, "LICENSE.marble"), []byte(lic), 0o644); err != nil {
		return err
	}
	notice := fmt.Sprintf(`# Notice

The tasks in this suite are derived from MultiAgentBench, the benchmark of the
MARBLE repository (%s, revision %s), described in "MultiAgentBench:
Evaluating the Collaboration and Competition of LLM agents" (Zhu et al.,
%s). MARBLE is released under the MIT licence; LICENSE.marble in this
folder is %s.

What was taken: each task's text, output format, agents and profiles,
relationships and, for the database scenario, the init SQL, the anomaly
settings, the labels and the true root causes. What was written here: the
instructions' team and tool sections, the verifier, the controls, the
database tool and its anomaly replica (docs/importers-marble.md).

This folder is git-ignored and must not be committed: it holds upstream task
data.
`, upstreamRepo, w.src.revisionOr("unknown"), paperURL, from)
	return os.WriteFile(filepath.Join(w.suiteDir, "NOTICE.md"), []byte(notice), 0o644)
}
