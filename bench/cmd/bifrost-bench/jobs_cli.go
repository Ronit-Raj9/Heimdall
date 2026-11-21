package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/importers"
)

// Job kinds that run a CLI command of this binary (web parity, lane L02).
// Every path a param names is relative to the org's project root and may
// not leave it; ids are plain names. Kinds that start containers are
// local only.
func init() {
	registerJobKind(cliJob("import.run", true, true, importRunArgs))
	registerJobKind(cliJob("tasks.mine", true, false, tasksMineArgs))
	registerJobKind(cliJob("export.run", true, true, exportRunArgs))
	registerJobKind(cliJob("scan.skill", true, true, scanSkillArgs))
	registerJobKind(cliJob("scan.mcp", true, false, scanMCPArgs))
}

// cliIDPattern is a suite, experiment or task id a CLI job accepts.
var cliIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// decodeParams reads params strictly: an unknown field is an error, so a
// typo is reported instead of silently ignored.
func decodeParams(raw json.RawMessage, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("params: %v", err)
	}
	return nil
}

func checkID(field, v string, required bool) error {
	if v == "" {
		if required {
			return fmt.Errorf("%s is required", field)
		}
		return nil
	}
	if !cliIDPattern.MatchString(v) || strings.Contains(v, "..") {
		return fmt.Errorf("%s %q is not a valid id (letters, digits, '.', '_', '-')", field, v)
	}
	return nil
}

// checkRelPath accepts a project-relative path that stays inside the
// project root and returns it cleaned (forward slashes).
func checkRelPath(field, v string, required bool) (string, error) {
	if v == "" {
		if required {
			return "", fmt.Errorf("%s is required (a path inside the project, for example uploads/my-bench)", field)
		}
		return "", nil
	}
	if len(v) > 512 || strings.ContainsRune(v, 0) || strings.HasPrefix(v, "-") {
		return "", fmt.Errorf("%s %q is not a valid path", field, v)
	}
	clean := filepath.Clean(filepath.FromSlash(v))
	if !filepath.IsLocal(clean) {
		return "", fmt.Errorf("%s %q must be a relative path inside the project", field, v)
	}
	for _, part := range strings.Split(filepath.ToSlash(clean), "/") {
		if part == ".git" || part == ".heimdall" || part == jobConfigDir {
			return "", fmt.Errorf("%s %q points into a protected folder", field, v)
		}
	}
	return filepath.ToSlash(clean), nil
}

func checkIDList(field string, ids []string) error {
	if len(ids) > 1000 {
		return fmt.Errorf("%s: at most 1000 ids", field)
	}
	for _, id := range ids {
		if err := checkID(field, id, true); err != nil {
			return err
		}
	}
	return nil
}

// importRunArgs: {format, from, suite, ids?, limit?, force?} ->
// import <format> -from <from> -suite <suite> ...
func importRunArgs(raw json.RawMessage) ([]string, error) {
	var p struct {
		Format string   `json:"format"`
		From   string   `json:"from"`
		Suite  string   `json:"suite"`
		IDs    []string `json:"ids"`
		Limit  int      `json:"limit"`
		Force  bool     `json:"force"`
	}
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Format == "" {
		return nil, errors.New("format is required (GET /benchmarks/catalog lists the importers)")
	}
	if _, ok := importers.Get(p.Format); !ok {
		return nil, fmt.Errorf("unknown import format %q", p.Format)
	}
	from, err := checkRelPath("from", p.From, true)
	if err != nil {
		return nil, err
	}
	if err := checkID("suite", p.Suite, true); err != nil {
		return nil, err
	}
	if err := checkIDList("ids", p.IDs); err != nil {
		return nil, err
	}
	if p.Limit < 0 {
		return nil, errors.New("limit must be 0 (all) or more")
	}
	argv := []string{"import", p.Format, "-config", cliConfig, "-from", from, "-suite", p.Suite}
	if len(p.IDs) > 0 {
		argv = append(argv, "-ids", strings.Join(p.IDs, ","))
	}
	if p.Limit > 0 {
		argv = append(argv, "-limit", strconv.Itoa(p.Limit))
	}
	if p.Force {
		argv = append(argv, "-force")
	}
	return argv, nil
}

// sincePattern is a git ref or a YYYY-MM-DD date.
var sincePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@{}~^-]{0,127}$`)

// tasksMineArgs: {suite, repo?, since?, paths?, limit?, dry_run?} -> tasks mine ...
func tasksMineArgs(raw json.RawMessage) ([]string, error) {
	var p struct {
		Suite  string   `json:"suite"`
		Repo   string   `json:"repo"`
		Since  string   `json:"since"`
		Paths  []string `json:"paths"`
		Limit  int      `json:"limit"`
		DryRun bool     `json:"dry_run"`
	}
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := checkID("suite", p.Suite, true); err != nil {
		return nil, err
	}
	repo, err := checkRelPath("repo", p.Repo, false)
	if err != nil {
		return nil, err
	}
	if repo == "" {
		repo = "."
	}
	if p.Since != "" && !sincePattern.MatchString(p.Since) {
		return nil, fmt.Errorf("since %q must be a commit, tag, branch or YYYY-MM-DD date", p.Since)
	}
	var paths []string
	for _, x := range p.Paths {
		c, err := checkRelPath("paths", x, true)
		if err != nil {
			return nil, err
		}
		paths = append(paths, c)
	}
	if p.Limit < 0 || p.Limit > 100 {
		return nil, errors.New("limit must be between 1 and 100 (0 uses the default 10)")
	}
	argv := []string{"tasks", "mine", "-config", cliConfig, "-suite", p.Suite, "-repo", repo}
	if p.Since != "" {
		argv = append(argv, "-since", p.Since)
	}
	if len(paths) > 0 {
		argv = append(argv, "-paths", strings.Join(paths, ","))
	}
	if p.Limit > 0 {
		argv = append(argv, "-limit", strconv.Itoa(p.Limit))
	}
	if p.DryRun {
		argv = append(argv, "-dry-run")
	}
	return argv, nil
}

// calibrateRunArgs: {suite, yes, seed_controls?, from_reviews_exps?} ->
// judge calibrate <suite> -yes ... Judge calls cost money, so yes must be
// true: the UI asks first. The calibrate.run job kind is now in process in
// both modes (jobs_cli_calibrate.go, G06); this stays the CLI arg contract.
func calibrateRunArgs(raw json.RawMessage) ([]string, error) {
	var p struct {
		Suite        string   `json:"suite"`
		Yes          bool     `json:"yes"`
		SeedControls bool     `json:"seed_controls"`
		FromReviews  []string `json:"from_reviews_exps"`
		Policy       string   `json:"policy"`
	}
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := checkID("suite", p.Suite, true); err != nil {
		return nil, err
	}
	if !p.Yes {
		return nil, errors.New("judge calibration calls the judge model and costs money: set yes to true to confirm")
	}
	if err := checkIDList("from_reviews_exps", p.FromReviews); err != nil {
		return nil, err
	}
	policy, err := checkRelPath("policy", p.Policy, false)
	if err != nil {
		return nil, err
	}
	argv := []string{"judge", "calibrate", p.Suite, "-config", cliConfig, "-yes"}
	if p.SeedControls {
		argv = append(argv, "-seed-controls")
	}
	if len(p.FromReviews) > 0 {
		argv = append(argv, "-from-reviews")
		for _, e := range p.FromReviews {
			argv = append(argv, "-exp", e)
		}
	}
	if policy != "" {
		argv = append(argv, "-policy", policy)
	}
	return argv, nil
}

// exportFormats are the formats export.run writes, with the file suffix.
var exportFormats = map[string]string{"jsonl": ".jsonl", "csv": ".csv", "eee": ".eee.jsonl", "otlp": ".otlp.json"}

// exportRunArgs: {format, exp_id, grade_source?, split?} -> export <format>
// -exp <id> -out exports/<id><suffix>. The output always lands in the
// project's exports/ folder.
func exportRunArgs(raw json.RawMessage) ([]string, error) {
	var p struct {
		Format      string `json:"format"`
		ExpID       string `json:"exp_id"`
		GradeSource string `json:"grade_source"`
		Split       string `json:"split"`
	}
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	suffix, ok := exportFormats[p.Format]
	if !ok {
		return nil, fmt.Errorf("format %q: use jsonl, csv, eee or otlp", p.Format)
	}
	if err := checkID("exp_id", p.ExpID, true); err != nil {
		return nil, err
	}
	if err := checkID("grade_source", p.GradeSource, false); err != nil {
		return nil, err
	}
	switch p.Split {
	case "", "public", "held_out", "all":
	default:
		return nil, fmt.Errorf("split %q: use public, held_out or all", p.Split)
	}
	argv := []string{"export", p.Format, "-config", cliConfig, "-exp", p.ExpID, "-out", "exports/" + p.ExpID + suffix}
	if p.GradeSource != "" {
		argv = append(argv, "-grade-source", p.GradeSource)
	}
	if p.Split != "" && (p.Format == "jsonl" || p.Format == "csv") {
		argv = append(argv, "-split", p.Split)
	}
	return argv, nil
}

// scanSkillArgs: {path} -> skill scan <path> -json (static scan only; the
// dynamic scan starts a container).
func scanSkillArgs(raw json.RawMessage) ([]string, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	path, err := checkRelPath("path", p.Path, true)
	if err != nil {
		return nil, err
	}
	return []string{"skill", "scan", "-config", cliConfig, "-json", path}, nil
}

// scanMCPArgs: {path} -> mcp scan <service config> -json (runs the server in
// a sandbox container: local only).
func scanMCPArgs(raw json.RawMessage) ([]string, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	path, err := checkRelPath("path", p.Path, true)
	if err != nil {
		return nil, err
	}
	return []string{"mcp", "scan", "-config", cliConfig, "-json", path}, nil
}
