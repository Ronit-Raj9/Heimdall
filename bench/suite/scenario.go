package suite

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// reservedServiceIDs are host names the bench itself uses on a run's network.
var reservedServiceIDs = map[string]bool{"toolproxy": true}

// hostNameRE is one RFC 1123 label: a service id is the host name other
// containers of the run reach it by.
var hostNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// foldedKey is the seen-map key of a service id folded the way the gateway
// folds it into an MCP client name (every run of hyphens to one
// underscore), so two ids that fold alike are refused (3-A-10). The NUL
// prefix keeps it apart from real ids.
func foldedKey(id string) string {
	return "\x00fold:" + strings.Trim(hyphenRunRE.ReplaceAllString(id, "_"), "_")
}

var hyphenRunRE = regexp.MustCompile(`-+`)

// answerGraders are the graders that can grade an answer task.
var answerGraders = map[string]bool{"answer_match": true, "judge": true, "verifier_command": true, "checks": true}

// defaultAnswerSpec is the hidden answer file looked for in the verifier tests
// folder when a task names none.
const defaultAnswerSpec = "answer.yaml"

// scenarioHashParts is scenario content of one task, as hash key -> sha256,
// grouped by the task lock hash it is folded into: instruction (sessions),
// repo (the task's own image), verifier (answer file, output, impossible
// flag, the suite's output and verifier mode) and fixtures (services and
// skills, the suite's services included).
type scenarioHashParts struct {
	instruction, repo, verifier, fixtures map[string]string
}

// scenarioParts hashes the scenario content of t. Every map is empty for a
// task and suite without scenario fields.
func scenarioParts(s *schemas.Suite, t *schemas.Task) (scenarioHashParts, error) {
	p := scenarioHashParts{instruction: map[string]string{}, repo: map[string]string{}, verifier: map[string]string{}, fixtures: map[string]string{}}
	var errs []error
	put := func(m map[string]string, key string, sum string, err error) {
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
			return
		}
		m[key] = sum
	}
	if env := t.Environment; env != nil {
		sum, err := utils.HashJSON(env)
		put(p.repo, "environment", sum, err)
		if env.Dockerfile != "" {
			sum, err = fileHash(t.Dir, env.Dockerfile)
			put(p.repo, "environment.dockerfile", sum, err)
			sum, err = treeHashRel(t.Dir, buildContext(env.Dockerfile, env.Context))
			put(p.repo, "environment.context", sum, err)
		}
	}
	for i, sess := range t.Sessions {
		key := fmt.Sprintf("sessions[%d]", i)
		sum, err := utils.HashJSON(sess)
		put(p.instruction, key, sum, err)
		sum, err = fileHash(t.Dir, sess.Instruction)
		put(p.instruction, key+".instruction", sum, err)
		if sess.Files != "" {
			sum, err = treeHashRel(t.Dir, sess.Files)
			put(p.instruction, key+".files", sum, err)
		}
	}
	if s != nil {
		errs = append(errs, serviceParts(p.fixtures, "suite.services", s.Root, s.Services)...)
	}
	errs = append(errs, serviceParts(p.fixtures, "services", t.Dir, t.Services)...)
	if s != nil {
		errs = append(errs, serviceCode(p.fixtures, "suite.services.code", s.Root, utils.TaskServices(s, t))...)
	}
	errs = append(errs, serviceCode(p.fixtures, "services.code", t.Dir, t.Services)...)
	errs = append(errs, externalParts(p.fixtures, s, t)...) // External APIs area: specs and cassettes (external.go)
	if t.Compose != "" {                                    // X2: the compose file itself (compose.go)
		sum, err := fileHash(t.Dir, t.Compose)
		put(p.fixtures, "compose", sum, err)
	}
	if t.Skills != "" {
		sum, err := treeHashRel(t.Dir, t.Skills)
		put(p.fixtures, "skills", sum, err)
	}
	if t.Verifier.Answer != "" {
		sum, err := fileHash(t.Dir, t.Verifier.Answer)
		put(p.verifier, "verifier.answer", sum, err)
	}
	if t.Output != nil {
		sum, err := utils.HashJSON(t.Output)
		put(p.verifier, "output", sum, err)
	}
	if t.Impossible {
		p.verifier["impossible"] = utils.SHA256Bytes([]byte("true"))
	}
	if t.Verifier.OriginalTests != "" {
		sum, err := treeHashRel(t.Dir, t.Verifier.OriginalTests)
		put(p.verifier, "verifier.original_tests", sum, err)
	}
	if t.Controls.Honest != "" {
		sum, err := fileHash(t.Dir, t.Controls.Honest)
		put(p.verifier, "controls.honest", sum, err)
	}
	if s != nil && s.Output != nil {
		sum, err := utils.HashJSON(s.Output)
		put(p.verifier, "suite.output", sum, err)
	}
	if s != nil && s.Verifier.Mode != "" {
		p.verifier["suite.verifier.mode"] = utils.SHA256Bytes([]byte(s.Verifier.Mode))
	}
	return p, errors.Join(errs...)
}

// serviceParts hashes each service's spec, its seeded files and its build
// (Dockerfile and context), relative to base.
func serviceParts(out map[string]string, prefix, base string, services []schemas.ServiceSpec) []error {
	var errs []error
	for i, sv := range services {
		key := fmt.Sprintf("%s[%d]", prefix, i)
		sum, err := utils.HashJSON(sv)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
			continue
		}
		out[key] = sum
		for _, dst := range sortedKeys(sv.Files) {
			sum, err := fileHash(base, sv.Files[dst])
			if err != nil {
				errs = append(errs, fmt.Errorf("%s.files[%s]: %w", key, dst, err))
				continue
			}
			out[key+".files["+dst+"]"] = sum
		}
		if sv.Build != nil && sv.Build.Dockerfile != "" {
			if sum, err := fileHash(base, sv.Build.Dockerfile); err != nil {
				errs = append(errs, fmt.Errorf("%s.build.dockerfile: %w", key, err))
			} else {
				out[key+".build.dockerfile"] = sum
			}
			if sum, err := treeHashRel(base, buildContext(sv.Build.Dockerfile, sv.Build.Context)); err != nil {
				errs = append(errs, fmt.Errorf("%s.build.context: %w", key, err))
			} else {
				out[key+".build.context"] = sum
			}
		}
	}
	return errs
}

// serviceCode hashes the folder services/<id>/ under base of every service
// that has one: the source its image is built from by convention (`suite
// admit` builds a missing service image from it), so editing a tool server
// changes the lock even when the service names a prebuilt tag.
func serviceCode(out map[string]string, prefix, base string, services []schemas.ServiceSpec) []error {
	var errs []error
	seen := map[string]bool{}
	for _, sv := range services {
		if sv.ID == "" || seen[sv.ID] {
			continue
		}
		seen[sv.ID] = true
		dir, err := resolveIn(base, path.Join("services", sv.ID))
		if err != nil {
			continue
		}
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			continue
		}
		sum, err := utils.TreeHash(dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s[%s]: %w", prefix, sv.ID, err))
			continue
		}
		out[prefix+"["+sv.ID+"]"] = sum
	}
	return errs
}

// foldHash returns base when parts is empty, so content without scenario
// fields keeps its hash, and otherwise the sha256 of base followed by every
// part in key order.
func foldHash(base string, parts map[string]string) string {
	if len(parts) == 0 {
		return base
	}
	var buf bytes.Buffer
	buf.WriteString(base)
	buf.WriteByte('\n')
	for _, k := range sortedKeys(parts) {
		buf.WriteString(k)
		buf.WriteByte(0)
		buf.WriteString(parts[k])
		buf.WriteByte('\n')
	}
	return utils.SHA256Bytes(buf.Bytes())
}

// buildContext is a build's context folder: context when set, else the
// Dockerfile's folder.
func buildContext(dockerfile, context string) string {
	if context != "" {
		return context
	}
	dir := path.Dir(filepath.ToSlash(dockerfile))
	if dir == "" {
		return "."
	}
	return dir
}

func fileHash(base, rel string) (string, error) {
	abs, err := resolveIn(base, rel)
	if err != nil {
		return "", err
	}
	return utils.SHA256File(abs)
}

// lintScenarioSuite checks the suite's scenario fields: a judged suite
// names its judge and only a judged suite uses the judge grader, and every
// suite service is well formed.
func lintScenarioSuite(s *schemas.Suite) []Problem {
	var out []Problem
	add := func(sev, format string, args ...any) {
		out = append(out, Problem{Path: SuiteFile, Severity: sev, Message: fmt.Sprintf(format, args...)})
	}
	grading := utils.Grading(s)
	switch {
	case grading == schemas.GradingJudged && s.Judge == nil && !utils.VisualJudged(s): // X1 vision grading: a rubric's pinned vision judge stands in
		add(SeverityError, "grading: judged needs a judge (provider, model, prompt_version)")
	case grading != schemas.GradingJudged && s.Grader == "judge":
		add(SeverityError, "grader: judge decides outcomes, so the suite must declare grading: judged")
	case grading != schemas.GradingJudged && s.Judge != nil:
		add(SeverityWarning, "judge: set, but grading is %s; the judge is never asked", grading)
	}
	if s.Judge != nil && s.Judge.Votes > 0 && s.Judge.Votes%2 == 0 {
		add(SeverityError, "judge.votes: %d is even; the majority needs an odd number of votes", s.Judge.Votes)
	}
	if a := lintJudgeAuditor(s.Judge); a != "" { // W8 judge calibration: the auditor comes from another vendor
		add(SeverityError, "%s", a)
	}
	if msg := lintCustomJudgePrompt(s); msg != "" { // custom judge prompt (graders/judgecustom.go)
		add(SeverityError, "%s", msg)
	}
	seen := map[string]string{}
	out = append(out, lintServices(SuiteFile, "services", s.Root, s.Services, seen)...)
	out = append(out, lintExternalSuite(s)...)  // External APIs area (external.go)
	out = append(out, lintSimulatorSuite(s)...) // W3 customer support: the user simulator (simulatorlint.go)
	if o := s.Output; o != nil {
		if o.Kind == schemas.OutputArtifact && s.Grader != "verifier_command" && !visualReadsArtifacts(s.Grader) { // X1 vision grading: grader visual reads artifacts too
			add(SeverityError, "output: artifact files are read by verifier_command (from %s), not by %s", utils.GraderArtifactsDir, s.Grader)
		}
		out = append(out, lintArtifacts(SuiteFile, "output", o.Kind, o.Artifacts)...)
	}
	return out
}

// lintArtifacts checks the artifacts one file declares: at most
// utils.MaxArtifactSpecs, ids unique, each spec well formed
// (utils.CheckArtifactSpec), and a warning when they are declared under an
// output kind that never reads them.
func lintArtifacts(file, field, kind string, specs []schemas.ArtifactSpec) []Problem {
	var out []Problem
	add := func(sev, format string, args ...any) {
		out = append(out, Problem{Path: file, Severity: sev, Message: fmt.Sprintf(format, args...)})
	}
	if len(specs) == 0 {
		return nil
	}
	if kind != schemas.OutputArtifact {
		k := kind
		if k == "" {
			k = "unset"
		}
		add(SeverityWarning, "%s.artifacts: declared, but output kind is %s; only output kind artifact exports them", field, k)
	}
	if len(specs) > utils.MaxArtifactSpecs {
		add(SeverityError, "%s.artifacts: %d declared, at most %d", field, len(specs), utils.MaxArtifactSpecs)
	}
	ids := map[string]bool{}
	for i, a := range specs {
		if a.ID != "" && ids[a.ID] {
			add(SeverityError, "%s.artifacts[%d].id: %q is used by an earlier artifact", field, i, a.ID)
		}
		ids[a.ID] = true
		if err := utils.CheckArtifactSpec(a); err != nil {
			add(SeverityError, "%s.artifacts[%d]: %v", field, i, err)
		}
	}
	return out
}

// lintScenarioTask checks one task's scenario fields: the output kind has a
// grader that reads it, a state task exports something, services are well
// formed and their ids unique across the suite and the task, sessions are
// unique, every path stays inside the task folder and exists, and the
// controls fit the output kind.
func lintScenarioTask(s *schemas.Suite, t *schemas.Task) []Problem {
	root := s.Root
	taskYAML := rel(root, filepath.Join(t.Dir, TaskFile))
	var out []Problem
	add := func(sev, format string, args ...any) {
		out = append(out, Problem{Path: taskYAML, Severity: sev, Message: fmt.Sprintf(format, args...)})
	}
	services := utils.TaskServices(s, t)
	switch utils.OutputKind(s, t) {
	case schemas.OutputAnswer:
		if !answerGraders[s.Grader] {
			add(SeverityError, "output: answer needs grader answer_match, judge, verifier_command or checks; the suite uses %s", s.Grader)
		}
		if s.Grader == "answer_match" && !hasAnswerSpec(t) {
			add(SeverityError, "verifier.answer: the answer_match grader needs the hidden answer (verifier.answer, or %s in %s)", defaultAnswerSpec, t.Verifier.Tests)
		}
	case schemas.OutputState:
		if !utils.HasExports(services) {
			add(SeverityError, "output: state needs at least one service export; no service of the suite or the task declares export")
		}
		if s.Grader == "hidden_tests" {
			add(SeverityError, "output: state is read by verifier_command (from /grader/state), not by hidden_tests")
		}
	case schemas.OutputArtifact:
		// A suite-level kind artifact with the wrong grader is reported once,
		// against suite.yaml (lintScenarioSuite).
		if s.Grader != "verifier_command" && !visualReadsArtifacts(s.Grader) && t.Output != nil && t.Output.Kind == schemas.OutputArtifact { // X1 vision grading: grader visual reads artifacts too
			add(SeverityError, "output: artifact files are read by verifier_command (from %s), not by %s", utils.GraderArtifactsDir, s.Grader)
		}
		if len(utils.OutputArtifacts(s, t)) == 0 {
			add(SeverityError, "output: artifact needs at least one artifact (output.artifacts in task.yaml or %s)", SuiteFile)
		}
	}
	if t.Output != nil {
		out = append(out, lintArtifacts(taskYAML, "output", utils.OutputKind(s, t), t.Output.Artifacts)...)
	}
	out = append(out, lintVisualTask(s, t)...) // X1 vision grading (visuallint.go)
	if t.Environment != nil && t.Environment.Image == "" && t.Environment.Dockerfile == "" {
		add(SeverityError, "environment: needs image or dockerfile")
	}
	switch {
	case t.Controls.Honest != "" && t.Verifier.OriginalTests == "":
		add(SeverityError, "controls.honest: needs verifier.original_tests, the tests the honest solution must pass")
	case t.Verifier.OriginalTests != "" && t.Controls.Honest == "":
		add(SeverityWarning, "verifier.original_tests: set, but no controls.honest; admission cannot check the task is possible without its mutation")
	case t.Controls.Honest != "" && !t.Impossible:
		add(SeverityWarning, "controls.honest: only an impossible task (impossible: true) uses it")
	}
	seen := map[string]string{}
	for _, sv := range s.Services {
		seen[sv.ID] = SuiteFile
		if seen[foldedKey(sv.ID)] == "" {
			seen[foldedKey(sv.ID)] = sv.ID
		}
	}
	out = append(out, lintServices(taskYAML, "services", t.Dir, t.Services, seen)...)
	out = append(out, lintExternalTask(s, t, taskYAML)...) // External APIs area (external.go)
	out = append(out, lintSimulatorTask(t, taskYAML)...)   // W3 customer support: the simulated user's file (simulatorlint.go)
	sessions := map[string]bool{}
	for i, sess := range t.Sessions {
		if sessions[sess.ID] {
			add(SeverityError, "sessions[%d].id: %q is used by an earlier session", i, sess.ID)
		}
		sessions[sess.ID] = true
	}
	out = append(out, scenarioPathProblems(root, t)...)
	out = append(out, lintControlKind(s, t, taskYAML)...)
	if t.Skills != "" {
		out = append(out, lintSkills(root, t)...)
	}
	out = append(out, lintCompose(s, t, taskYAML)...) // X2: what the compose mapping changed or ignored (compose.go)
	return out
}

// lintServices checks services declared in one file, relative to base: ids
// unique (seen holds ids of earlier files and gains these), valid host names
// and not reserved, an image or a build, a ready check with exactly one of
// argv or http (http needs a port), absolute container paths, export ids
// unique with exactly one of argv or path, and seeded files inside base.
func lintServices(file, field, base string, services []schemas.ServiceSpec, seen map[string]string) []Problem {
	var out []Problem
	add := func(sev, format string, args ...any) {
		out = append(out, Problem{Path: file, Severity: sev, Message: fmt.Sprintf(format, args...)})
	}
	for i, sv := range services {
		at := fmt.Sprintf("%s[%d]", field, i)
		switch {
		case !hostNameRE.MatchString(sv.ID):
			add(SeverityError, "%s.id: %q is not a valid host name (lower-case letters, digits and '-', at most 63)", at, sv.ID)
		case reservedServiceIDs[sv.ID]:
			add(SeverityError, "%s.id: %q is reserved for the bench's tool proxy", at, sv.ID)
		case seen[sv.ID] != "":
			add(SeverityError, "%s.id: %q is already a service in %s", at, sv.ID, seen[sv.ID])
		case seen[foldedKey(sv.ID)] != "":
			add(SeverityError, "%s.id: %q differs from service %s only in its hyphens, so both get one gateway MCP client name", at, sv.ID, seen[foldedKey(sv.ID)])
		}
		if sv.ID != "" && seen[sv.ID] == "" {
			seen[sv.ID] = file
			if seen[foldedKey(sv.ID)] == "" {
				seen[foldedKey(sv.ID)] = sv.ID
			}
		}
		if sv.Image == "" && sv.Build == nil {
			add(SeverityError, "%s: needs image or build", at)
		}
		if r := sv.Ready; r != nil {
			switch {
			case (len(r.Argv) > 0) == (r.HTTP != ""):
				add(SeverityError, "%s.ready: set exactly one of argv or http", at)
			case r.HTTP != "" && sv.Port == 0:
				add(SeverityError, "%s.ready.http: needs the service's port", at)
			}
		}
		for _, dst := range sortedKeys(sv.Files) {
			if !path.IsAbs(dst) {
				add(SeverityError, "%s.files: container path %q must be absolute", at, dst)
			}
			if err := checkFile(base, sv.Files[dst], false); err != nil {
				add(SeverityError, "%s.files[%s]: %v", at, dst, err)
			}
		}
		if sv.Build != nil && sv.Build.Dockerfile != "" {
			if err := checkFile(base, sv.Build.Dockerfile, false); err != nil {
				add(SeverityError, "%s.build.dockerfile: %v", at, err)
			}
		}
		exports := map[string]bool{}
		for j, ex := range sv.Export {
			if exports[ex.ID] {
				add(SeverityError, "%s.export[%d].id: %q is used twice", at, j, ex.ID)
			}
			exports[ex.ID] = true
			if (len(ex.Argv) > 0) == (ex.Path != "") {
				add(SeverityError, "%s.export[%d]: set exactly one of argv or path", at, j)
			}
		}
		if sv.Role == schemas.ServiceRoleMCP && sv.Port == 0 {
			add(SeverityError, "%s: an mcp service needs the port its endpoint listens on", at)
		}
	}
	return out
}

// scenarioPathProblems checks that every scenario path of t stays inside the
// task folder and exists: session instructions and files, skills, the
// environment's Dockerfile and context, and the hidden answer.
func scenarioPathProblems(root string, t *schemas.Task) []Problem {
	taskYAML := rel(root, filepath.Join(t.Dir, TaskFile))
	var out []Problem
	check := func(field, p string, isDir bool) {
		if err := checkFile(t.Dir, p, isDir); err != nil {
			out = append(out, Problem{Path: taskYAML, Severity: SeverityError, Message: fmt.Sprintf("%s: %v", field, err)})
		}
	}
	for i, sess := range t.Sessions {
		check(fmt.Sprintf("sessions[%d].instruction", i), sess.Instruction, false)
		if sess.Files != "" {
			check(fmt.Sprintf("sessions[%d].files", i), sess.Files, true)
		}
	}
	if t.Skills != "" {
		check("skills", t.Skills, true)
	}
	if env := t.Environment; env != nil && env.Dockerfile != "" {
		check("environment.dockerfile", env.Dockerfile, false)
		check("environment.context", buildContext(env.Dockerfile, env.Context), true)
	}
	if t.Verifier.Answer != "" {
		check("verifier.answer", t.Verifier.Answer, false)
	}
	if t.Verifier.OriginalTests != "" {
		check("verifier.original_tests", t.Verifier.OriginalTests, true)
	}
	if t.Controls.Honest != "" {
		check("controls.honest", t.Controls.Honest, false)
	}
	return out
}

// lintControlKind checks that the controls are what the output kind needs:
// scripts for a script-control or artifact task, and a readable reference
// answer for an answer task.
func lintControlKind(s *schemas.Suite, t *schemas.Task, taskYAML string) []Problem {
	var out []Problem
	add := func(sev, format string, args ...any) {
		out = append(out, Problem{Path: taskYAML, Severity: sev, Message: fmt.Sprintf(format, args...)})
	}
	if utils.OutputKind(s, t) == schemas.OutputArtifact {
		// The artifact files are what the agent leaves in the workspace, so
		// every control is a script run in the agent's place that leaves them
		// (or fails to).
		const why = "is not a script; an artifact task's controls run in the agent's place and leave the artifact files"
		if !strings.HasSuffix(t.Controls.Reference, ".sh") {
			add(SeverityError, "controls.reference: %s %s", t.Controls.Reference, why)
		}
		if t.Controls.Empty != "" && !strings.HasSuffix(t.Controls.Empty, ".sh") {
			add(SeverityError, "controls.empty: %s %s", t.Controls.Empty, why)
		}
		for i, m := range t.Controls.Mutants {
			if !strings.HasSuffix(m, ".sh") {
				add(SeverityError, "controls.mutants[%d]: %s %s", i, m, why)
			}
		}
		return out
	}
	switch utils.ControlKind(s, t) {
	case utils.ControlScript:
		for i, m := range t.Controls.Mutants {
			if !strings.HasSuffix(m, ".sh") {
				add(SeverityError, "controls.mutants[%d]: %s is not a script; the reference is a script, so every mutant runs in the agent's place too", i, m)
			}
		}
	case utils.ControlAnswer:
		if strings.HasSuffix(t.Controls.Reference, ".diff") || strings.HasSuffix(t.Controls.Reference, ".patch") {
			add(SeverityError, "controls.reference: %s is a patch; an answer task's reference is a file holding the answer", t.Controls.Reference)
		}
	case utils.ControlPatch:
		if utils.OutputKind(s, t) == schemas.OutputState || utils.VerifierMode(s) == schemas.VerifierSnapshot {
			add(SeverityWarning, "controls.reference: a patch only changes the workspace; a state or snapshot task usually needs a script reference (.sh) run with the services up")
		}
	}
	return out
}

// lintSkills warns about skill folders without a SKILL.md.
func lintSkills(root string, t *schemas.Task) []Problem {
	dir, err := resolveIn(t.Dir, t.Skills)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var missing []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "SKILL.md")); err != nil {
			missing = append(missing, e.Name())
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return []Problem{{Path: rel(root, dir), Severity: SeverityWarning,
		Message: fmt.Sprintf("skill folders without SKILL.md: %s; an agent only loads a folder that holds one", strings.Join(missing, ", "))}}
}

// hasAnswerSpec reports whether the task has a hidden answer file.
func hasAnswerSpec(t *schemas.Task) bool {
	if t.Verifier.Answer != "" {
		return true
	}
	abs, err := resolveIn(t.Dir, path.Join(filepath.ToSlash(t.Verifier.Tests), defaultAnswerSpec))
	if err != nil {
		return false
	}
	fi, err := os.Stat(abs)
	return err == nil && fi.Mode().IsRegular()
}

// scenarioLoadErrors turns the error problems of scenarioPathProblems and of
// the suite's and task's services into load errors, so a task that names a
// missing or escaping scenario file never loads (and is never hashed).
func scenarioLoadErrors(t *schemas.Task, file string) []error {
	var errs []error
	for _, p := range scenarioPathProblems(filepath.Dir(t.Dir), t) {
		if p.Severity == SeverityError {
			errs = append(errs, fmt.Errorf("%s: %s", file, p.Message))
		}
	}
	for i, sv := range t.Services {
		for _, dst := range sortedKeys(sv.Files) {
			if err := checkFile(t.Dir, sv.Files[dst], false); err != nil {
				errs = append(errs, fmt.Errorf("%s: services[%d].files[%s]: %w", file, i, dst, err))
			}
		}
	}
	return errs
}

// repoInImage reports whether the task's workspace is seeded from a folder of
// its own image (environment.workspace_path), so repo_dir does not hold the
// repository the agent sees.
func repoInImage(t *schemas.Task) bool {
	return t.Environment != nil && t.Environment.WorkspacePath != ""
}

// lintCustomJudgePrompt checks a custom-v1 judge: its prompt file is in the
// suite folder, hashes to the pinned prompt_sha256 and shows the judge the
// response.
func lintCustomJudgePrompt(s *schemas.Suite) string {
	j := s.Judge
	if j == nil || j.PromptVersion != "custom-v1" {
		return ""
	}
	switch {
	case j.PromptFile == "":
		return "judge.prompt_file: custom-v1 needs the prompt file in the suite folder"
	case j.PromptSHA256 == "":
		return "judge.prompt_sha256: custom-v1 pins its prompt file by sha256"
	}
	b, err := utils.ReadFileInside(s.Root, j.PromptFile)
	if err != nil {
		return fmt.Sprintf("judge.prompt_file: %v", err)
	}
	if got := utils.SHA256Bytes(b); got != j.PromptSHA256 {
		return fmt.Sprintf("judge.prompt_sha256: %s hashes to %s, not the pinned %s; review the change and update the pin", j.PromptFile, got, j.PromptSHA256)
	}
	if !strings.Contains(string(b), "{{response}}") {
		return fmt.Sprintf("judge.prompt_file: %s has no {{response}}, so the judge would never see the answer", j.PromptFile)
	}
	return ""
}
