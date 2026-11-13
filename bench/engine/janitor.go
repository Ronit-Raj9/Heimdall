package engine

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Janitor removes what crashed runs left behind: labelled docker objects and
// gateway keys of runs that are not active.
type Janitor struct {
	gateway schemas.Gateway
	docker  utils.Docker
	logger  schemas.Logger
}

// SweepReport counts what one sweep removed.
type SweepReport struct {
	Containers int `json:"containers"`
	Volumes    int `json:"volumes"`
	Networks   int `json:"networks"`
	Keys       int `json:"keys"`
}

// dockerKind is one kind of docker object the janitor sweeps, in removal
// order: containers hold volumes and networks, so they go first.
type dockerKind struct {
	name string
	list []string
	rm   []string
}

var dockerKinds = []dockerKind{
	{"containers", []string{"ps", "-a"}, []string{"rm", "-f"}},
	{"volumes", []string{"volume", "ls"}, []string{"volume", "rm", "-f"}},
	{"networks", []string{"network", "ls"}, []string{"network", "rm"}},
}

// NewJanitor returns a janitor. gateway may be nil, then keys are not swept.
func NewJanitor(gateway schemas.Gateway, docker utils.Docker, logger schemas.Logger) *Janitor {
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	return &Janitor{gateway: gateway, docker: docker, logger: logger}
}

// Sweep removes docker containers, volumes and networks labelled
// bifrost-bench=1 whose bifrost-bench.run label names a run that is not in
// active, then revokes stale gateway keys. Objects without a run label are
// shared bench infrastructure (the evaluation network, the gateway stack) and
// are never touched. Every kind is attempted even when another fails.
func (j *Janitor) Sweep(ctx context.Context, active map[string]bool) (*SweepReport, error) {
	rep := &SweepReport{}
	var errs []error
	counts := []*int{&rep.Containers, &rep.Volumes, &rep.Networks}
	for i, k := range dockerKinds {
		n, err := j.sweepKind(ctx, k, active)
		*counts[i] = n
		if err != nil {
			errs = append(errs, err)
		}
	}
	if j.gateway != nil {
		n, err := j.gateway.RevokeStale(ctx, active)
		rep.Keys = n
		if err != nil {
			errs = append(errs, fmt.Errorf("revoke stale keys: %w", err))
		}
	}
	if rep.Containers+rep.Volumes+rep.Networks+rep.Keys > 0 {
		j.logger.Info("janitor removed leftovers", "containers", rep.Containers, "volumes", rep.Volumes,
			"networks", rep.Networks, "keys", rep.Keys)
	}
	return rep, errors.Join(errs...)
}

func (j *Janitor) sweepKind(ctx context.Context, k dockerKind, active map[string]bool) (int, error) {
	args := append(append([]string(nil), k.list...),
		"--filter", "label="+utils.LabelBench+"=1",
		"--format", `{{.ID}}	{{.Label "`+utils.LabelRun+`"}}`)
	if k.name == "volumes" {
		args[len(args)-1] = `{{.Name}}	{{.Label "` + utils.LabelRun + `"}}`
	}
	res, err := j.docker.MustRun(ctx, args...)
	if err != nil {
		return 0, fmt.Errorf("list %s: %w", k.name, err)
	}
	stale := staleObjects(res.Stdout, active)
	if len(stale) == 0 {
		return 0, nil
	}
	rm := append(append([]string(nil), k.rm...), stale...)
	if _, err := j.docker.MustRun(ctx, rm...); err != nil {
		return 0, fmt.Errorf("remove %d %s: %w", len(stale), k.name, err)
	}
	return len(stale), nil
}

// staleObjects parses "<id>\t<run>" lines and returns the ids whose run label
// is set and not active.
func staleObjects(out []byte, active map[string]bool) []string {
	var ids []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		id, run, _ := strings.Cut(strings.TrimSpace(sc.Text()), "\t")
		id, run = strings.TrimSpace(id), strings.TrimSpace(run)
		if id == "" || run == "" || run == "<no value>" || active[run] {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}
