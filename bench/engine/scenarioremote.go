package engine

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// remoteTaskErrs refuses each remote arm that cannot produce task t's output
// (utils.RemoteOutputAllowed), and a state task for a remote arm when the
// task has no proxied service or external API to publish to it. arms is in
// sorted order; cfg holds each arm's remote block. It is pure.
func remoteTaskErrs(suite *schemas.Suite, t *schemas.Task, arms []string, cfg map[string]*schemas.RemoteAgent) []error {
	var errs []error
	kind := utils.OutputKind(suite, t)
	for _, arm := range arms {
		r := cfg[arm]
		if err := utils.CheckRemoteAgent(r); err != nil {
			errs = append(errs, fmt.Errorf("arm %s: %w", arm, err))
			continue
		}
		if ok, why := utils.RemoteOutputAllowed(r, kind); !ok {
			errs = append(errs, fmt.Errorf("task %s has output %s, but arm %s runs a remote agent: %s", t.ID, kind, arm, why))
			continue
		}
		if kind == schemas.OutputState && !hasPublishable(suite, t) {
			errs = append(errs, fmt.Errorf("task %s has output state, but has no proxied service or external API that arm %s's remote agent could change", t.ID, arm))
		}
	}
	return errs
}

// hasPublishable reports whether the run of t has anything the remote tool
// gate would publish: a proxied, non-hidden service or an external API.
func hasPublishable(suite *schemas.Suite, t *schemas.Task) bool {
	if len(utils.ExternalAPIs(suite, t)) > 0 {
		return true
	}
	for _, s := range utils.TaskServices(suite, t) {
		if !s.Hidden && utils.ServiceProxied(s) {
			return true
		}
	}
	return false
}
