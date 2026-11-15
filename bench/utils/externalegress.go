package utils

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// External egress is the route out a run's tool proxy gets for its external
// APIs in live or record mode (docs/external-apis.md). It reuses the remote
// egress forwarder program: one forwarder container per API joins an
// internal network only the tool proxy shares (never the agent) and a fresh
// outbound bridge network of its own, and forwards TCP to exactly the API's
// host and port. TLS runs end to end between the proxy and the real host. A
// replay run starts none.
const (
	// ExternalEgressFile records the forwarders in the run folder, under
	// ExternalFolder.
	ExternalEgressFile = "egress.json"
)

// ExternalEgressName is the forwarder container's and its outbound network's
// name for one external API of a run.
func ExternalEgressName(runID, id string) string { return "bench-eg-" + runID + "-" + id }

// ExternalEgressAddr is where a run's tool proxy reaches the forwarder of
// external API id.
func ExternalEgressAddr(runID, id string) string {
	return ExternalEgressName(runID, id) + ":" + strconv.Itoa(RemoteEgressPort)
}

// ExternalEgressRecord is external/egress.json in the run folder.
type ExternalEgressRecord struct {
	Allowed    map[string]string `json:"allowed"`    // external api id -> the one host:port its forwarder connects to
	Forwarders []string          `json:"forwarders"` // forwarder containers, each with an outbound network of the same name
	Joined     string            `json:"joined"`     // the internal network the forwarders share with the tool proxy
	Started    time.Time         `json:"started"`
}

// ExternalEgress is a set of started forwarders.
type ExternalEgress struct {
	d      Docker
	names  []string
	logger schemas.Logger
}

// StartExternalEgress starts one forwarder per entry of targets (external
// api id -> host:port), attached to network (the internal network the tool
// proxy also joins), with image (any image with python3), and records them
// in <run folder>/external/egress.json. Empty targets start nothing and
// return nil. The forwarders run as nobody with every capability dropped, a
// read-only root and small limits; they carry the run's labels, so Destroy
// and the janitor remove leftovers. On failure everything started is removed.
func StartExternalEgress(ctx context.Context, d Docker, rc *schemas.RunContext, image, network string, targets map[string]string) (*ExternalEgress, error) {
	if len(targets) == 0 {
		return nil, nil
	}
	if rc == nil || rc.Run == nil || rc.Run.RunID == "" {
		return nil, errors.New("external egress: run context has no run")
	}
	if network == "" || network == "none" || network == "host" || network == "bridge" {
		return nil, fmt.Errorf("external egress: network %q cannot be joined", network)
	}
	if image == "" {
		return nil, errors.New("external egress: no forwarder image")
	}
	e := &ExternalEgress{d: d, logger: rc.Logger}
	if e.logger == nil {
		e.logger = schemas.NoopLogger{}
	}
	ids := make([]string, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	labels := LabelArgs(rc.Run.ExpID, rc.Run.RunID)
	for _, id := range ids {
		target := targets[id]
		if err := checkHostPort(target); err != nil {
			e.Stop(context.WithoutCancel(ctx))
			return nil, fmt.Errorf("external egress %s: %w", id, err)
		}
		name := ExternalEgressName(rc.Run.RunID, id)
		e.names = append(e.names, name)
		steps := [][]string{
			append(append([]string{"network", "create"}, labels...), name),
			append(append([]string{"create", "--name", name}, labels...),
				"--user", "65534", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--read-only",
				"--pids-limit", "64", "--memory", "64m", "--network", name, "-e", "BENCH_EGRESS_TARGET="+target,
				"--entrypoint", "python3", image, "-I", "-c", remoteForwarder),
			{"network", "connect", network, name},
			{"start", name},
		}
		for _, args := range steps {
			if _, err := d.MustRun(ctx, args...); err != nil {
				e.Stop(context.WithoutCancel(ctx))
				return nil, fmt.Errorf("external egress %s to %s: %w", id, target, err)
			}
		}
	}
	if rc.Dir != "" {
		rec := ExternalEgressRecord{Allowed: targets, Forwarders: e.names, Joined: network, Started: time.Now().UTC()}
		if err := writeEgressRecord(filepath.Join(rc.Dir, ExternalFolder, ExternalEgressFile), rec); err != nil {
			e.Stop(context.WithoutCancel(ctx))
			return nil, fmt.Errorf("external egress: record: %w", err)
		}
	}
	return e, nil
}

// Stop removes the forwarders and their outbound networks. It is safe on nil
// and after a partial start; a removal that fails is logged and left to
// Destroy and the janitor.
func (e *ExternalEgress) Stop(ctx context.Context) {
	if e == nil {
		return
	}
	for _, n := range e.names {
		r := &RemoteEgress{d: e.d, name: n, logger: e.logger}
		r.Stop(ctx)
	}
}

// writeEgressRecord writes rec as indented JSON at p, creating its folder.
func writeEgressRecord(p string, rec ExternalEgressRecord) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}
