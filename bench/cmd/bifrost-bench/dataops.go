package main

// Data lifecycle wiring (lane X16, audits N10, N11, N17, N18):
//   - comparison delete (handlers.DataEraser): the experiment folder with its
//     runs, traces and gateway logs, its mirrored copy, then the indexes;
//   - the retention sweeper: comparisons older than an org's run_days go the
//     same way, hourly;
//   - the org deletion worker: picks up db.PendingOrgDeletions, removes the
//     org's folders, mirror prefix and every database row except the audit
//     log, and records the receipt; a restart resumes it;
//   - the webhook outbox worker (webhooks.ProcessDue every 15 s) and the
//     keystore-sealed webhook secrets;
//   - the org service's audit export and verify, and the API rate limits.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/evidencestore"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/maximhq/bifrost/bench/webhooks"
)

func init() {
	serviceHooks = append(serviceHooks, func(a *app, s *handlers.Services) {
		org := s.OrgID
		if org == "" {
			org = auth.LocalOrgID
		}
		// s.Reindex is read at call time: the reindex hook may run after
		// this one.
		s.Data = experimentEraser{orgID: org, storeRoot: a.store.Root(), logger: a.logger, reindex: func(ctx context.Context) error {
			if s.Reindex == nil {
				return nil
			}
			return s.Reindex(ctx)
		}}
		if c := webhookCipher(org, s.Hosted); c != nil {
			s.WebhookCipher = c
		}
	})
}

// expIDPattern is the store's experiment id alphabet: an id is never a path.
var expIDPattern = regexp.MustCompile(`^[a-z0-9_]{1,128}$`)

// experimentEraser implements handlers.DataEraser for one org.
type experimentEraser struct {
	orgID     string
	storeRoot string
	logger    schemas.Logger
	reindex   func(context.Context) error
}

// EraseExperiment removes the experiment's folder and mirror copy, then
// brings the indexes up to date.
func (e experimentEraser) EraseExperiment(ctx context.Context, expID string) error {
	if err := eraseExperimentData(ctx, e.orgID, e.storeRoot, expID); err != nil {
		return err
	}
	if e.reindex != nil {
		if err := e.reindex(ctx); err != nil && e.logger != nil {
			e.logger.Warn("index refresh after delete failed", "org", e.orgID, "exp", expID, "error", err.Error())
		}
	}
	return nil
}

// eraseExperimentData deletes <storeRoot>/<expID> (read-only sealed runs
// included) and every mirrored object of it. schemas.ErrNotFound when the
// folder does not exist.
func eraseExperimentData(ctx context.Context, orgID, storeRoot, expID string) error {
	if !expIDPattern.MatchString(expID) || storeRoot == "" {
		return fmt.Errorf("%w: comparison %q", schemas.ErrNotFound, expID)
	}
	dir := filepath.Join(storeRoot, expID)
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: comparison %s", schemas.ErrNotFound, expID)
	}
	if err := evidencestore.RemoveFolder(dir); err != nil {
		return err
	}
	if m := runningMirror(); m != nil {
		if _, err := m.EraseExperiment(ctx, orgID, expID); err != nil {
			return fmt.Errorf("comparison %s deleted here, but not from the evidence mirror: %w", expID, err)
		}
	}
	return nil
}

// runningMirror is the server's evidence mirror, nil when none runs.
func runningMirror() *evidencestore.Mirror {
	evidenceMu.Lock()
	defer evidenceMu.Unlock()
	if evidenceMirror == nil {
		return nil
	}
	return evidenceMirror.Mirror()
}

// ---- webhook secrets and outbox ----

// webhookCipher seals org's webhook secrets with the keystore's master key.
// Hosted servers always seal (they must have a master key; bootstrap makes
// one). A local server seals when a master key exists (the environment or
// ~/.bifrost-bench/master.key) and never creates one just for this.
func webhookCipher(orgID string, hosted bool) webhooks.Cipher {
	if !hosted && strings.TrimSpace(os.Getenv(keystore.MasterKeyEnv)) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		if _, err := os.Stat(filepath.Join(home, ".bifrost-bench", "master.key")); err != nil {
			return nil
		}
	}
	return &lazySealer{orgID: orgID}
}

// lazySealer opens the master key on first use, so building an org's
// services never fails on it; a webhook write without one does.
type lazySealer struct {
	orgID string
	once  sync.Once
	s     *keystore.Sealer
	err   error
}

func (l *lazySealer) get() (*keystore.Sealer, error) {
	l.once.Do(func() {
		master, err := keystore.MasterKey()
		if err != nil {
			l.err = err
			return
		}
		l.s, l.err = keystore.NewSealer(master, l.orgID, "webhook")
	})
	return l.s, l.err
}

func (l *lazySealer) Seal(id, p string) (string, error) {
	s, err := l.get()
	if err != nil {
		return "", err
	}
	return s.Seal(id, p)
}

func (l *lazySealer) Open(id, sealed string) (string, error) {
	s, err := l.get()
	if err != nil {
		return "", err
	}
	return s.Open(id, sealed)
}

// webhookStore is an org's webhook store with its secrets sealed.
func webhookStore(orgID, root string, hosted bool) *webhooks.Store {
	return webhooks.OpenWith(root, webhookCipher(orgID, hosted))
}

// outboxEvery is how often the outbox worker looks for due deliveries.
var outboxEvery = 15 * time.Second

// startWebhookOutbox runs the outbox worker until ctx ends: every
// outboxEvery, each org from orgs gets one ProcessDue pass. It covers orgs
// whose runtime is not built since the restart.
func startWebhookOutbox(ctx context.Context, logger schemas.Logger, orgs func() map[string]webhookOrg) {
	go func() {
		t := time.NewTicker(outboxEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			for id, o := range orgs() {
				if _, err := os.Stat(filepath.Join(o.root, ".heimdall", "webhooks", "outbox")); err != nil {
					continue
				}
				pctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
				_, err := webhookStore(id, o.root, o.hosted).ProcessDue(pctx, webhooks.Sender{AllowPrivate: !o.hosted}, time.Now().UTC())
				cancel()
				if err != nil {
					logger.Warn("webhook outbox pass failed", "org", id, "error", err.Error())
				}
			}
		}
	}()
}

// hostedOrgFolders lists every org folder of a hosted layout (projects and
// stores), by org id.
func hostedOrgFolders(layout tenancy.Layout) map[string]tenancy.OrgPaths {
	out := map[string]tenancy.OrgPaths{}
	for _, sub := range []string{tenancy.ProjectsDir, tenancy.OrgsDir} {
		ents, err := os.ReadDir(filepath.Join(layout.StorageRoot, sub))
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() || tenancy.ValidOrgID(e.Name()) != nil {
				continue
			}
			if p, err := layout.Paths(e.Name()); err == nil {
				out[e.Name()] = p
			}
		}
	}
	return out
}

// ---- retention ----

// retentionEvery is how often the sweeper runs; retentionFirst is the delay
// before its first pass after start.
var (
	retentionEvery = time.Hour
	retentionFirst = 2 * time.Minute
)

// retentionTarget is one org the sweeper looks at.
type retentionTarget struct {
	orgID string
	paths tenancy.OrgPaths
	// active reports whether an experiment is still queued or running.
	active func(ctx context.Context, expID string) bool
	// after runs once something was deleted (index refresh, audit row).
	after func(ctx context.Context, deleted []string)
}

// sweepRetention applies one org's retention setting at now and returns the
// comparisons it deleted.
func sweepRetention(ctx context.Context, t retentionTarget, now time.Time, logger schemas.Logger) ([]string, error) {
	r, err := handlers.ReadRetention(t.paths.ProjectRoot)
	if err != nil || r.RunDays <= 0 {
		return nil, err
	}
	st, err := store.NewFSStore(t.paths.StoreRoot, logger)
	if err != nil {
		return nil, err
	}
	ids, err := st.ListExperiments(ctx)
	if err != nil {
		return nil, err
	}
	cutoff := now.Add(-time.Duration(r.RunDays) * 24 * time.Hour)
	var deleted []string
	for _, id := range ids {
		m, err := st.LoadManifest(ctx, id)
		if err != nil || m.CreatedAt.IsZero() || !m.CreatedAt.Before(cutoff) {
			continue
		}
		if t.active != nil && t.active(ctx, id) {
			continue
		}
		if err := eraseExperimentData(ctx, t.orgID, t.paths.StoreRoot, id); err != nil {
			logger.Warn("retention delete failed", "org", t.orgID, "exp", id, "error", err.Error())
			continue
		}
		deleted = append(deleted, id)
	}
	sweptAt := now.UTC()
	r.LastSweepAt, r.LastDeleted = &sweptAt, len(deleted)
	if err := handlers.WriteRetention(t.paths.ProjectRoot, r); err != nil {
		logger.Warn("retention status not saved", "org", t.orgID, "error", err.Error())
	}
	if len(deleted) > 0 && t.after != nil {
		t.after(ctx, deleted)
	}
	return deleted, nil
}

// startRetention runs the sweeper over targets() until ctx ends.
func startRetention(ctx context.Context, logger schemas.Logger, targets func() []retentionTarget) {
	go func() {
		wait := retentionFirst
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			wait = retentionEvery
			for _, t := range targets() {
				sctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
				if _, err := sweepRetention(sctx, t, time.Now(), logger); err != nil {
					logger.Warn("retention sweep failed", "org", t.orgID, "error", err.Error())
				}
				cancel()
			}
		}
	}()
}

// startLocalDataOps starts the retention sweeper and the webhook outbox of
// a local server.
func startLocalDataOps(ctx context.Context, a *app) {
	paths := tenancy.OrgPaths{StoreRoot: a.store.Root(), ProjectRoot: a.cfg.Root}
	startRetention(ctx, a.logger, func() []retentionTarget {
		return []retentionTarget{{orgID: auth.LocalOrgID, paths: paths, active: recentlyActive(paths.StoreRoot)}}
	})
	startWebhookOutbox(ctx, a.logger, func() map[string]webhookOrg {
		return map[string]webhookOrg{auth.LocalOrgID: {root: a.cfg.Root}}
	})
	startLocalNotifications(ctx, a) // Y05 (notify.go)
}

// recentlyActive is the local server's activity check: an experiment whose
// folder changed in the last six hours may still be running.
func recentlyActive(storeRoot string) func(context.Context, string) bool {
	return func(_ context.Context, expID string) bool {
		newest := time.Time{}
		_ = filepath.WalkDir(filepath.Join(storeRoot, expID), func(_ string, d os.DirEntry, err error) error {
			if err == nil {
				if info, ierr := d.Info(); ierr == nil && info.ModTime().After(newest) {
					newest = info.ModTime()
				}
			}
			return nil
		})
		return time.Since(newest) < 6*time.Hour
	}
}

// ---- hosted: retention, outbox, org deletions ----

// orgDeleteKicks wakes the org deletion worker of a hosted env.
var orgDeleteKicks sync.Map // *hostedEnv -> chan struct{}

// startHostedDataOps starts the hosted workers: org deletions, retention and
// the webhook outbox.
func startHostedDataOps(ctx context.Context, env *hostedEnv) {
	kick := make(chan struct{}, 1)
	orgDeleteKicks.Store(env, kick)
	go orgDeletionLoop(ctx, env, kick)
	startRetention(ctx, env.base.logger, func() []retentionTarget { return hostedRetentionTargets(env) })
	startWebhookOutbox(ctx, env.base.logger, func() map[string]webhookOrg {
		out := map[string]webhookOrg{}
		for id, p := range hostedOrgFolders(env.layout) {
			out[id] = webhookOrg{root: p.ProjectRoot, hosted: true}
		}
		return out
	})
	startHostedNotifications(ctx, env) // Y05 (notify.go)
}

func hostedRetentionTargets(env *hostedEnv) []retentionTarget {
	var out []retentionTarget
	for id, p := range hostedOrgFolders(env.layout) {
		orgID := id
		paths := p
		out = append(out, retentionTarget{orgID: orgID, paths: paths,
			active: func(ctx context.Context, expID string) bool { return hostedExperimentActive(ctx, env.db, orgID, expID) },
			after: func(ctx context.Context, deleted []string) {
				if st, err := store.NewFSStore(paths.StoreRoot, env.base.logger); err == nil {
					if _, err := env.db.SyncOrg(ctx, orgID, st); err != nil {
						env.base.logger.Warn("index sync after retention failed", "org", orgID, "error", err.Error())
					}
				}
				_ = tenancy.BumpGeneration(paths.StoreRoot) // the cached runtime rebuilds its in-memory index
				if env.audit != nil {
					_ = env.audit.Audit(ctx, tenancy.AuditEntry{OrgID: orgID, ActorType: db.ActorSystem, ActorID: "retention",
						Action: "data.retention.delete", TargetType: "experiments", At: time.Now().UTC(),
						Details: map[string]any{"deleted": deleted, "count": len(deleted)}})
				}
			}})
	}
	return out
}

// hostedExperimentActive reports whether the experiment has a queued or
// running experiment.run job. On a database error it says active, so the
// sweeper never deletes what it could not check.
func hostedExperimentActive(ctx context.Context, d *db.DB, orgID, expID string) bool {
	var n int
	err := d.QueryRow(ctx, `SELECT COUNT(*) FROM jobs WHERE org_id = ? AND target = ? AND status IN ('queued', 'running')`,
		orgID, expTarget(expID)).Scan(&n)
	return err != nil || n > 0
}

// orgDeletionLoop finishes pending org deletions at start, on every kick and
// every minute (a failed one is retried).
func orgDeletionLoop(ctx context.Context, env *hostedEnv, kick <-chan struct{}) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		pctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		pending, err := env.db.PendingOrgDeletions(pctx)
		if err != nil {
			env.base.logger.Warn("org deletions not read", "error", err.Error())
		}
		for _, od := range pending {
			runOrgDeletion(pctx, env, od)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-kick:
		case <-t.C:
		}
	}
}

// runOrgDeletion removes one org's data and records the outcome. Every step
// can run again: a deletion interrupted half way resumes cleanly.
func runOrgDeletion(ctx context.Context, env *hostedEnv, od db.OrgDeletion) {
	logger := env.base.logger
	fail := func(step string, err error) {
		msg := step + ": " + err.Error()
		logger.Error("org deletion failed", "org", od.OrgID, "error", msg)
		_ = env.db.MarkOrgDeletion(ctx, od.OrgID, db.OrgDeletionFailed, msg, nil)
		if env.audit != nil {
			_ = env.audit.Audit(ctx, tenancy.AuditEntry{OrgID: od.OrgID, ActorType: db.ActorSystem, ActorID: "org-deletion",
				Action: "org.delete.failed", TargetType: "org", TargetID: od.OrgID, At: time.Now().UTC(), Details: map[string]any{"error": msg}})
		}
	}
	if err := env.db.MarkOrgDeletion(ctx, od.OrgID, db.OrgDeletionRunning, "", nil); err != nil {
		logger.Error("org deletion not started", "org", od.OrgID, "error", err.Error())
		return
	}
	if env.registry != nil {
		env.registry.Evict(od.OrgID)
	}
	paths, err := env.layout.Paths(od.OrgID)
	if err != nil {
		fail("folders", err)
		return
	}
	for _, dir := range []string{paths.StoreRoot, paths.ProjectRoot} {
		if err := evidencestore.RemoveFolder(dir); err != nil {
			fail("folders", err)
			return
		}
	}
	mirrored := 0
	if m := runningMirror(); m != nil {
		if mirrored, err = m.EraseOrg(ctx, od.OrgID); err != nil {
			fail("evidence mirror", err)
			return
		}
	}
	rows, err := env.db.PurgeOrgRows(ctx, od.OrgID)
	if err != nil {
		fail("database", err)
		return
	}
	webhookOrgsMu.Lock()
	delete(webhookOrgs, od.OrgID)
	webhookOrgsMu.Unlock()
	details := map[string]any{"rows": rows, "mirror_objects": mirrored, "folders": []string{paths.StoreRoot, paths.ProjectRoot}}
	if err := env.db.MarkOrgDeletion(ctx, od.OrgID, db.OrgDeletionDone, "", details); err != nil {
		logger.Error("org deletion receipt not saved", "org", od.OrgID, "error", err.Error())
	}
	if env.audit != nil {
		_ = env.audit.Audit(ctx, tenancy.AuditEntry{OrgID: od.OrgID, ActorType: db.ActorSystem, ActorID: "org-deletion",
			Action: "org.delete.completed", TargetType: "org", TargetID: od.OrgID, At: time.Now().UTC(), Details: details})
	}
	logger.Info("organization deleted", "org", od.OrgID, "slug", od.Slug)
}

// ---- org service: deletion and the audit chain ----

var _ handlers.OrgDataService = (*orgService)(nil)

// ScheduleOrgDeletion implements handlers.OrgDataService.
func (s *orgService) ScheduleOrgDeletion(ctx context.Context, orgID, requestedBy string, audit tenancy.AuditEntry) (handlers.OrgDeletionInfo, error) {
	e := tenancy.DBEntry(audit)
	od, err := s.env.db.ScheduleOrgDeletion(ctx, orgID, requestedBy, &e)
	if errors.Is(err, db.ErrConflict) {
		return handlers.OrgDeletionInfo{}, handlers.ErrOrgDeleting
	}
	if err != nil {
		return handlers.OrgDeletionInfo{}, dbErr(err)
	}
	if k, ok := orgDeleteKicks.Load(s.env); ok {
		select {
		case k.(chan struct{}) <- struct{}{}:
		default:
		}
	}
	return handlers.OrgDeletionInfo{OrgID: od.OrgID, Slug: od.Slug, Status: od.Status, RequestedBy: od.RequestedBy, RequestedAt: od.RequestedAt}, nil
}

// EachAuditEntry implements handlers.OrgDataService.
func (s *orgService) EachAuditEntry(ctx context.Context, orgID string, fn func(handlers.AuditExportRow) error) error {
	return s.env.db.EachAudit(ctx, orgID, func(e db.AuditEntry) error {
		return fn(handlers.AuditExportRow{ID: e.ID, OrgID: e.OrgID, Seq: e.Seq, PrevHash: e.PrevHash, Hash: e.Hash, ActorType: e.ActorType,
			ActorID: e.ActorID, Action: e.Action, TargetType: e.TargetType, TargetID: e.TargetID, IP: e.IP, At: e.At, Details: e.Details})
	})
}

// VerifyAudit implements handlers.OrgDataService.
func (s *orgService) VerifyAudit(ctx context.Context, orgID string) (handlers.AuditVerifyReport, error) {
	r, err := s.env.db.VerifyAuditChain(ctx, orgID)
	if err != nil {
		return handlers.AuditVerifyReport{}, err
	}
	return handlers.AuditVerifyReport(r), nil
}

// ---- API rate limits ----

// RateLimitsEnv tunes the API rate limits: "off", or four numbers
// "cred_burst,cred_per_sec,org_burst,org_per_sec". Unset: the defaults.
const RateLimitsEnv = "HEIMDALL_API_RATE_LIMITS"

// apiRateLimits builds the dispatcher's limits from RateLimitsEnv.
func apiRateLimits(logger schemas.Logger) *tenancy.RateLimits {
	c, err := parseRateLimits(os.Getenv(RateLimitsEnv))
	if err != nil {
		logger.Warn("bad "+RateLimitsEnv+", using the defaults", "error", err.Error())
		c = tenancy.DefaultRateLimits
	}
	if c.PerCredential.Burst == 0 && c.PerOrg.Burst == 0 {
		return nil
	}
	return tenancy.NewRateLimits(c, nil)
}

func parseRateLimits(v string) (tenancy.RateLimitConfig, error) {
	v = strings.TrimSpace(v)
	switch v {
	case "":
		return tenancy.DefaultRateLimits, nil
	case "off", "0":
		return tenancy.RateLimitConfig{}, nil
	}
	parts := strings.Split(v, ",")
	if len(parts) != 4 {
		return tenancy.RateLimitConfig{}, fmt.Errorf("want off or cred_burst,cred_per_sec,org_burst,org_per_sec, got %q", v)
	}
	var n [4]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || f < 0 {
			return tenancy.RateLimitConfig{}, fmt.Errorf("%q is not a number >= 0", p)
		}
		n[i] = f
	}
	shape := func(burst, perSec float64) auth.RateLimit {
		if burst <= 0 || perSec <= 0 {
			return auth.RateLimit{}
		}
		return auth.RateLimit{Burst: int(burst), Every: time.Duration(float64(time.Second) / perSec)}
	}
	// Share link buckets keep their defaults: the four numbers shape the
	// credential and org buckets only (share traffic never uses those).
	return tenancy.RateLimitConfig{PerCredential: shape(n[0], n[1]), PerOrg: shape(n[2], n[3]),
		PerShareLink: tenancy.DefaultShareLinkLimit, PerShareIP: tenancy.DefaultShareIPLimit}, nil
}
