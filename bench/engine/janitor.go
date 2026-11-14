package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Janitor defaults.
const (
	// DefaultSweepGrace is how old every docker object of a run must be before
	// the janitor removes them when no lease says whether the run is alive: a
	// run of another project folder on the same docker host and gateway, an
	// admission control, or an agent check, which holds no lease. It is far
	// longer than any run's wall budget plus grading, because removing a live
	// run's workspace is much worse than keeping a crashed run's leftovers a
	// few hours longer.
	DefaultSweepGrace = 6 * time.Hour
	// ChecksRoot is the folder, relative to the bench root, where `agent
	// check` keeps its runs by default. The scheduler scans it with its own
	// store root so a running check is known to the janitor.
	ChecksRoot = "checks"
)

// Run folder names the janitor reads. They are the store's layout (DESIGN.md
// 16.1): <root>/<exp>/slots/<slot>/lease.json and .../runs/<run>/seal.json.
const (
	slotsFolder = "slots"
	runsFolder  = "runs"
	leaseName   = "lease.json"
	sealName    = "seal.json"
)

// Janitor removes what crashed runs left behind: labelled docker objects and
// gateway keys of runs that are not active.
type Janitor struct {
	gateway schemas.Gateway
	docker  utils.Docker
	logger  schemas.Logger
	grace   time.Duration
	now     func() time.Time
}

// JanitorOption configures a Janitor.
type JanitorOption func(*Janitor)

// WithSweepGrace sets how old a run without lease information must be before
// it is swept (DefaultSweepGrace when unset or not positive).
func WithSweepGrace(d time.Duration) JanitorOption {
	return func(j *Janitor) {
		if d > 0 {
			j.grace = d
		}
	}
}

// SweepReport counts what one sweep removed.
type SweepReport struct {
	Containers int `json:"containers"`
	Volumes    int `json:"volumes"`
	Networks   int `json:"networks"`
	Keys       int `json:"keys"`
	KeptYoung  int `json:"kept_young,omitempty"` // runs with no lease information kept because they are younger than the grace
}

// RunLiveness is what the lease files under the store roots say about runs.
// A run in neither map has no lease information: it is not in any scanned
// root, or its slot holds no lease and it is not sealed (an agent check, or a
// run whose seal failed).
type RunLiveness struct {
	Live map[string]bool // runs of a slot whose lease heartbeat is fresh, whoever owns it
	Dead map[string]bool // runs of a slot whose lease is stale, and sealed runs of a slot with no lease
}

// dockerKind is one kind of docker object the janitor sweeps, in removal
// order: containers hold volumes and networks, so they go first. A kind
// whose listing has no creation time (volumes) is aged with inspect.
type dockerKind struct {
	name    string
	list    []string
	idField string
	created bool // the listing can print {{.CreatedAt}}
	rm      []string
}

var dockerKinds = []dockerKind{
	{"containers", []string{"ps", "-a"}, "{{.ID}}", true, []string{"rm", "-f"}},
	{"volumes", []string{"volume", "ls"}, "{{.Name}}", false, []string{"volume", "rm", "-f"}},
	{"networks", []string{"network", "ls"}, "{{.ID}}", true, []string{"network", "rm"}},
}

// dockerObject is one labelled object with its run and creation time (zero
// when it could not be read).
type dockerObject struct {
	id      string
	run     string
	created time.Time
}

// NewJanitor returns a janitor. gateway may be nil, then keys are not swept.
func NewJanitor(gateway schemas.Gateway, docker utils.Docker, logger schemas.Logger, opts ...JanitorOption) *Janitor {
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	j := &Janitor{gateway: gateway, docker: docker, logger: logger, grace: DefaultSweepGrace, now: time.Now}
	for _, o := range opts {
		o(j)
	}
	return j
}

// ScanRunLiveness reads every slot lease under roots (a missing root is
// skipped) and classifies the runs it finds. A lease is fresh when its
// heartbeat is at most staleAfter before now; a lease.json that does not
// decode is one being created and counts as fresh from its mtime, as the
// store treats it.
func ScanRunLiveness(roots []string, staleAfter time.Duration, now time.Time) (RunLiveness, error) {
	lv := RunLiveness{Live: map[string]bool{}, Dead: map[string]bool{}}
	for _, root := range roots {
		slots, err := filepath.Glob(filepath.Join(root, "*", slotsFolder, "*"))
		if err != nil {
			return lv, fmt.Errorf("scan leases under %s: %w", root, err)
		}
		for _, slot := range slots {
			if err := scanSlot(slot, staleAfter, now, lv); err != nil {
				return lv, err
			}
		}
	}
	return lv, nil
}

// Sweep removes docker containers, volumes and networks labelled
// bifrost-bench=1 whose bifrost-bench.run label names a run that is not
// alive, then revokes the gateway keys of the same runs. A run in lv.Live is
// kept and a run in lv.Dead is removed. Any other run has no lease
// information, so it is removed only when every object of it is older than
// the grace period; an object whose age cannot be read keeps its run.
// Objects without a run label are shared bench infrastructure (the
// evaluation network, the gateway stack) and are never touched. Every kind
// is attempted even when another fails, but when a listing fails no run
// without lease information is removed and no key is revoked, since a young
// run the sweep could not fully see would lose its workspace or its key.
func (j *Janitor) Sweep(ctx context.Context, lv RunLiveness) (*SweepReport, error) {
	rep := &SweepReport{}
	var errs []error
	objects := make([][]dockerObject, len(dockerKinds))
	listed := true
	for i, k := range dockerKinds {
		objs, err := j.list(ctx, k)
		if err != nil {
			errs = append(errs, err)
			listed = false
			continue
		}
		objects[i] = objs
	}
	keep, remove := j.decide(lv, objects, listed, rep)
	counts := []*int{&rep.Containers, &rep.Volumes, &rep.Networks}
	for i, k := range dockerKinds {
		n, err := j.remove(ctx, k, objects[i], remove)
		*counts[i] = n
		if err != nil {
			errs = append(errs, err)
		}
	}
	switch {
	case j.gateway == nil:
	case !listed:
		errs = append(errs, errors.New("stale keys not revoked: a docker listing failed, so young runs could not be told apart"))
	default:
		n, err := j.gateway.RevokeStale(ctx, keep)
		rep.Keys = n
		if err != nil {
			errs = append(errs, fmt.Errorf("revoke stale keys: %w", err))
		}
	}
	if rep.Containers+rep.Volumes+rep.Networks+rep.Keys > 0 {
		j.logger.Info("janitor removed leftovers", "containers", rep.Containers, "volumes", rep.Volumes,
			"networks", rep.Networks, "keys", rep.Keys, "kept_young", rep.KeptYoung)
	}
	return rep, errors.Join(errs...)
}

// decide returns the runs whose keys must be kept (live runs and runs
// without lease information that are not provably old) and the runs whose
// docker objects are removed. A run without lease information is old when
// every object of it was listed (listed: no listing failed) and aged, and
// the youngest is older than the grace period.
func (j *Janitor) decide(lv RunLiveness, objects [][]dockerObject, listed bool, rep *SweepReport) (keep, remove map[string]bool) {
	keep, remove = map[string]bool{}, map[string]bool{}
	for run := range lv.Live {
		keep[run] = true
	}
	for run := range lv.Dead {
		remove[run] = true
	}
	youngest := map[string]time.Time{}
	unaged := map[string]bool{}
	for _, objs := range objects {
		for _, o := range objs {
			switch {
			case lv.Live[o.run] || lv.Dead[o.run]:
			case o.created.IsZero() || !listed:
				unaged[o.run] = true
			case o.created.After(youngest[o.run]):
				youngest[o.run] = o.created
			}
		}
	}
	now := j.now()
	for run := range unaged {
		keep[run] = true
	}
	for run, at := range youngest {
		switch {
		case unaged[run]:
		case now.Sub(at) >= j.grace:
			remove[run] = true
		default:
			keep[run] = true
			rep.KeptYoung++
			j.logger.Info("janitor kept a run with no lease information: younger than the grace period", "run", run,
				"age", now.Sub(at).Round(time.Second).String(), "grace", j.grace.String())
		}
	}
	return keep, remove
}

// list returns the kind's labelled objects that carry a run label, with
// their creation times.
func (j *Janitor) list(ctx context.Context, k dockerKind) ([]dockerObject, error) {
	format := k.idField + "\t{{.Label \"" + utils.LabelRun + "\"}}"
	if k.created {
		format += "\t{{.CreatedAt}}"
	}
	args := append(append([]string(nil), k.list...), "--filter", "label="+utils.LabelBench+"=1", "--format", format)
	res, err := j.docker.MustRun(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", k.name, err)
	}
	objs := parseObjects(res.Stdout)
	if k.created || len(objs) == 0 {
		return objs, nil
	}
	return j.inspectVolumes(ctx, objs), nil
}

// inspectVolumes fills each volume's creation time from docker volume
// inspect. A volume it cannot age keeps a zero time, which keeps its run.
func (j *Janitor) inspectVolumes(ctx context.Context, objs []dockerObject) []dockerObject {
	args := []string{"volume", "inspect", "--format", "{{.Name}}\t{{.CreatedAt}}"}
	for _, o := range objs {
		args = append(args, o.id)
	}
	res, err := j.docker.Run(ctx, args...)
	if err != nil {
		j.logger.Warn("janitor could not age volumes; runs without lease information keep them", "error", err.Error())
		return objs
	}
	created := map[string]time.Time{}
	sc := bufio.NewScanner(bytes.NewReader(res.Stdout))
	for sc.Scan() {
		name, at, _ := strings.Cut(strings.TrimSpace(sc.Text()), "\t")
		if t, ok := parseDockerTime(at); ok {
			created[strings.TrimSpace(name)] = t
		}
	}
	out := make([]dockerObject, len(objs))
	for i, o := range objs {
		o.created = created[o.id]
		out[i] = o
	}
	return out
}

func (j *Janitor) remove(ctx context.Context, k dockerKind, objs []dockerObject, remove map[string]bool) (int, error) {
	var ids []string
	for _, o := range objs {
		if remove[o.run] {
			ids = append(ids, o.id)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	rm := append(append([]string(nil), k.rm...), ids...)
	if _, err := j.docker.MustRun(ctx, rm...); err != nil {
		return 0, fmt.Errorf("remove %d %s: %w", len(ids), k.name, err)
	}
	return len(ids), nil
}

// scanSlot classifies the runs of one slot folder into lv.
func scanSlot(slot string, staleAfter time.Duration, now time.Time, lv RunLiveness) error {
	runs, err := os.ReadDir(filepath.Join(slot, runsFolder))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("scan runs of %s: %w", slot, err)
	}
	if len(runs) == 0 {
		return nil
	}
	held, fresh, err := readLeaseFile(filepath.Join(slot, leaseName), staleAfter, now)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if !r.IsDir() {
			continue
		}
		id := r.Name()
		switch {
		case fresh:
			lv.Live[id] = true
		case held:
			lv.Dead[id] = true
		default:
			if _, err := os.Lstat(filepath.Join(slot, runsFolder, id, sealName)); err == nil {
				lv.Dead[id] = true
			}
		}
	}
	return nil
}

// readLeaseFile reports whether the slot has a lease and whether it is
// fresh.
func readLeaseFile(p string, staleAfter time.Duration, now time.Time) (held, fresh bool, err error) {
	raw, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("read lease %s: %w", p, err)
	}
	var l schemas.Lease
	var at time.Time
	if json.Unmarshal(raw, &l) == nil && !l.HeartbeatAt.IsZero() {
		at = l.HeartbeatAt
	} else {
		info, err := os.Stat(p)
		if err != nil {
			return false, false, fmt.Errorf("stat lease %s: %w", p, err)
		}
		at = info.ModTime()
	}
	return true, now.Sub(at) <= staleAfter, nil
}

// parseObjects reads "<id>\t<run>[\t<created>]" lines and keeps the objects
// with a run label. It is pure.
func parseObjects(out []byte) []dockerObject {
	var objs []dockerObject
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Split(strings.TrimSpace(sc.Text()), "\t")
		if len(f) < 2 {
			continue
		}
		id, run := strings.TrimSpace(f[0]), strings.TrimSpace(f[1])
		if id == "" || run == "" || run == "<no value>" {
			continue
		}
		o := dockerObject{id: id, run: run}
		if len(f) > 2 {
			o.created, _ = parseDockerTime(f[2])
		}
		objs = append(objs, o)
	}
	return objs
}

// parseDockerTime reads a creation time as the docker CLI prints it: list
// formats print "2006-01-02 15:04:05 -0700 MST" (with or without a fraction
// of a second) and inspect prints RFC 3339. It is pure.
func parseDockerTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02 15:04:05 -0700 MST", time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
