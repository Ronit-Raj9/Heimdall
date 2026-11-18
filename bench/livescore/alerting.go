package livescore

import (
	"context"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/bench/alerts"
)

// LinkPath is the Live page of the bench UI.
const LinkPath = "/live"

// checkAlerts evaluates every threshold over its window and the
// comparison, and sends an alert when a rule opens: a threshold crossed, or
// the comparison decided worse. An open rule is not sent again until it
// closes, so a pull every few minutes does not repeat the same alert.
func (s *Scorer) checkAlerts(ctx context.Context, state *State, all []SessionRecord, now time.Time, res *PullResult) []string {
	if state.Alerts == nil {
		state.Alerts = map[string]AlertState{}
	}
	cfgs := append(append([]alerts.Config(nil), s.Config.Alerts...), s.ExtraAlerts...)
	var sent []string
	send := func(event, subject, message string, changes []string) {
		if s.Notifier == nil || len(cfgs) == 0 {
			return
		}
		p := alerts.Payload{Event: event, Link: LinkPath, Message: message, Changes: changes, SentAt: now}
		if err := s.Notifier.Send(ctx, cfgs, p); err != nil {
			res.Warnings = append(res.Warnings, "alert "+event+": "+oneLine(err.Error(), 200))
			return
		}
		sent = append(sent, event+": "+subject)
		_ = s.Store.Log(LogEvent{At: now, Event: "alert", Detail: event + ": " + message})
	}
	for i, th := range s.Config.Thresholds {
		def, _ := signalByName(th.Signal)
		window := time.Duration(th.Window)
		if window <= 0 {
			window = DefaultWindow
		}
		minN := th.MinSessions
		if minN <= 0 {
			minN = 20
		}
		for _, v := range s.Config.Versions {
			if th.Version != "" && th.Version != v.Name {
				continue
			}
			key := fmt.Sprintf("threshold:%d:%s:%s", i, th.Signal, v.Name)
			mean, n := windowMean(all, v.Name, def, now.Add(-window), now)
			prev := state.Alerts[key]
			crossed := false
			if n >= minN {
				switch {
				case th.Above != nil:
					crossed = mean > *th.Above
				case th.Below != nil:
					crossed = mean < *th.Below
				}
			}
			switch {
			case crossed && !prev.Open:
				state.Alerts[key] = AlertState{Open: true, Since: now, Value: mean}
				level, side := 0.0, "above"
				if th.Above != nil {
					level = *th.Above
				} else {
					level, side = *th.Below, "below"
				}
				msg := fmt.Sprintf("live %s of version %s is %s over the last %s (%d sessions), %s the threshold %s (live evidence: no ground truth)",
					def.Name, v.Name, formatValue(def, mean), window, n, side, formatValue(def, level))
				send(EventThreshold, v.Name+" "+def.Name, msg, []string{fmt.Sprintf("%s %s: %s over %d sessions", v.Name, def.Name, formatValue(def, mean), n)})
			case !crossed && prev.Open && n >= minN:
				state.Alerts[key] = AlertState{Open: false, Since: now, Value: mean}
			}
		}
	}
	if st := state.Compare; st != nil && st.Result == ResultWorse {
		key := "compare:" + st.Baseline + ":" + st.Candidate + ":" + st.StartedAt.Format(time.RFC3339)
		if !state.Alerts[key].Open {
			state.Alerts[key] = AlertState{Open: true, Since: now}
			view := st.View(s.Config)
			var changes []string
			for _, sr := range view.Signals {
				if sr.Decision == ResultWorse {
					changes = append(changes, fmt.Sprintf("%s: baseline %s, candidate %s, %d pairs, e-value %.1f over threshold %.0f",
						sr.Name, fmtMean(sr.MeanA, sr), fmtMean(sr.MeanB, sr), sr.Pairs, sr.EWorse, view.Threshold))
				}
			}
			send(EventWorse, st.Candidate+" vs "+st.Baseline, "live comparison: "+view.summaryLine()+" (live evidence: no ground truth, no control of inputs)", changes)
		}
	}
	return sent
}

// windowMean is a signal's mean over one version's sessions that ended in
// [from, to], and how many sessions had it known.
func windowMean(all []SessionRecord, version string, def SignalDef, from, to time.Time) (float64, int) {
	sum, n := 0.0, 0
	for i := range all {
		r := &all[i]
		if r.Version != version || r.Ended.Before(from) || r.Ended.After(to) {
			continue
		}
		if v, ok := def.value(&r.Signals); ok {
			sum += v
			n++
		}
	}
	if n == 0 {
		return 0, 0
	}
	return sum / float64(n), n
}

func formatValue(def SignalDef, v float64) string {
	if def.Kind == KindBounded {
		return fmt.Sprintf("%.1f%%", v*100)
	}
	switch def.Unit {
	case "USD":
		return fmt.Sprintf("$%.4f", v)
	case "":
		return fmt.Sprintf("%.2f", v)
	}
	return fmt.Sprintf("%.0f %s", v, def.Unit)
}

func fmtMean(m *float64, sr SignalResult) string {
	if m == nil {
		return "unknown"
	}
	def, _ := signalByName(sr.Name)
	return formatValue(def, *m)
}
