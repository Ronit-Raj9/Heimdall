// Package probes holds the probe layers (docs/DESIGN.md 10): the fixture
// overlay, the tool shim and the gateway fault. A layer arms a perturbation
// before the agent starts, and after the run completes a receipt from
// server-owned evidence (gateway rows, the proxy ledger, the shim log, the
// grade), never from the agent's own account.
//
// Each armed probe carries a fresh secret nonce. A layer keeps it in memory,
// keyed by run id, between Arm and Receipts; it is never written anywhere the
// agent can read except inside the perturbation itself.
package probes

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// layerVersion is the version of the fixture and tool_shim layers. Bump it
// with any change to what they write or how they read receipts.
const layerVersion = "1.0.0"

// agentUID owns every file a layer writes where the agent must be able to
// write: the agent images run as uid 1000.
const agentUID = "1000:1000"

// maxHelperArg caps the raw bytes passed to a helper container in one
// argument. Linux limits one argv string to 128 KiB and base64 grows data by
// a third.
const maxHelperArg = 96 << 10

// Deps are the services the probe layers need. main.go builds it once.
type Deps struct {
	Docker       utils.Docker            // runs the docker CLI
	HelperImage  string                  // trusted image with sh, base64, sha256sum, head
	Fault        schemas.FaultController // the fault proxy's control API, for the gateway layer
	AdapterFault schemas.AdapterFaultController
	Logger       schemas.Logger
}

// Builtin returns the probe layers that ship with the bench, in arming order.
// Layers disarm in reverse. Each call builds new instances, so nonces held by
// one registry are never visible to another.
func Builtin(deps Deps) []schemas.ProbeLayer {
	if deps.Logger == nil {
		deps.Logger = schemas.NoopLogger{}
	}
	return []schemas.ProbeLayer{
		newFixtureLayer(deps),
		newToolShimLayer(deps),
		newGatewayLayer(deps.Fault, deps.Logger),
		newToolFaultLayer(deps), // tool proxy faults (Tool proxy and MCP)
		// condition data planted in services (Tool safety)
		newServiceLayer(deps.Logger),
		newAdapterLayer(deps.AdapterFault),
		newProcessLayer(), // crash-resume: the engine kills and resumes the agent (process.go)
		newA2ALayer(),     // B1: client-side A2A stream faults through the agent gateway (a2a.go)
		// X22: fault schedules across the gateway and tool layers, with
		// their own instances so their per-run state never mixes.
		newCompoundLayer(deps.Logger, newGatewayLayer(deps.Fault, deps.Logger), newToolFaultLayer(deps)),
	}
}

var volumeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// helperMount mounts a named docker volume into a helper container.
type helperMount struct {
	Volume   string
	Target   string
	ReadOnly bool
}

// helperRun runs a POSIX sh script as root in a short-lived trusted helper
// container with no network, only the named volumes mounted and only the
// capabilities needed to set ownership. Volumes are mounted with --mount
// type=volume, so a volume name can never become a host bind mount. It
// returns stdout; a non-zero exit is an error carrying stderr.
func (d Deps) helperRun(ctx context.Context, rc *schemas.RunContext, mounts []helperMount, script string, args ...string) ([]byte, error) {
	if d.HelperImage == "" {
		return nil, errors.New("no helper image configured")
	}
	argv := []string{
		"run", "--rm", "--network", "none", "--user", "0:0",
		"--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--cap-add", "FOWNER",
		"--security-opt", "no-new-privileges", "--pids-limit", "64", "--memory", "256m",
	}
	argv = append(argv, utils.LabelArgs(expIDOf(rc), runIDOf(rc))...)
	for _, m := range mounts {
		if !volumeName.MatchString(m.Volume) {
			return nil, fmt.Errorf("invalid volume name %q", m.Volume)
		}
		spec := "type=volume,source=" + m.Volume + ",target=" + m.Target
		if m.ReadOnly {
			spec += ",readonly"
		}
		argv = append(argv, "--mount", spec)
	}
	argv = append(argv, "--entrypoint", "/bin/sh", d.HelperImage, "-c", script, "sh")
	argv = append(argv, args...)
	res, err := d.Docker.MustRun(ctx, argv...)
	if err != nil {
		return nil, fmt.Errorf("helper container: %w", err)
	}
	return res.Stdout, nil
}

// encodeHelperArg base64-encodes data for a helper script argument, refusing
// data that would not fit in one argv string.
func encodeHelperArg(name string, data []byte) (string, error) {
	if len(data) > maxHelperArg {
		return "", fmt.Errorf("%s is %d bytes, over the %d byte limit", name, len(data), maxHelperArg)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// parseSHA256Sums reads `sha256sum` output into path -> hash.
func parseSHA256Sums(out []byte) map[string]string {
	sums := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		hash, p, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || len(hash) != 64 {
			continue
		}
		sums[strings.TrimLeft(p, " *")] = hash
	}
	return sums
}

// runIDOf returns the run id, or "".
func runIDOf(rc *schemas.RunContext) string {
	if rc == nil || rc.Run == nil {
		return ""
	}
	return rc.Run.RunID
}

// expIDOf returns the experiment id from the run, slot or experiment, or "".
func expIDOf(rc *schemas.RunContext) string {
	switch {
	case rc == nil:
		return ""
	case rc.Run != nil && rc.Run.ExpID != "":
		return rc.Run.ExpID
	case rc.Slot != nil && rc.Slot.ExpID != "":
		return rc.Slot.ExpID
	case rc.Experiment != nil:
		return rc.Experiment.ID
	}
	return ""
}

// startReceipt returns a receipt with every step of the ladder present: the
// layer's own steps unknown until filled, the rest not_applicable, never
// blank.
func startReceipt(rc *schemas.RunContext, layer schemas.ProbeLayerName, steps []schemas.ReceiptStep) *schemas.Receipt {
	rec := &schemas.Receipt{Layer: layer, Steps: map[schemas.ReceiptStep]schemas.ReceiptEntry{}}
	if rc != nil && rc.Probe != nil {
		rec.ProbeID = rc.Probe.ID
	}
	for _, s := range schemas.AllReceiptSteps {
		rec.Steps[s] = schemas.ReceiptEntry{State: schemas.StepNotApplicable}
	}
	for _, s := range steps {
		rec.Steps[s] = schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: "not yet collected"}
	}
	return rec
}

// nonceSeen is the visible step: yes when a gateway request recorded by
// Bifrost carries the nonce in its raw body, no when the gateway log is
// complete and none does, unknown otherwise. The nonce is secret and only
// ever printed by the perturbation, so a request that carries it necessarily
// came after the agent saw the perturbation.
func nonceSeen(ev *schemas.Evidence, nonce string) schemas.ReceiptEntry {
	if nonce == "" {
		return schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: "nonce not in memory"}
	}
	if ev == nil || ev.Requests == nil {
		return schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: "no gateway log"}
	}
	var ids []string
	for _, r := range ev.Requests.Requests {
		if strings.Contains(string(r.Raw), nonce) {
			ids = append(ids, r.ID)
		}
	}
	switch {
	case len(ids) > 0:
		return schemas.ReceiptEntry{State: schemas.StepYes, Detail: fmt.Sprintf("nonce in %d gateway request(s)", len(ids)), Evidence: ids}
	case ev.Requests.Complete:
		return schemas.ReceiptEntry{State: schemas.StepNo, Detail: "nonce in no gateway request; log complete"}
	default:
		return schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: "nonce not found; gateway log incomplete: " + ev.Requests.Reason}
	}
}

// claimMentions is the acknowledged step: yes when the agent's final message
// mentions any of terms (case-insensitive), no when it does not, unknown when
// there is no final message.
func claimMentions(ev *schemas.Evidence, terms []string) schemas.ReceiptEntry {
	if ev == nil || ev.Claim == nil || strings.TrimSpace(ev.Claim.Text) == "" {
		return schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: "no final message"}
	}
	text := strings.ToLower(ev.Claim.Text)
	for _, t := range terms {
		if t != "" && strings.Contains(text, strings.ToLower(t)) {
			return schemas.ReceiptEntry{State: schemas.StepYes, Detail: "final message mentions " + t}
		}
	}
	return schemas.ReceiptEntry{State: schemas.StepNo, Detail: "final message does not mention the perturbation"}
}

// gradeEffect is the effect step: yes when the hidden verifier passed the
// diff, no when it failed it, unknown when there is no decided grade.
func gradeEffect(ev *schemas.Evidence) schemas.ReceiptEntry {
	switch {
	case ev == nil || ev.Grade == nil:
		return schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: "no grade"}
	case ev.Grade.InvalidEvaluator:
		return schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: "grade undecided: " + ev.Grade.Reason}
	case ev.Grade.Passed:
		return schemas.ReceiptEntry{State: schemas.StepYes, Detail: "passed", Evidence: []string{"diff_sha256:" + ev.Grade.DiffSHA256}}
	default:
		return schemas.ReceiptEntry{State: schemas.StepNo, Detail: "failed", Evidence: []string{"diff_sha256:" + ev.Grade.DiffSHA256}}
	}
}
