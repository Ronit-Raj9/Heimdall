package suite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// ChangeLevel is the semantic-version level of a change between two locks
// (DESIGN.md section 12).
type ChangeLevel string

// Change levels.
const (
	LevelPatch ChangeLevel = "PATCH" // metadata only: old results are reused
	LevelMinor ChangeLevel = "MINOR" // verifier or controls changed, or a task was added or removed: regrade stored diffs
	LevelMajor ChangeLevel = "MAJOR" // instruction, repo, image or fixtures changed: rerun
)

// Actions a change requires for results recorded under the old lock.
const (
	ActionReuse   = "reuse"
	ActionRegrade = "regrade"
	ActionRerun   = "rerun"
	ActionAdded   = "added"   // no old results; comparisons use the shared tasks
	ActionRemoved = "removed" // old results have no counterpart
)

// Change scopes.
const (
	ScopeSuite = "suite"
	ScopeTask  = "task"
	ScopeProbe = "probe"
)

// Change is one difference between two suite locks.
type Change struct {
	Scope  string      `json:"scope"` // suite | task | probe
	ID     string      `json:"id"`
	Level  ChangeLevel `json:"level"`
	Action string      `json:"action"`
	Fields []string    `json:"fields"` // the lock fields that differ, e.g. verifier_sha256
}

// Lock pins every loaded task and probe of s by hash. Admission records from
// s.Lock are kept for tasks whose five content hashes are unchanged; a task
// whose content changed, or every task when the base image changed, goes back
// to draft with its admission cleared, because an admission holds only for the
// exact task version it checked. When nothing but the time would change, the
// previous created_at is kept, so re-locking an unchanged pack leaves the
// lock file and its hash identical.
func Lock(s *schemas.Suite, now time.Time) (*schemas.SuiteLock, error) {
	prev := s.Lock
	if prev != nil && prev.Suite != s.ID {
		prev = nil
	}
	imageSame := prev != nil && sameImage(prev.BaseImage, s.BaseImage)
	l := &schemas.SuiteLock{
		Suite:     s.ID,
		Version:   s.Version,
		BaseImage: s.BaseImage,
		Tasks:     make(map[string]schemas.TaskLock, len(s.Tasks)),
		Probes:    make(map[string]schemas.ProbeLock, len(s.Probes)),
		CreatedAt: now.UTC(),
	}
	for _, t := range s.Tasks {
		tl, err := lockTask(s, t)
		if err != nil {
			return nil, err
		}
		if old, ok := prevTask(prev, t.ID); ok && imageSame && sameContent(old, tl) {
			tl.Status = old.Status
			tl.Admission = old.Admission
		}
		l.Tasks[t.ID] = tl
	}
	for _, p := range s.Probes {
		sum, err := utils.SHA256File(p.Path)
		if err != nil {
			return nil, fmt.Errorf("lock probe %s: %w", p.ID, err)
		}
		l.Probes[p.ID] = schemas.ProbeLock{Version: p.Version, SHA256: sum}
	}
	if prev != nil && equalIgnoringTime(prev, l) {
		l.CreatedAt = prev.CreatedAt
	}
	return l, nil
}

// WriteLock writes l to path as indented JSON with sorted keys and a trailing
// newline, through a temp file and a rename so a crash never leaves half a
// lock. It returns the bytes written; their sha256 is the suite lock hash.
func WriteLock(path string, l *schemas.SuiteLock) ([]byte, error) {
	data, err := encodeLock(l)
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".suite.lock.*")
	if err != nil {
		return nil, fmt.Errorf("write lock %s: %w", path, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return nil, fmt.Errorf("write lock %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("write lock %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("write lock %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("write lock %s: %w", path, err)
	}
	return data, nil
}

// Diff lists what changed from lock a to lock b, per task and probe, with the
// level from DESIGN.md section 12: instruction, repo or fixtures changed is
// MAJOR (rerun); verifier or controls changed is MINOR (regrade stored diffs);
// a version or status change alone is PATCH (reuse). A changed base image is
// one suite-level MAJOR change. Either lock may be nil. The result is sorted
// by scope and id.
func Diff(a, b *schemas.SuiteLock) []Change {
	if a == nil {
		a = &schemas.SuiteLock{}
	}
	if b == nil {
		b = &schemas.SuiteLock{}
	}
	var out []Change
	if !sameImage(a.BaseImage, b.BaseImage) && (a.BaseImage.Tag != "" || b.BaseImage.Tag != "") {
		out = append(out, Change{Scope: ScopeSuite, ID: b.Suite, Level: LevelMajor, Action: ActionRerun, Fields: []string{"base_image"}})
	}
	for _, id := range unionKeys(a.Tasks, b.Tasks) {
		oldT, inA := a.Tasks[id]
		newT, inB := b.Tasks[id]
		if c, ok := diffPresence(ScopeTask, id, inA, inB); ok {
			out = append(out, c)
			continue
		}
		if c, ok := diffTask(id, oldT, newT); ok {
			out = append(out, c)
		}
	}
	for _, id := range unionKeys(a.Probes, b.Probes) {
		oldP, inA := a.Probes[id]
		newP, inB := b.Probes[id]
		if c, ok := diffPresence(ScopeProbe, id, inA, inB); ok {
			out = append(out, c)
			continue
		}
		switch {
		case oldP.SHA256 != newP.SHA256:
			out = append(out, Change{Scope: ScopeProbe, ID: id, Level: LevelMajor, Action: ActionRerun, Fields: []string{"sha256"}})
		case oldP.Version != newP.Version:
			out = append(out, Change{Scope: ScopeProbe, ID: id, Level: LevelPatch, Action: ActionReuse, Fields: []string{"version"}})
		}
	}
	return out
}

// lockTask computes one task's hashes. The controls hash covers the reference,
// the empty patch, every mutant and the exploit together; the fixtures hash
// covers both notes files and the fixtures/ folder when present. Both use the
// utils.TreeHash line format over paths relative to the task folder. Scenario
// content (scenarioParts) is folded into the hash whose change level it
// shares, and only when present, so a task without it hashes as it always
// did.
func lockTask(s *schemas.Suite, t *schemas.Task) (schemas.TaskLock, error) {
	fail := func(what string, err error) (schemas.TaskLock, error) {
		return schemas.TaskLock{}, fmt.Errorf("lock task %s: %s: %w", t.ID, what, err)
	}
	instr, err := resolveIn(t.Dir, t.Instruction)
	if err != nil {
		return fail("instruction", err)
	}
	instrSum, err := utils.SHA256File(instr)
	if err != nil {
		return fail("instruction", err)
	}
	repoSum, err := treeHashRel(t.Dir, t.RepoDir)
	if err != nil {
		return fail("repo_dir", err)
	}
	verSum, err := treeHashRel(t.Dir, t.Verifier.Tests)
	if err != nil {
		return fail("verifier.tests", err)
	}
	controls := append([]string{t.Controls.Reference, t.Controls.Empty, t.Controls.Exploit}, t.Controls.Mutants...)
	ctlSum, err := setHash(t.Dir, controls)
	if err != nil {
		return fail("controls", err)
	}
	var fixtures []string
	if t.Notes.Clean != "" {
		fixtures = append(fixtures, t.Notes.Clean)
	}
	if t.Notes.Stale != "" {
		fixtures = append(fixtures, t.Notes.Stale)
	}
	if fi, err := os.Stat(filepath.Join(t.Dir, "fixtures")); err == nil && fi.IsDir() {
		fixtures = append(fixtures, "fixtures")
	}
	fixSum, err := setHash(t.Dir, fixtures)
	if err != nil {
		return fail("fixtures", err)
	}
	extra, err := scenarioParts(s, t)
	if err != nil {
		return fail("scenario", err)
	}
	instrSum = foldHash(instrSum, extra.instruction)
	repoSum = foldHash(repoSum, extra.repo)
	verSum = foldHash(verSum, extra.verifier)
	fixSum = foldHash(fixSum, extra.fixtures)
	return schemas.TaskLock{
		Version:           t.Version,
		InstructionSHA256: instrSum,
		RepoTreeSHA256:    repoSum,
		VerifierSHA256:    verSum,
		ControlsSHA256:    ctlSum,
		FixturesSHA256:    fixSum,
		Status:            schemas.TaskDraft,
	}, nil
}

func treeHashRel(base, rel string) (string, error) {
	abs, err := resolveIn(base, rel)
	if err != nil {
		return "", err
	}
	return utils.TreeHash(abs)
}

// setHash hashes a set of files and folders relative to base. The digest
// equals utils.TreeHash of a folder holding exactly these files at the same
// relative paths; a path listed twice, or a file inside a listed folder, is
// counted once.
func setHash(base string, rels []string) (string, error) {
	files := map[string]string{}
	for _, rel := range rels {
		abs, err := resolveIn(base, rel)
		if err != nil {
			return "", err
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("stat %s: %w", rel, err)
		}
		key := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
		if !fi.IsDir() {
			sum, err := utils.SHA256File(abs)
			if err != nil {
				return "", err
			}
			files[key] = sum
			continue
		}
		sub, err := utils.HashFiles(abs)
		if err != nil {
			return "", err
		}
		for p, sum := range sub {
			files[key+"/"+p] = sum
		}
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var buf bytes.Buffer
	for _, p := range paths {
		buf.WriteString(p)
		buf.WriteByte(0)
		buf.WriteString(files[p])
		buf.WriteByte('\n')
	}
	return utils.SHA256Bytes(buf.Bytes()), nil
}

func encodeLock(l *schemas.SuiteLock) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(l); err != nil {
		return nil, fmt.Errorf("encode lock: %w", err)
	}
	return buf.Bytes(), nil
}

func prevTask(prev *schemas.SuiteLock, id string) (schemas.TaskLock, bool) {
	if prev == nil {
		return schemas.TaskLock{}, false
	}
	t, ok := prev.Tasks[id]
	return t, ok
}

// sameContent reports whether two task locks pin identical task content.
// The version is metadata and does not count.
func sameContent(a, b schemas.TaskLock) bool {
	return len(contentFields(a, b)) == 0
}

// contentFields lists the hash fields that differ, MAJOR ones first.
func contentFields(a, b schemas.TaskLock) []string {
	var f []string
	if a.InstructionSHA256 != b.InstructionSHA256 {
		f = append(f, "instruction_sha256")
	}
	if a.RepoTreeSHA256 != b.RepoTreeSHA256 {
		f = append(f, "repo_tree_sha256")
	}
	if a.FixturesSHA256 != b.FixturesSHA256 {
		f = append(f, "fixtures_sha256")
	}
	if a.VerifierSHA256 != b.VerifierSHA256 {
		f = append(f, "verifier_sha256")
	}
	if a.ControlsSHA256 != b.ControlsSHA256 {
		f = append(f, "controls_sha256")
	}
	return f
}

func diffTask(id string, a, b schemas.TaskLock) (Change, bool) {
	fields := contentFields(a, b)
	c := Change{Scope: ScopeTask, ID: id, Level: LevelPatch, Action: ActionReuse}
	for _, f := range fields {
		switch f {
		case "instruction_sha256", "repo_tree_sha256", "fixtures_sha256":
			c.Level, c.Action = LevelMajor, ActionRerun
		default:
			if c.Level != LevelMajor {
				c.Level, c.Action = LevelMinor, ActionRegrade
			}
		}
	}
	if a.Version != b.Version {
		fields = append(fields, "version")
	}
	if a.Status != b.Status {
		fields = append(fields, "status")
	}
	if len(fields) == 0 {
		return Change{}, false
	}
	c.Fields = fields
	return c, true
}

func diffPresence(scope, id string, inA, inB bool) (Change, bool) {
	switch {
	case inA && !inB:
		return Change{Scope: scope, ID: id, Level: LevelMinor, Action: ActionRemoved, Fields: []string{"removed"}}, true
	case !inA && inB:
		return Change{Scope: scope, ID: id, Level: LevelMinor, Action: ActionAdded, Fields: []string{"added"}}, true
	default:
		return Change{}, false
	}
}

// sameImage compares base images. The digest counts only when both sides
// have one, because `suite lock` may run before the image is built.
func sameImage(a, b schemas.ImageSpec) bool {
	if a.Tag != b.Tag || a.Dockerfile != b.Dockerfile {
		return false
	}
	return a.Digest == "" || b.Digest == "" || a.Digest == b.Digest
}

func equalIgnoringTime(a, b *schemas.SuiteLock) bool {
	ca, cb := *a, *b
	ca.CreatedAt, cb.CreatedAt = time.Time{}, time.Time{}
	ja, errA := encodeLock(&ca)
	jb, errB := encodeLock(&cb)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

func unionKeys[V any](a, b map[string]V) []string {
	seen := make(map[string]bool, len(a)+len(b))
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
