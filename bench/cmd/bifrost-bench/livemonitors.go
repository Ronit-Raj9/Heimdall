package main

// Lane Z22: live synthetic monitoring (handlers/livemonitors.go,
// docs/live-monitors.md). Three hooks join monitors to the server:
//
//   - every running monitor is a schedule (addExtraSchedulesAt), so the
//     ticker fires its checks as schedule.fire and the daily budget,
//     overlap lock and hosted guardrails of every schedule apply;
//   - the ticker collects finished checks about once a minute
//     (monitorCollector): samples, the incident rules, notices;
//   - notices go to the org's webhook outbox as monitor.incident events
//     (deliverMonitorNotice), one alert thread per incident.

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/livemon"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/webhooks"
)

func init() {
	handlers.MonitorNotifier = deliverMonitorNotice
}

// collectEvery is how often the ticker collects finished monitor checks.
var collectEvery = time.Minute

// addExtraSchedulesAt adds the schedules that live outside the web file:
// scheduled canaries, then running live monitors.
func addExtraSchedulesAt(root string, f *schedule.File) {
	addCanarySchedulesAt(root, f)
	handlers.MonitorSchedules(root, f)
}

// monitorCollector returns the ticker's collect step for one org, run at
// most every collectEvery.
func monitorCollector(a *app, s handlers.Services) func(context.Context, time.Time) {
	if a == nil || a.cfg == nil || a.cfg.Root == "" {
		return nil
	}
	env := handlers.OrgEnv{Cfg: a.cfg, Mode: a.cfg.Server.Mode, Services: s}
	var mu sync.Mutex
	var last time.Time
	return func(ctx context.Context, now time.Time) {
		mu.Lock()
		defer mu.Unlock()
		if !last.IsZero() && now.Sub(last) < collectEvery {
			return
		}
		last = now
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		n, err := handlers.CollectMonitors(cctx, env, now)
		if a.logger == nil {
			return
		}
		if err != nil {
			a.logger.Warn("live monitors: collect failed", "org", s.OrgID, "error", err.Error())
		} else if n > 0 {
			a.logger.Info("live monitors: checks recorded", "org", s.OrgID, "checks", n)
		}
	}
}

// monitorIncidentData is the data of a monitor.incident event.
type monitorIncidentData struct {
	Action   string            `json:"action"` // opened | updated | resolved
	Message  string            `json:"message"`
	Link     string            `json:"link"`
	Monitor  monitorEventRef   `json:"monitor"`
	Incident livemon.Incident  `json:"incident"`
	Runs     []monitorEventRun `json:"failing_runs"`
}

type monitorEventRef struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Agent     string `json:"agent"`
	Benchmark string `json:"benchmark"`
}

type monitorEventRun struct {
	RunID   string `json:"run_id"`
	Task    string `json:"task"`
	Outcome string `json:"outcome"`
	Error   string `json:"error,omitempty"`
	Link    string `json:"link"`
}

// monitorIncidentEvent is the webhook event of one notice. Links are
// absolute when the server knows its public URL.
func monitorIncidentEvent(env handlers.OrgEnv, m livemon.Monitor, n livemon.Notice) webhooks.Event {
	base := ""
	if env.Cfg != nil {
		base = strings.TrimRight(env.Cfg.Auth.PublicURL, "/")
	}
	d := monitorIncidentData{Action: n.Event, Incident: n.Incident, Runs: []monitorEventRun{},
		Link:    base + "/scheduled-checks?tab=live&monitor=" + m.ID,
		Monitor: monitorEventRef{ID: m.ID, Name: m.Name, Agent: m.Agent, Benchmark: m.Benchmark}}
	for _, r := range n.Incident.FailingRuns {
		d.Runs = append(d.Runs, monitorEventRun{RunID: r.RunID, Task: r.Task, Outcome: r.Outcome, Error: r.Error, Link: base + "/runs/" + r.RunID})
	}
	verb := map[string]string{livemon.NoticeOpened: "Incident opened", livemon.NoticeUpdated: "Incident update", livemon.NoticeResolved: "Resolved"}[n.Event]
	d.Message = verb + " on monitor " + m.Name + " (" + m.Agent + "): " + n.Incident.Title + ". " + n.Incident.Detail
	ev := webhooks.NewEvent(webhooks.EventMonitorIncident, env.Services.OrgID, d)
	// The Y05 channel formats (Slack, Teams, PagerDuty, email) render the
	// Summary; one incident is one PagerDuty incident, resolved with it.
	sev := webhooks.SeverityError
	if n.Event == livemon.NoticeResolved {
		sev = webhooks.SeverityInfo
	}
	ev.Summary = &webhooks.Summary{Title: verb + " on monitor " + m.Name + ": " + n.Incident.Title, Text: n.Incident.Detail,
		Severity: sev, Link: d.Link, DedupKey: "heimdall-monitor-" + m.ID + "-" + n.Incident.ID,
		Resolve: n.Event == livemon.NoticeResolved,
		Fields: []webhooks.Field{{Label: "Agent", Value: m.Agent}, {Label: "Benchmark", Value: m.Benchmark},
			{Label: "Failing runs", Value: strconv.Itoa(len(d.Runs))}}}
	return ev
}

// deliverMonitorNotice sends a notice through the org's webhook outbox
// (every Y05 channel the org's hooks use) and to the members who chose
// monitor.incident by email.
func deliverMonitorNotice(env handlers.OrgEnv, m livemon.Monitor, n livemon.Notice) {
	org := env.Services.OrgID
	webhookOrgsMu.Lock()
	o, ok := webhookOrgs[org]
	webhookOrgsMu.Unlock()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	emitOrgEvent(ctx, org, o.root, o.hosted, monitorIncidentEvent(env, m, n))
}
