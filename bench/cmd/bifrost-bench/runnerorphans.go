package main

// Leftover run containers on the shared runner's host (fix wave 2, E5).
// A run's containers and networks are removed by the run's own cleanup
// stage; a run cut short by a runner restart or a crash leaves them behind,
// and the janitor (engine/janitor.go), which decides from lease files and
// project labels, kept such leftovers up for days on the live host. This
// sweep removes the run containers and networks of runs this runner process
// does not own, at start-up and every orphanSweepEvery. It only touches an
// object when every one of these holds:
//
//   - its name starts with "bench-" (the per-run prefix) and it carries a
//     run label (utils.LabelRun);
//   - it belongs to no compose project (the platform's own services, and
//     anything else an operator runs with compose, are never touched);
//   - its project label is empty or this runner's project (utils.Project());
//   - its run is not active in any worker of this process;
//   - it was created at least orphanMinAge ago.
//
// Containers go first (their networks cannot be removed while attached),
// then networks. A failure is logged and the sweep goes on.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const (
	// orphanMinAge is the youngest leftover the sweep removes. A run of this
	// process is protected by its active mark; the age only guards objects of
	// a run that is starting while the sweep lists.
	orphanMinAge = 30 * time.Minute
	// orphanSweepEvery is how often the shared runner sweeps after start.
	orphanSweepEvery = 15 * time.Minute
	// orphanNamePrefix starts the name of every per-run container and network.
	orphanNamePrefix = "bench-"
	// composeProjectLabel is the label docker compose puts on its objects.
	composeProjectLabel = "com.docker.compose.project"
	// orphanLogRuns caps the run ids one sweep line names.
	orphanLogRuns = 20
)

// orphanObject is one listed container or network.
type orphanObject struct {
	id, name       string
	created        time.Time
	createdKnown   bool
	run, project   string
	composeProject string
	network        bool
}

// orphanFormat is the --format of both listings: id, name, creation time,
// run, project and compose project.
func orphanFormat(nameField string) string {
	return "{{.ID}}\t{{." + nameField + "}}\t{{.CreatedAt}}\t{{.Label \"" + utils.LabelRun + "\"}}\t{{.Label \"" +
		utils.LabelProject + "\"}}\t{{.Label \"" + composeProjectLabel + "\"}}"
}

// parseOrphanObjects reads one listing.
func parseOrphanObjects(out []byte, network bool) []orphanObject {
	var objs []orphanObject
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		for len(f) < 6 {
			f = append(f, "")
		}
		o := orphanObject{id: strings.TrimSpace(f[0]), name: strings.TrimSpace(f[1]), run: strings.TrimSpace(f[3]),
			project: strings.TrimSpace(f[4]), composeProject: strings.TrimSpace(f[5]), network: network}
		o.created, o.createdKnown = parseOrphanTime(strings.TrimSpace(f[2]))
		objs = append(objs, o)
	}
	return objs
}

// parseOrphanTime reads docker's CreatedAt ("2026-11-21 16:49:15 +0000 UTC",
// networks with fractional seconds).
func parseOrphanTime(s string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04:05 -0700 MST", "2006-01-02 15:04:05 -0700", time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// orphanRemovable applies the sweep's rules to one object.
func orphanRemovable(o orphanObject, project string, active map[string]bool, now time.Time, minAge time.Duration) bool {
	switch {
	case o.run == "" || !strings.HasPrefix(o.name, orphanNamePrefix):
		return false
	case o.composeProject != "":
		return false
	case o.project != "" && o.project != project:
		return false
	case active[o.run]:
		return false
	case !o.createdKnown || now.Sub(o.created) < minAge:
		return false
	}
	return true
}

// orphanSweepReport is what one sweep removed.
type orphanSweepReport struct {
	Containers, Networks int
	Runs                 []string
}

// sweepOrphanRuns removes the run containers and networks of runs this
// process does not own (rules at the top of the file). active is read once,
// after both listings, so a run that starts while the sweep lists is kept.
func sweepOrphanRuns(ctx context.Context, d utils.Docker, active func() map[string]bool, now time.Time, minAge time.Duration, logger schemas.Logger) orphanSweepReport {
	var rep orphanSweepReport
	ctrs, err := d.MustRun(ctx, "ps", "-a", "--no-trunc", "--filter", "label="+utils.LabelRun, "--format", orphanFormat("Names"))
	if err != nil {
		logger.Warn("shared runner: leftover run containers not listed", "error", err.Error())
		return rep
	}
	nets, err := d.MustRun(ctx, "network", "ls", "--no-trunc", "--filter", "label="+utils.LabelRun, "--format", orphanFormat("Name"))
	if err != nil {
		logger.Warn("shared runner: leftover run networks not listed", "error", err.Error())
		nets = &utils.DockerResult{}
	}
	act := map[string]bool{}
	if active != nil {
		if a := active(); a != nil {
			act = a
		}
	}
	project := utils.Project()
	runs := map[string]bool{}
	for _, o := range parseOrphanObjects(ctrs.Stdout, false) {
		if !orphanRemovable(o, project, act, now, minAge) {
			continue
		}
		if _, err := d.MustRun(ctx, "rm", "-f", "-v", o.id); err != nil {
			logger.Warn("shared runner: leftover run container not removed", "container", o.name, "run", o.run, "error", err.Error())
			continue
		}
		rep.Containers++
		runs[o.run] = true
	}
	for _, o := range parseOrphanObjects(nets.Stdout, true) {
		if !orphanRemovable(o, project, act, now, minAge) {
			continue
		}
		if _, err := d.MustRun(ctx, "network", "rm", o.id); err != nil {
			logger.Warn("shared runner: leftover run network not removed", "network", o.name, "run", o.run, "error", err.Error())
			continue
		}
		rep.Networks++
		runs[o.run] = true
	}
	for r := range runs {
		rep.Runs = append(rep.Runs, r)
	}
	sort.Strings(rep.Runs)
	named := rep.Runs
	if len(named) > orphanLogRuns {
		named = named[:orphanLogRuns]
	}
	logger.Info(fmt.Sprintf("shared runner: removed leftover run containers: %d container(s), %d network(s) of %d run(s)", rep.Containers, rep.Networks, len(rep.Runs)),
		"runs", strings.Join(named, ","))
	return rep
}

// runOrphanSweeps sweeps once now and then every orphanSweepEvery until ctx
// ends.
func runOrphanSweeps(ctx context.Context, d utils.Docker, active func() map[string]bool, logger schemas.Logger) {
	sweepOrphanRuns(ctx, d, active, time.Now(), orphanMinAge, logger)
	tick := time.NewTicker(orphanSweepEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			sweepOrphanRuns(ctx, d, active, time.Now(), orphanMinAge, logger)
		}
	}
}
