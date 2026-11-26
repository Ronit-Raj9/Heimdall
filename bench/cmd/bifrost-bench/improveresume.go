package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/notify"
	"github.com/maximhq/bifrost/bench/webhooks"
)

// Improvement runs, wave 4 (D5 WP6): resume, continue, restart safety and
// the notification events.

// Resume drives a planned, failed or interrupted run again in the
// background. Nothing already paid for runs again: the record keeps every
// finished stage.
func (s improveSource) Resume(_ context.Context, id, _ string) (*improve.Summary, error) {
	if s.offline {
		return nil, improve.ErrOffline
	}
	rec, err := s.r.CanResume(id)
	if err != nil {
		return nil, err
	}
	s.start(id)
	sum := improve.Summarize(rec, true)
	return &sum, nil
}

// Continue runs the second look of a finished run in the background.
func (s improveSource) Continue(_ context.Context, id, _ string, in improve.ContinueRequest) (*improve.Summary, error) {
	if s.offline {
		return nil, improve.ErrOffline
	}
	rec, err := s.r.CheckContinue(id)
	if err != nil {
		return nil, err
	}
	extend := improve.RunExtend
	go func() {
		if _, err := extend(s.r, context.Background(), id, in.ExtraTrials, in.NewTasks, in.ExtraBudgetUSD); err != nil && s.r.Logger != nil {
			s.r.Logger.Warn("improvement second look ended with an error", "improvement", id, "error", err.Error())
		}
	}()
	sum := improve.Summarize(rec, true)
	return &sum, nil
}

// ContinueEstimate is what a second look would run and cost.
func (s improveSource) ContinueEstimate(ctx context.Context, id string, in improve.ContinueRequest) (*improve.ContinueOffer, error) {
	rec, err := s.r.Get(id)
	if err != nil {
		return nil, err
	}
	if err := improve.CanContinue(rec); err != nil && rec.Spec.Claim.Extension {
		return nil, err
	}
	fresh := 0
	if in.NewTasks && s.r.Exec != nil {
		infos, err := s.r.Exec.Tasks(ctx, &rec.Spec)
		if err != nil {
			return nil, fmt.Errorf("list the benchmark's tasks: %w", err)
		}
		used := slices.Concat(rec.Splits.Dev, rec.Splits.Audit, rec.Splits.Sealed, rec.Splits.Honeypots)
		for _, t := range infos {
			if !t.Impossible && !slices.Contains(used, t.ID) {
				fresh++
			}
		}
	}
	perRun := 0.0
	if rec.Estimate != nil {
		perRun = rec.Estimate.USD.PerRun
	}
	return improve.ContinueOfferFor(rec, in, fresh, perRun), nil
}

// resumeImprovements drives again, on a local server's start, every run the
// last stop left running.
func resumeImprovements(ctx context.Context, src improveSource) {
	if src.offline || ctx.Err() != nil {
		return
	}
	ids, err := src.r.ResumeRunning()
	if err != nil {
		if src.r.Logger != nil {
			src.r.Logger.Warn("could not list improvement runs to resume", "error", err.Error())
		}
		return
	}
	for _, id := range ids {
		if src.r.Logger != nil {
			src.r.Logger.Info("resuming an improvement run the last stop left running", "improvement", id)
		}
		src.start(id)
	}
}

// markInterruptedImprovements sets, on a hosted server's start, every org's
// running improvement runs to interrupted. They are not resumed on their
// own: an org admin presses Resume, since the spend goes on the org's keys.
func markInterruptedImprovements(ctx context.Context, env *hostedEnv) {
	orgs, err := env.db.ListOrgs(ctx)
	if err != nil {
		env.base.logger.Error("could not list orgs to mark interrupted improvement runs", "error", err.Error())
		return
	}
	total := 0
	for _, o := range orgs {
		p, err := env.layout.Paths(o.ID)
		if err != nil {
			continue
		}
		n, err := improve.MarkInterrupted(filepath.Join(p.StoreRoot, improve.DefaultDir))
		total += n
		if err != nil {
			env.base.logger.Warn("some improvement runs could not be marked interrupted", "org", o.ID, "error", err.Error())
		}
	}
	if total > 0 {
		env.base.logger.Info(fmt.Sprintf("improvement runs marked interrupted: %d", total))
	}
}

// ImprovementEvent is the data of improvement.finished and
// improvement.paused (docs/notifications.md).
type ImprovementEvent struct {
	ImprovementID string  `json:"improvement_id"`
	Name          string  `json:"name"`
	Agent         string  `json:"agent"`
	Suite         string  `json:"suite"`
	Status        string  `json:"status"`
	State         string  `json:"state,omitempty"`
	Headline      string  `json:"headline,omitempty"`
	GainPoints    float64 `json:"gain_points"`
	SpentUSD      float64 `json:"spent_usd"`
	URL           string  `json:"url"`
}

// improvementEvent is the notification of kind for rec.
func improvementEvent(org, kind string, rec *improve.Record, link string) webhooks.Event {
	data := ImprovementEvent{ImprovementID: rec.ID, Name: rec.Name, Agent: rec.Spec.Agent, Suite: rec.Spec.Suite, Status: rec.Status,
		SpentUSD: rec.SpentUSD, URL: link}
	if v := rec.Verdict; v != nil {
		data.State, data.Headline, data.GainPoints = v.State, v.Headline, 100*v.Gain
	}
	ev := webhooks.NewEvent(kind, org, data)
	sum := &webhooks.Summary{Link: data.URL, Severity: webhooks.SeverityInfo}
	switch kind {
	case webhooks.EventImprovementPaused:
		sum.Title = fmt.Sprintf("Improvement run %s is paused for your approval", rec.Name)
		sum.Text = rec.Reason
		sum.Severity, sum.DedupKey = webhooks.SeverityWarning, "improvement-paused-"+rec.ID
	default:
		sum.Title = fmt.Sprintf("Improvement run %s ended: %s", rec.Name, rec.Status)
		sum.Text = data.Headline
		if sum.Text == "" {
			sum.Text = rec.Reason
		}
	}
	ev.Summary = sum
	return ev
}

// improvementNotifier is the runner's OnEvent for an app: it sends
// improvement.finished (done, stopped or failed) and improvement.paused
// through the org's webhook outbox, like every other event.
func improvementNotifier(a *app) func(kind string, rec *improve.Record) {
	org := firstNonEmptyStr(a.orgID, auth.LocalOrgID)
	hosted := a.orgID != ""
	return func(kind string, rec *improve.Record) {
		if rec == nil || a.cfg == nil || a.cfg.Root == "" {
			return
		}
		if kind == webhooks.EventImprovementFinished && rec.Status != improve.StatusDone && rec.Status != improve.StatusStopped && rec.Status != improve.StatusFailed {
			return
		}
		ev := improvementEvent(org, kind, rec, notify.Default().Absolute("/improvements/"+rec.ID))
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		emitOrgEvent(ctx, org, a.cfg.Root, hosted, ev)
	}
}
