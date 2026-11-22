package main

// Lane Y05 (audit G4-08, G3 P1-10): notifications that reach people.
//
//   - Evaluation events: handlers builds an EvaluationNotice when an
//     evaluation ends (handlers/evalnotify.go); onEvaluationFinished sends
//     it as evaluation.finished, evaluation.regressed and release.blocked
//     through the org's webhook outbox (signed, retried; json, Slack,
//     Teams or PagerDuty bodies) and to the members who chose email, adds
//     it to the digest history and, for a scheduled evaluation, settles
//     the schedule's record so its verdict_regressed and policy_block
//     alerts fire.
//   - schedule.failed from the schedule.fire job, worker.offline from the
//     hosted runner rows (a watcher), and the weekly digest (hourly check,
//     sent after Monday 09:00 UTC).
//   - The mail transport comes from the environment (package mail): SMTP,
//     Resend or Postmark, else the server log only.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/mail"
	"github.com/maximhq/bifrost/bench/notify"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/webhooks"
)

func init() {
	serviceHooks = append(serviceHooks, rememberNotifyOrg)
	handlers.SetEvaluationNotifier(onEvaluationFinished)
}

// notifyOrg is an org whose services were built in this process: what the
// evaluation sweep needs to refresh its evaluations.
type notifyOrg struct {
	a *app
	s *handlers.Services
}

var (
	notifyOrgsMu sync.Mutex
	notifyOrgs   = map[string]notifyOrg{}
)

func rememberNotifyOrg(a *app, s *handlers.Services) {
	if a == nil || a.cfg == nil || a.cfg.Root == "" || s == nil {
		return
	}
	org := s.OrgID
	if org == "" {
		org = auth.LocalOrgID
	}
	notifyOrgsMu.Lock()
	notifyOrgs[org] = notifyOrg{a: a, s: s}
	notifyOrgsMu.Unlock()
}

func (o notifyOrg) env() handlers.OrgEnv {
	return handlers.OrgEnv{Cfg: o.a.cfg, Mode: o.a.cfg.Server.Mode, Services: *o.s}
}

// refreshOrgEvaluations brings an org's unfinished evaluations up to date
// (the job finish hook calls it after each experiment ends).
func refreshOrgEvaluations(org string) {
	if org == "" {
		org = auth.LocalOrgID
	}
	notifyOrgsMu.Lock()
	o, ok := notifyOrgs[org]
	notifyOrgsMu.Unlock()
	if ok {
		handlers.RefreshEvaluations(o.env())
	}
}

// emitOrgEvent sends ev through the org's webhook outbox and to the
// members who chose it by email.
func emitOrgEvent(ctx context.Context, org, root string, hosted bool, ev webhooks.Event) {
	if ev.Summary != nil {
		ev.Summary.Link = notify.Default().Absolute(ev.Summary.Link)
	}
	webhooks.Dispatch(ctx, webhookStore(org, root, hosted), webhooks.Sender{AllowPrivate: !hosted}, ev)
	if hub := notify.Default(); hub.EmailAvailable() {
		if _, err := hub.EmailEvent(ctx, org, root, ev); err != nil {
			notifyLog().Warn("notification email not sent", "org", org, "event", ev.Type, "error", err.Error())
		}
	}
}

// onEvaluationFinished is the handlers.EvaluationNotifier.
func onEvaluationFinished(env handlers.OrgEnv, ev *schemas.Evaluation, n handlers.EvaluationNotice) {
	if env.Cfg == nil || env.Cfg.Root == "" {
		return
	}
	org := firstNonEmptyStr(env.Services.OrgID, ev.Org, auth.LocalOrgID)
	root, hosted := env.Cfg.Root, env.Services.Hosted
	n.Link = notify.Default().Absolute(n.Link)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	for _, typ := range n.Events() {
		e := webhooks.NewEvent(typ, org, n)
		e.Summary = n.Summary(typ)
		emitOrgEvent(ctx, org, root, hosted, e)
	}
	if err := notify.Open(root).AppendHistory(notify.Record{At: n.FinishedAt, EvaluationID: n.EvaluationID, Name: n.Name, Agent: n.Agent, Version: n.Version,
		Status: n.Status, Score: n.Score, Change: n.Change, Regressed: n.Regressed, Blocked: len(n.Blocked) > 0, CostUSD: n.CostUSD, Schedule: n.Schedule}); err != nil {
		notifyLog().Warn("digest history not written", "org", org, "evaluation", ev.ID, "error", err.Error())
	}
	if ev.Schedule != "" && n.Status == schemas.EvaluationDone {
		if err := settleScheduledEvaluation(ctx, env, ev, n); err != nil {
			notifyLog().Warn("scheduled evaluation outcome not recorded", "org", org, "schedule", ev.Schedule, "evaluation", ev.ID, "error", err.Error())
		}
	}
}

// noRunner is the Runner of a daemon that only settles.
type noRunner struct{}

func (noRunner) Run(context.Context, schedule.Schedule) (*schedule.Result, error) {
	return nil, fmt.Errorf("this daemon only records outcomes")
}

// settleScheduledEvaluation records the finished evaluation's outcome on
// its schedule and sends the schedule's own alerts for it (the fix for
// schedule_eval.go, whose trigger only says "started").
func settleScheduledEvaluation(ctx context.Context, env handlers.OrgEnv, ev *schemas.Evaluation, n handlers.EvaluationNotice) error {
	root := env.Cfg.Root
	f, err := webScheduleFile(root)
	if err != nil {
		return err
	}
	s, ok := f.Find(ev.Schedule)
	if !ok {
		return fmt.Errorf("schedule %s no longer exists", ev.Schedule)
	}
	state := handlers.ScheduleStateDir(root)
	opts := schedule.Options{Root: root, StateDir: state, Runner: noRunner{}, Load: loadScheduled,
		Notifier: handlers.NewAlertSender(ctx, env.Services, state, false)}
	if !env.Services.Hosted && env.Cfg != nil {
		opts.ExtraAlerts = env.Cfg.Alerts
	}
	d, err := schedule.New(&schedule.File{Alerts: f.Alerts, Schedules: []schedule.Schedule{s}}, opts)
	if err != nil {
		return err
	}
	res := scheduledOutcome(n)
	_, err = d.Settle(ctx, s.Name, ev.CreatedAt, res)
	return err
}

// scheduledOutcome is the schedule Result of a finished evaluation.
func scheduledOutcome(n handlers.EvaluationNotice) *schedule.Result {
	res := &schedule.Result{EvaluationID: n.EvaluationID, Runs: n.Runs, Decision: string(schemas.DecisionInconclusive)}
	if n.CostUSD != nil {
		res.CostUSD = *n.CostUSD
	}
	if n.Regressed {
		res.Decision = string(schemas.DecisionRegressed)
	}
	if len(n.Blocked) > 0 {
		res.Policy = string(schemas.PolicyBlock)
	}
	parts := []string{fmt.Sprintf("evaluation %s finished", n.EvaluationID)}
	if n.Score != nil {
		p := "score " + handlers.Pct(*n.Score)
		if n.Change != nil && n.Baseline != nil {
			p += fmt.Sprintf(" (%s vs %s)", handlers.Points(*n.Change), map[bool]string{true: "the previous run", false: "the baseline"}[n.Baseline.Source == handlers.BaselineFromPreviousRun])
		}
		parts = append(parts, p)
	}
	if len(n.Regressions) > 0 {
		parts = append(parts, "regressed: "+strings.Join(n.Regressions, "; "))
	}
	if len(n.Blocked) > 0 {
		parts = append(parts, "release blocked: "+strings.Join(n.Blocked, "; "))
	}
	res.Summary = strings.Join(parts, "; ")
	return res
}

// scheduleFailedData is the data of a schedule.failed event.
type scheduleFailedData struct {
	Schedule     string `json:"schedule"`
	Status       string `json:"status"` // error, refused or failed (ran, and failed its check)
	Reason       string `json:"reason,omitempty"`
	JobID        string `json:"job_id"`
	ExpID        string `json:"exp_id,omitempty"`
	EvaluationID string `json:"evaluation_id,omitempty"`
	Link         string `json:"link,omitempty"`
}

// scheduleFailedEvent maps a finished schedule.fire job to schedule.failed
// when the trigger could not run or the run failed its check. A trigger
// skipped because the same schedule was still running is not a failure.
func scheduleFailedEvent(j jobs.Job, status jobs.Status, result []byte, msg string) (webhooks.Event, bool) {
	if j.Kind != kindScheduleFire {
		return webhooks.Event{}, false
	}
	var rec schedule.Record
	_ = json.Unmarshal(result, &rec)
	d := scheduleFailedData{Schedule: rec.Schedule, Status: rec.Status, Reason: rec.Reason, JobID: j.ID}
	if d.Schedule == "" {
		var p scheduleFireParams
		_ = json.Unmarshal(j.Params, &p)
		d.Schedule = p.Name
	}
	switch {
	case rec.Status == schedule.StatusSkipped:
		return webhooks.Event{}, false
	case rec.Status == schedule.StatusError, rec.Status == schedule.StatusRefused, rec.Status == schedule.StatusFailed:
	case status == jobs.StatusFailed:
		d.Status, d.Reason = schedule.StatusError, msg
	default:
		return webhooks.Event{}, false
	}
	if d.Reason == "" {
		d.Reason = msg
	}
	if r := rec.Result; r != nil {
		d.ExpID, d.EvaluationID = r.ExpID, r.EvaluationID
		if d.Reason == "" {
			d.Reason = r.Summary
		}
	}
	d.Link = "/scheduled-checks"
	ev := webhooks.NewEvent(webhooks.EventScheduleFailed, j.OrgID, d)
	title := fmt.Sprintf("Scheduled check %s failed", d.Schedule)
	switch d.Status {
	case schedule.StatusRefused:
		title = fmt.Sprintf("Scheduled check %s did not start", d.Schedule)
	case schedule.StatusError:
		title = fmt.Sprintf("Scheduled check %s could not finish", d.Schedule)
	}
	ev.Summary = &webhooks.Summary{Title: title, Text: d.Reason, Severity: webhooks.SeverityError, Link: d.Link,
		Fields: []webhooks.Field{{Label: "Schedule", Value: d.Schedule}, {Label: "Status", Value: d.Status}}, DedupKey: "heimdall-schedule-" + d.Schedule}
	return ev, true
}

// workerOfflineData is the data of a worker.offline event.
type workerOfflineData struct {
	WorkerID string    `json:"worker_id"`
	Name     string    `json:"name"`
	Pool     string    `json:"pool"`
	Host     string    `json:"host,omitempty"`
	LastSeen time.Time `json:"last_seen"`
	Link     string    `json:"link"`
}

func workerOfflineEvent(org string, r schemas.RunnerView) webhooks.Event {
	d := workerOfflineData{WorkerID: r.ID, Name: r.Name, Pool: r.Pool, Host: r.Host, LastSeen: r.LastSeen, Link: "/workers"}
	ev := webhooks.NewEvent(webhooks.EventWorkerOffline, org, d)
	ev.Summary = &webhooks.Summary{Title: fmt.Sprintf("Worker %s is offline", r.Name), Severity: webhooks.SeverityError, Link: d.Link,
		Text:     fmt.Sprintf("No heartbeat since %s. Evaluations on its pool wait until it is back or another worker takes them.", r.LastSeen.UTC().Format("2006-01-02 15:04 UTC")),
		Fields:   []webhooks.Field{{Label: "Worker", Value: r.Name}, {Label: "Pool", Value: firstNonEmptyStr(r.Pool, "default")}, {Label: "Host", Value: firstNonEmptyStr(r.Host, "unknown")}},
		DedupKey: "heimdall-worker-" + r.ID}
	return ev
}

// offlineTracker turns runner states into one worker.offline per drop.
type offlineTracker struct {
	mu   sync.Mutex
	seen map[string]string // org/runner id -> last state
}

// changes records the org's runners and returns the ones that just went
// lost. The first sight of a runner never alerts (a restart sees old rows).
func (t *offlineTracker) changes(org string, rs []schemas.RunnerView) []schemas.RunnerView {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.seen == nil {
		t.seen = map[string]string{}
	}
	var out []schemas.RunnerView
	for _, r := range rs {
		k := org + "/" + r.ID
		prev, known := t.seen[k]
		t.seen[k] = r.State
		if known && prev != schemas.RunnerLost && r.State == schemas.RunnerLost {
			out = append(out, r)
		}
	}
	return out
}

// ---- workers ----

var (
	notifyLoggerMu sync.RWMutex
	notifyLogger   schemas.Logger = schemas.NoopLogger{}
)

func notifyLog() schemas.Logger {
	notifyLoggerMu.RLock()
	defer notifyLoggerMu.RUnlock()
	return notifyLogger
}

// Intervals of the notification workers (tests shorten them).
var (
	evaluationSweepEvery = time.Minute
	digestCheckEvery     = time.Hour
	workerWatchEvery     = time.Minute
)

// notifyTarget is one org's project folder.
type notifyTarget struct {
	org, root string
	hosted    bool
}

// startNotifications installs the mail hub and starts the evaluation
// sweep and the weekly digest. members is nil on a server without
// accounts.
func startNotifications(ctx context.Context, logger schemas.Logger, publicURL string, members func(context.Context, string) ([]notify.Member, error), targets func() []notifyTarget) {
	notifyLoggerMu.Lock()
	notifyLogger = logger
	notifyLoggerMu.Unlock()
	sender, err := mail.FromEnv(logger)
	if err != nil {
		logger.Error("email is not configured correctly; emails go to the server log only", "error", err.Error())
		sender = mail.NewLog(logger)
	}
	logger.Info("notifications email transport", "provider", mail.ProviderOf(sender))
	notify.SetDefault(&notify.Hub{Mail: sender, PublicURL: publicURL, Members: members})

	go func() {
		t := time.NewTicker(evaluationSweepEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			notifyOrgsMu.Lock()
			orgs := make([]notifyOrg, 0, len(notifyOrgs))
			for _, o := range notifyOrgs {
				orgs = append(orgs, o)
			}
			notifyOrgsMu.Unlock()
			for _, o := range orgs {
				handlers.RefreshEvaluations(o.env())
			}
		}
	}()
	if members == nil {
		return
	}
	go func() {
		t := time.NewTicker(digestCheckEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			sendDigests(ctx, logger, targets(), time.Now())
		}
	}()
}

func sendDigests(ctx context.Context, logger schemas.Logger, ts []notifyTarget, now time.Time) {
	hub := notify.Default()
	for _, t := range ts {
		dctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		n, err := hub.SendDigest(dctx, t.org, t.root, now)
		cancel()
		if err != nil {
			logger.Warn("weekly digest not fully sent", "org", t.org, "sent", n, "error", err.Error())
		} else if n > 0 {
			logger.Info("weekly digest sent", "org", t.org, "emails", n)
		}
	}
}

// startWorkerWatch sends worker.offline when one of an org's runners goes
// silent (hosted).
func startWorkerWatch(ctx context.Context, list func(ctx context.Context, org string) ([]schemas.RunnerView, error), targets func() []notifyTarget) {
	tr := &offlineTracker{}
	go func() {
		t := time.NewTicker(workerWatchEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			for _, tg := range targets() {
				lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				rs, err := list(lctx, tg.org)
				if err == nil {
					for _, r := range tr.changes(tg.org, rs) {
						emitOrgEvent(lctx, tg.org, tg.root, tg.hosted, workerOfflineEvent(tg.org, r))
					}
				}
				cancel()
			}
		}
	}()
}

// startLocalNotifications starts notifications on a local server.
func startLocalNotifications(ctx context.Context, a *app) {
	pub := a.cfg.Auth.PublicURL
	if pub == "" && a.cfg.Server.Listen != "" {
		pub = "http://" + a.cfg.Server.Listen
	}
	startNotifications(ctx, a.logger, pub, nil, func() []notifyTarget {
		return []notifyTarget{{org: auth.LocalOrgID, root: a.cfg.Root}}
	})
}

// startHostedNotifications starts notifications on a hosted server: member
// email from the accounts database and the worker watch.
func startHostedNotifications(ctx context.Context, env *hostedEnv) {
	targets := func() []notifyTarget {
		var out []notifyTarget
		for id, p := range hostedOrgFolders(env.layout) {
			out = append(out, notifyTarget{org: id, root: p.ProjectRoot, hosted: true})
		}
		return out
	}
	members := func(ctx context.Context, org string) ([]notify.Member, error) {
		ms, err := env.db.Members(ctx, org)
		if err != nil {
			return nil, err
		}
		out := make([]notify.Member, 0, len(ms))
		for _, m := range ms {
			out = append(out, notify.Member{UserID: m.UserID, Email: m.Email, Name: m.Name, Disabled: m.Disabled})
		}
		return out, nil
	}
	startNotifications(ctx, env.base.logger, env.base.cfg.Auth.PublicURL, members, targets)
	startWorkerWatch(ctx, func(ctx context.Context, org string) ([]schemas.RunnerView, error) {
		v, ok := runnerEnvs.Load(env)
		if !ok {
			return nil, nil
		}
		return v.(*hostedRunners).svc.ListRunners(ctx, org)
	}, targets)
}

func firstNonEmptyStr(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
