package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// This file holds the hosted side of the coordinator (Runners area,
// docs/runners.md): one coordinator per organization, runners authenticated
// by runner tokens scoped to one organization and one pool. A local
// coordinator (RemoteOptions.OrgID empty) never reads a scope and behaves as
// before.

// maxLabels caps the labels one runner registers.
const maxLabels = 16

// hosted reports whether this is an organization's hosted coordinator.
func (c *Coordinator) hosted() bool { return c.opts.OrgID != "" }

// callScope returns the runner scope of a call. A hosted coordinator needs
// one of its own organization on every call; a local coordinator ignores it.
func (c *Coordinator) callScope(ctx context.Context) (schemas.RunnerScope, error) {
	if !c.hosted() {
		return schemas.RunnerScope{}, nil
	}
	s, ok := schemas.RunnerScopeFrom(ctx)
	if !ok || s.TokenID == "" || s.OrgID == "" || s.Pool == "" {
		return s, fmt.Errorf("%w: this coordinator serves hosted runners, which call with a runner token", schemas.ErrWorkerAuth)
	}
	if s.OrgID != c.opts.OrgID {
		return s, fmt.Errorf("%w: the runner token belongs to organization %s, not %s", schemas.ErrWorkerScope, s.OrgID, c.opts.OrgID)
	}
	return s, nil
}

// workerLocked returns a registered worker. On a hosted coordinator the call
// must come with the token the worker registered with: another token of the
// same organization can not act for it. The caller holds mu.
func (c *Coordinator) workerLocked(workerID string, scope schemas.RunnerScope) (*remoteWorker, error) {
	w, ok := c.workers[workerID]
	if !ok {
		return nil, fmt.Errorf("worker %s: %w", workerID, schemas.ErrWorkerUnknown)
	}
	if c.hosted() && w.scope.TokenID != scope.TokenID {
		return nil, fmt.Errorf("worker %s: %w: it registered with another runner token", workerID, schemas.ErrWorkerScope)
	}
	w.restored = false // its old token called: the old process is alive (remote.go)
	return w, nil
}

// openTo reports whether an attached experiment is offered to a worker: on a
// hosted coordinator only when its runner pool (schemas.DefaultRunnerPool
// when it names none) is the worker's pool.
func (c *Coordinator) openTo(w *remoteWorker, r *schedRun) bool {
	return !c.hosted() || poolServes(w.scope.Pool, r.m.Experiment.RunnerPool())
}

// poolServes reports whether a worker in pool workerPool may run an
// experiment of pool expPool: its own pool, and the shared runner also takes
// experiments that name no pool, so an org with no runner of its own can run.
func poolServes(workerPool, expPool string) bool {
	return workerPool == expPool || (workerPool == schemas.SharedRunnerPool && expPool == schemas.DefaultRunnerPool)
}

// infoLocked copies a worker's list entry. The caller holds mu.
func (c *Coordinator) infoLocked(w *remoteWorker) schemas.WorkerInfo {
	info := w.info
	info.Rejected = w.rejected // QC2-08-03: the operator sees how close a worker is to the lease stop
	info.Running = append([]string{}, info.Running...)
	sort.Strings(info.Running)
	if info.Labels != nil {
		info.Labels = append([]string(nil), info.Labels...)
	}
	return info
}

func (c *Coordinator) infoOf(w *remoteWorker) schemas.WorkerInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.infoLocked(w)
}

// book hands a hosted runner's state to the RunnerBook, if any.
func (c *Coordinator) book(ctx context.Context, scope schemas.RunnerScope, info schemas.WorkerInfo, registered bool) {
	if c.hosted() && c.opts.Book != nil {
		c.opts.Book.RunnerSeen(ctx, scope, info, registered)
	}
}

// Whoami answers GET /workers/whoami: what the caller's token is scoped to,
// the workers registered with it, and how many attached experiments it may
// lease from now. A local coordinator lists every worker.
func (c *Coordinator) Whoami(ctx context.Context) (*schemas.RunnerWhoami, error) {
	scope, err := c.callScope(ctx)
	if err != nil {
		return nil, err
	}
	out := &schemas.RunnerWhoami{Mode: "local", Workers: []schemas.WorkerInfo{}, Now: c.now().UTC()}
	if c.hosted() {
		out.Mode, out.OrgID, out.Pool, out.TokenID = "hosted", scope.OrgID, scope.Pool, scope.TokenID
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range c.order {
		w := c.workers[id]
		if !c.hosted() || w.scope.TokenID == scope.TokenID {
			out.Workers = append(out.Workers, c.infoLocked(w))
		}
	}
	for _, r := range c.exps {
		if !c.hosted() || poolServes(scope.Pool, r.m.Experiment.RunnerPool()) {
			out.Open++
		}
	}
	return out, nil
}

// Forget removes a registered worker (an admin removed the runner): its
// leases are lost at once under the lease-loss rules and its next call is
// answered as an unknown worker. It reports whether the worker was known.
func (c *Coordinator) Forget(workerID, why string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.workers[workerID]; !ok {
		return false
	}
	c.forgetLocked(workerID, why)
	return true
}

// ForgetToken removes every worker registered with a runner token (the
// token was revoked) and returns how many there were.
func (c *Coordinator) ForgetToken(tokenID, why string) int {
	if tokenID == "" {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var ids []string
	for id, w := range c.workers {
		if w.scope.TokenID == tokenID {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		c.forgetLocked(id, why)
	}
	return len(ids)
}

func (c *Coordinator) forgetLocked(workerID, why string) {
	for _, l := range c.leases {
		if l.worker == workerID && !l.closing {
			c.loseLocked(l, why)
		}
	}
	delete(c.workers, workerID)
	for i, id := range c.order {
		if id == workerID {
			c.order = append(c.order[:i:i], c.order[i+1:]...)
			break
		}
	}
	c.logger.Warn("remote worker removed", "worker", workerID, "why", why)
}

// validLabels checks a registration's pool and labels: the pool empty or a
// pool name, at most maxLabels labels of 1 to 64 letters, digits, '.', '_',
// '-', ':' or '='.
func validLabels(pool string, labels []string) error {
	if pool != "" && !schemas.ValidPoolName(pool) {
		return fmt.Errorf("%w: pool %q must be 1 to 64 letters, digits, '.', '_' or '-'", schemas.ErrWorkerRequest, pool)
	}
	if len(labels) > maxLabels {
		return fmt.Errorf("%w: at most %d labels", schemas.ErrWorkerRequest, maxLabels)
	}
	for _, l := range labels {
		ok := l != "" && len(l) <= 64
		for _, r := range l {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' || r == ':' || r == '=') {
				ok = false
			}
		}
		if !ok {
			return fmt.Errorf("%w: label %q must be 1 to 64 letters, digits, '.', '_', '-', ':' or '='", schemas.ErrWorkerRequest, l)
		}
	}
	return nil
}

// FetchWhoami asks a coordinator what token is scoped to (GET
// /api/bench/workers/whoami), for `bifrost-bench runner status`. Errors
// wrap the worker API's sentinels: ErrWorkerAuth for a token the server
// refuses, ErrWorkerScope for a token of another organization.
func FetchWhoami(ctx context.Context, server, token string, httpc *http.Client) (*schemas.RunnerWhoami, error) {
	u, err := url.Parse(server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("server %q is not an http(s) URL", server)
	}
	if httpc == nil {
		httpc = &http.Client{Timeout: 30 * time.Second}
	}
	w := &Worker{opts: WorkerOptions{Server: strings.TrimRight(server, "/"), Token: token, HTTP: httpc}}
	var out schemas.RunnerWhoami
	if err := w.call(ctx, http.MethodGet, workerAPI+"/whoami", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
