package environments

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Stream memory carry, the process probe's kill and world ticks
// (schemas/stream.go) for the docker workspace.

var (
	_ schemas.MemoryCarrier    = (*dockerWorkspace)(nil)
	_ schemas.AgentKiller      = (*dockerWorkspace)(nil)
	_ schemas.WorldEnvironment = (*dockerWorkspace)(nil)
)

// maxMemoryArchive caps the memory one stream run hands to the next.
const maxMemoryArchive = 64 << 20

// archiveScript tars every memory volume (mounted read-only at
// /bench-mem/<i>) to stdout, owners kept as numbers, and stops one byte past
// the cap so the caller can tell an oversized archive from a full one.
const archiveScript = `set -eu
cd /bench-mem
tar -cf - --numeric-owner . | head -c "$BENCH_MAX"
`

// seedScript unpacks a memory archive over the fresh memory volumes,
// owners and modes as archived. Only the top-level folders that are mounted
// volumes receive files; GNU tar strips absolute paths and "..".
const seedScript = `set -eu
cd /bench-mem
for d in $(tar -tf /bench-seed.tar | sed -n 's#^\./\([0-9][0-9]*\)/.*#\1#p' | sort -u); do
	[ -d "/bench-mem/$d" ] || continue
	tar -xpf /bench-seed.tar --numeric-owner -C /bench-mem "./$d"
done
`

// ArchiveMemory writes the run's memory volumes to dst as a tar archive with
// one top-level folder per volume (0 is /memory). A run without memory
// volumes writes nothing and is not an error.
func (e *dockerWorkspace) ArchiveMemory(ctx context.Context, rc *schemas.RunContext, dst string) error {
	expID, runID, err := runIDs(rc)
	if err != nil {
		return err
	}
	if !memoryEnabled(rc) {
		return nil
	}
	paths, err := memoryPaths(rc)
	if err != nil {
		return err
	}
	args := e.helperArgs(helperName(runID, "memtar"), expID, runID, []string{"DAC_READ_SEARCH"})
	for i, v := range viewFor(rc).memoryVolumes(runID, len(paths)) {
		args = append(args, "-v", v+":/bench-mem/"+strconv.Itoa(i)+":ro")
	}
	args = append(args, "-e", "BENCH_MAX="+strconv.Itoa(maxMemoryArchive+1), e.helperImage(rc), "-c", archiveScript)
	res, err := e.cfg.Docker.MustRun(ctx, args...)
	if err != nil {
		return fmt.Errorf("archive memory: %w", err)
	}
	if len(res.Stdout) > maxMemoryArchive {
		return fmt.Errorf("archive memory: the memory is larger than %d MiB", maxMemoryArchive>>20)
	}
	if err := os.WriteFile(dst, res.Stdout, 0o644); err != nil {
		return fmt.Errorf("archive memory: %w", err)
	}
	return nil
}

// SeedMemory unpacks src (an archive ArchiveMemory wrote) over the run's
// fresh memory volumes. Create has made the volumes, so it runs right after
// Create and before the agent starts.
func (e *dockerWorkspace) SeedMemory(ctx context.Context, rc *schemas.RunContext, src string) error {
	expID, runID, err := runIDs(rc)
	if err != nil {
		return err
	}
	if !memoryEnabled(rc) {
		return fmt.Errorf("seed memory for run %s: the run has no memory volumes", runID)
	}
	paths, err := memoryPaths(rc)
	if err != nil {
		return err
	}
	if st, err := os.Stat(src); err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("seed memory for run %s: %s is not a file", runID, src)
	}
	args := e.helperArgs(helperName(runID, "memseed"), expID, runID, []string{"CHOWN", "DAC_OVERRIDE", "DAC_READ_SEARCH", "FOWNER"})
	for i, v := range viewFor(rc).memoryVolumes(runID, len(paths)) {
		args = append(args, "-v", v+":/bench-mem/"+strconv.Itoa(i))
	}
	args = append(args, "-v", src+":/bench-seed.tar:ro", e.helperImage(rc), "-c", seedScript)
	if _, err := e.cfg.Docker.MustRun(ctx, args...); err != nil {
		return fmt.Errorf("seed memory: %w", err)
	}
	return nil
}

// KillAgent sends SIGKILL to the run's running agent container.
func (e *dockerWorkspace) KillAgent(ctx context.Context, rc *schemas.RunContext) error {
	_, runID, err := runIDs(rc)
	if err != nil {
		return err
	}
	if _, err := e.cfg.Docker.MustRun(ctx, "kill", "--signal", "KILL", agentName(rc, runID)); err != nil {
		return fmt.Errorf("kill agent: %w", err)
	}
	return nil
}

// tickScript POSTs one JSON body and prints the status; a connection error
// prints 0 and the error.
const tickScript = `import sys, urllib.request, urllib.error
req = urllib.request.Request(sys.argv[1], data=sys.argv[2].encode(), method="POST", headers={"Content-Type": "application/json"})
try:
    r = urllib.request.urlopen(req, timeout=10)
    print(r.status)
except urllib.error.HTTPError as e:
    print(e.code)
except Exception as e:
    print(0)
    print(e, file=sys.stderr)
`

// worldTickBody is what a service's tick hook receives.
type worldTickBody struct {
	Session int      `json:"session"`
	Date    string   `json:"date"`
	Actions []string `json:"actions"`
}

// WorldTick POSTs the tick hook of every task and suite service with a
// port, straight to the service on the run's network (never through the
// tool proxy), from a short-lived probe container. A service that events
// name must answer 2xx; any other service may ignore the hook (a 404 or no
// answer is recorded, not an error).
func (e *dockerWorkspace) WorldTick(ctx context.Context, rc *schemas.RunContext, session int, date string, events []schemas.WorldEvent) ([]schemas.WorldTick, error) {
	expID, runID, err := runIDs(rc)
	if err != nil {
		return nil, err
	}
	svcs, err := runServices(rc)
	if err != nil {
		return nil, fmt.Errorf("world tick: %w", err)
	}
	actions := map[string][]string{}
	for _, ev := range events {
		actions[ev.Service] = append(actions[ev.Service], ev.Action)
	}
	hidden := false
	var targets []schemas.ServiceSpec
	for _, s := range svcs {
		if s.spec.Port <= 0 {
			if len(actions[s.spec.ID]) > 0 {
				return nil, fmt.Errorf("world tick: service %s has events but no port", s.spec.ID)
			}
			continue
		}
		hidden = hidden || s.spec.Hidden || proxied(s.spec)
		targets = append(targets, s.spec)
	}
	if len(targets) == 0 {
		return nil, nil
	}
	probe, err := e.startProbe(ctx, rc, expID, runID, hidden)
	if err != nil {
		return nil, fmt.Errorf("world tick: %w", err)
	}
	defer e.removeContainers(context.WithoutCancel(ctx), probe)
	var ticks []schemas.WorldTick
	var failed []string
	for _, s := range targets {
		acts := actions[s.ID]
		body, _ := json.Marshal(worldTickBody{Session: session, Date: date, Actions: append([]string{}, acts...)})
		t := schemas.WorldTick{Session: session, Date: date, Service: s.ID, Actions: acts}
		res, err := e.cfg.Docker.Run(ctx, "exec", probe, "python3", "-c", tickScript, upstreamURL(s)+schemas.WorldTickPath, string(body))
		switch {
		case err != nil:
			t.Error = err.Error()
		default:
			out := strings.Fields(string(res.Stdout))
			if len(out) > 0 {
				t.Status, _ = strconv.Atoi(out[0])
			}
			if t.Status == 0 {
				t.Error = truncate(strings.TrimSpace(string(res.Stderr)), 300)
			}
		}
		if len(acts) > 0 && (t.Status < 200 || t.Status > 299) {
			failed = append(failed, fmt.Sprintf("%s (status %d %s)", s.ID, t.Status, t.Error))
		}
		ticks = append(ticks, t)
	}
	if len(failed) > 0 {
		return ticks, fmt.Errorf("world tick: events not delivered: %s", strings.Join(failed, ", "))
	}
	return ticks, nil
}
