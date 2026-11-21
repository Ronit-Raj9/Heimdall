package schemas

import (
	"fmt"
	"strings"
)

// Where an organization's runs go (FIX_BRIEF decision 6): one per-org
// setting over the two ways to bring your own compute. A cloud sandbox
// platform (Modal, Daytona, E2B) starts a fresh sandbox per run; an own
// worker (Runloop, Fly Machines, Kubernetes, a VM) is a long-lived runner
// with its own pool. The setting is one of:
//
//	shared             the shared worker
//	platform:<name>    a fresh sandbox per run on a connected platform
//	worker:<name>      one of the org's own workers
//
// It replaces the separate defaults of /worker-platforms/default and
// /workers/default, which stay as aliases that set it.

// Worker target kinds (WorkerTargetChoice.Kind) and the shared target.
const (
	WorkerTargetShared   = "shared"
	WorkerTargetPlatform = "platform"
	WorkerTargetWorker   = "worker"
)

// WorkerTargetView is GET /api/bench/workers/target: where runs go now and
// every choice the organization has.
type WorkerTargetView struct {
	Target  string               `json:"target"`
	Choices []WorkerTargetChoice `json:"choices"`
}

// WorkerTargetChoice is one place runs can go. PerEvaluation is false for
// a choice an evaluation cannot pick on its own (a platform that is not
// where runs go, or the shared worker while a platform is: both share the
// shared worker's queue); Note says why.
type WorkerTargetChoice struct {
	Target        string `json:"target"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Title         string `json:"title"`
	Status        string `json:"status,omitempty"`
	Current       bool   `json:"current"`
	PerEvaluation bool   `json:"per_evaluation"`
	Note          string `json:"note,omitempty"`
}

// WorkerTargetSet is the body of PUT /api/bench/workers/target.
type WorkerTargetSet struct {
	Target string `json:"target"`
}

// ParseWorkerTarget splits a target into its kind and name: shared (also
// "" and the pool names default and shared), platform:<name> or
// worker:<name>.
func ParseWorkerTarget(t string) (kind, name string, err error) {
	t = strings.TrimSpace(t)
	switch t {
	case "", WorkerTargetShared, DefaultRunnerPool:
		return WorkerTargetShared, "", nil
	}
	k, n, ok := strings.Cut(t, ":")
	n = strings.TrimSpace(n)
	if !ok || n == "" || (k != WorkerTargetPlatform && k != WorkerTargetWorker) {
		return "", "", fmt.Errorf("target %q: want shared, platform:<modal|daytona|e2b> or worker:<name>", t)
	}
	return k, n, nil
}

// WorkerTargetOf joins a kind and a name into a target.
func WorkerTargetOf(kind, name string) string {
	if kind == WorkerTargetShared || name == "" {
		return WorkerTargetShared
	}
	return kind + ":" + name
}
