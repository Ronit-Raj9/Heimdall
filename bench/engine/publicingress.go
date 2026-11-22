package engine

import (
	"time"

	"github.com/maximhq/bifrost/bench/publicingress"
	"github.com/maximhq/bifrost/bench/schemas"
)

// publicKeyMargin is how long past the agent's wall budget a run's key
// stays published, for the export and grading that follow the agent.
const publicKeyMargin = 15 * time.Minute

// publishKey adds the run's virtual key to the process's public ingress
// registry (bench/publicingress), so a deployed agent or a cloud sandbox can
// reach the gateway with it while the run is live. Without an ingress
// listener the entry is only an unused map row; cleanup drops it either way.
func publishKey(rc *schemas.RunContext) {
	if rc == nil || rc.Run == nil || rc.Key == nil || rc.Key.Value == "" {
		return
	}
	wall := time.Hour
	if rc.Agent != nil && rc.Agent.Budgets.WallS > 0 {
		wall = time.Duration(rc.Agent.Budgets.WallS) * time.Second
	}
	publicingress.Default.AddKey(rc.Run.RunID, rc.Key.Value, time.Now().Add(wall+publicKeyMargin))
}

// unpublishRun drops the run's key and tool gate from the public ingress.
func unpublishRun(rc *schemas.RunContext) {
	if rc == nil || rc.Run == nil {
		return
	}
	publicingress.Default.Drop(rc.Run.RunID)
}
