package probes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

var _ schemas.ProbeLayer = (*fixtureLayer)(nil)

// fixtureSteps are the receipt steps the fixture layer proves. It acts once,
// before the agent starts, so there is nothing to fire, deliver or absorb.
var fixtureSteps = []schemas.ReceiptStep{
	schemas.StepAssigned, schemas.StepArmed, schemas.StepVisible, schemas.StepAcknowledged, schemas.StepEffect,
}

// fixtureAckTerms are the words that count as acknowledging a stale or
// misleading file, besides the file's own name.
var fixtureAckTerms = []string{"stale", "outdated", "conflict"}

// fixtureLayer overlays a task fixture onto a workspace path, for example the
// stale notes file over the agent's project notes file. The written copy ends
// with an HTML comment carrying the run's nonce; when that nonce shows up in a
// request Bifrost recorded, the model provably read the file.
type fixtureLayer struct {
	deps Deps
	mu   sync.Mutex
	runs map[string]fixtureArm // run id -> what Arm wrote
}

// fixtureArm is what Arm remembers for Receipts.
type fixtureArm struct {
	Nonce  string
	Target string // workspace-relative path written
	SHA256 string // of the content written
}

// GetName implements schemas.Part.
func (l *fixtureLayer) GetName() string { return string(schemas.LayerFixture) }

// GetVersion implements schemas.Part.
func (l *fixtureLayer) GetVersion() string { return layerVersion }

// Steps implements schemas.ProbeLayer.
func (l *fixtureLayer) Steps() []schemas.ReceiptStep {
	return append([]schemas.ReceiptStep(nil), fixtureSteps...)
}

// Arm implements schemas.ProbeLayer. It renders the overlay paths, reads the
// fixture from the task folder, stamps the nonce, and writes the result into
// the workspace volume with a trusted helper container, owned by the agent
// user. The helper reports the sha256 of the file it wrote, and Arm fails
// unless it matches the content it meant to write.
func (l *fixtureLayer) Arm(ctx context.Context, rc *schemas.RunContext) (*schemas.Receipt, error) {
	runID := runIDOf(rc)
	if runID == "" || rc.Probe == nil || rc.Handle == nil {
		return nil, errors.New("fixture arm: run context needs a run, a probe and an environment handle")
	}
	if rc.Probe.Apply.Overlay == nil {
		return nil, fmt.Errorf("fixture arm: probe %s has no overlay", rc.Probe.ID)
	}
	from, to, err := renderOverlay(*rc.Probe.Apply.Overlay, rc.Task, rc.Agent)
	if err != nil {
		return nil, fmt.Errorf("fixture arm for run %s: %w", runID, err)
	}
	raw, err := os.ReadFile(filepath.Join(rc.Task.Dir, filepath.FromSlash(from)))
	if err != nil {
		return nil, fmt.Errorf("fixture arm for run %s: read fixture: %w", runID, err)
	}
	nonce := utils.NewNonce()
	content := stampNonce(raw, nonce)
	encoded, err := encodeHelperArg(from, content)
	if err != nil {
		return nil, fmt.Errorf("fixture arm for run %s: %w", runID, err)
	}
	out, err := l.deps.helperRun(ctx, rc,
		[]helperMount{{Volume: rc.Handle.Volume, Target: "/w"}},
		fixtureWriteScript, to, encoded)
	if err != nil {
		return nil, fmt.Errorf("fixture arm for run %s: write %s: %w", runID, to, err)
	}
	want := utils.SHA256Bytes(content)
	if got := parseSHA256Sums(out)["/w/"+to]; got != want {
		return nil, fmt.Errorf("fixture arm for run %s: %s has sha256 %q after writing, want %q", runID, to, got, want)
	}
	arm := fixtureArm{Nonce: nonce, Target: to, SHA256: want}
	l.mu.Lock()
	l.runs[runID] = arm
	l.mu.Unlock()
	l.deps.Logger.Debug("fixture armed", "run_id", runID, "target", to, "sha256", want)
	rec := startReceipt(rc, schemas.LayerFixture, fixtureSteps)
	fillFixtureArm(rec, arm, true)
	return rec, nil
}

// Disarm implements schemas.ProbeLayer. The overlay stays in the workspace:
// it is part of the starting state the diff is taken against.
func (l *fixtureLayer) Disarm(context.Context, *schemas.RunContext) error {
	return nil
}

// Receipts implements schemas.ProbeLayer. Visible comes from the gateway log,
// acknowledged from the final message (the notes file name, "stale",
// "outdated" or "conflict"), effect from the grade.
func (l *fixtureLayer) Receipts(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (*schemas.Receipt, error) {
	runID := runIDOf(rc)
	if runID == "" {
		return nil, errors.New("fixture receipts: run context has no run")
	}
	l.mu.Lock()
	arm, ok := l.runs[runID]
	l.mu.Unlock()
	rec := startReceipt(rc, schemas.LayerFixture, fixtureSteps)
	fillFixtureArm(rec, arm, ok)
	rec.Steps[schemas.StepVisible] = nonceSeen(ev, arm.Nonce)
	terms := append([]string(nil), fixtureAckTerms...)
	if rc.Agent != nil && rc.Agent.Notes.ProjectFile != "" {
		terms = append(terms, rc.Agent.Notes.ProjectFile, path.Base(rc.Agent.Notes.ProjectFile))
	}
	rec.Steps[schemas.StepAcknowledged] = claimMentions(ev, terms)
	rec.Steps[schemas.StepEffect] = gradeEffect(ev)
	return rec, nil
}

// newFixtureLayer returns the fixture probe layer.
func newFixtureLayer(deps Deps) *fixtureLayer {
	if deps.Logger == nil {
		deps.Logger = schemas.NoopLogger{}
	}
	return &fixtureLayer{deps: deps, runs: map[string]fixtureArm{}}
}

// fixtureWriteScript writes base64 $2 to /w/$1 as the agent user, creating
// missing parent folders (also owned by the agent), and prints its sha256. An
// existing file is removed first so a symlink is replaced, not followed.
const fixtureWriteScript = `set -eu
p="/w/$1"
d=$(dirname "$p")
top=
while [ ! -e "$d" ]; do top=$d; d=$(dirname "$d"); done
mkdir -p "$(dirname "$p")"
[ -z "$top" ] || chown -R ` + agentUID + ` "$top"
rm -f "$p"
printf '%s' "$2" | base64 -d > "$p"
chown ` + agentUID + ` "$p"
chmod 0644 "$p"
sha256sum "$p"
`

// renderOverlay resolves the overlay's template variables and checks that
// both paths stay inside their roots: from is relative to the task folder,
// to is relative to the workspace. It is pure.
func renderOverlay(spec schemas.OverlaySpec, task *schemas.Task, agent *schemas.AgentConfig) (string, string, error) {
	if task == nil || agent == nil {
		return "", "", errors.New("overlay needs the task and the agent config")
	}
	vars := map[string]string{
		"task.notes.stale":         task.Notes.Stale,
		"agent.notes.project_file": agent.Notes.ProjectFile,
	}
	from, err := utils.Render(spec.From, vars)
	if err != nil {
		return "", "", fmt.Errorf("overlay from: %w", err)
	}
	to, err := utils.Render(spec.To, vars)
	if err != nil {
		return "", "", fmt.Errorf("overlay to: %w", err)
	}
	from, err = localPath("overlay from", from)
	if err != nil {
		return "", "", err
	}
	to, err = localPath("overlay to", to)
	if err != nil {
		return "", "", err
	}
	return from, to, nil
}

// localPath cleans a relative slash path and rejects empty, absolute and
// escaping paths.
func localPath(what, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("%s is empty", what)
	}
	if strings.HasPrefix(p, "/") || !filepath.IsLocal(filepath.FromSlash(p)) {
		return "", fmt.Errorf("%s %q must be a relative path inside its root", what, p)
	}
	return path.Clean(p), nil
}

// stampNonce appends a line "<!-- ref: <nonce> -->" to content. The comment
// is inert in Markdown, so the notes read the same to the agent.
func stampNonce(content []byte, nonce string) []byte {
	out := append([]byte(nil), content...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, []byte("<!-- ref: "+nonce+" -->\n")...)
}

// fillFixtureArm sets the assigned and armed steps. Without the arm record
// (the bench restarted between arm and receipts) armed is unknown, never a
// guess.
func fillFixtureArm(rec *schemas.Receipt, arm fixtureArm, ok bool) {
	rec.Steps[schemas.StepAssigned] = schemas.ReceiptEntry{State: schemas.StepYes}
	if !ok {
		rec.Steps[schemas.StepArmed] = schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: "arm record not in memory"}
		return
	}
	rec.Steps[schemas.StepArmed] = schemas.ReceiptEntry{
		State:    schemas.StepYes,
		Detail:   "sha256 " + arm.SHA256,
		Evidence: []string{arm.Target + ":" + arm.SHA256},
	}
}
