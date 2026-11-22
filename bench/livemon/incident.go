package livemon

import (
	"fmt"
	"math"
	"time"
)

// Incident kinds.
const (
	KindQuality = "quality" // the pass rate dropped
	KindLatency = "latency" // the agent got slower
	KindInfra   = "infra"   // the endpoint or the infrastructure failed, not the agent's answers
)

// Incident states.
const (
	StateOpen     = "open"
	StateResolved = "resolved"
)

// Notice events, one alert thread per incident.
const (
	NoticeOpened   = "opened"
	NoticeUpdated  = "updated"
	NoticeResolved = "resolved"
)

// Caps on what one incident keeps.
const (
	maxFailingRuns   = 20
	maxEvaluations   = 20
	infraRemindEvery = 6   // an infra incident still open after this many more checks sends an update
	worseBy          = 0.2 // a quality update goes out when the pass rate falls 20 more points
)

// Window is the numbers of a run of checks.
type Window struct {
	Checks   int       `json:"checks"`
	Passed   int       `json:"passed"`
	Scored   int       `json:"scored"`
	PassRate *float64  `json:"pass_rate,omitempty"`
	P50MS    *float64  `json:"p50_ms,omitempty"`
	From     time.Time `json:"from,omitzero"`
	To       time.Time `json:"to,omitzero"`
}

// RunRef is a run an incident links to.
type RunRef struct {
	RunID        string    `json:"run_id"`
	EvaluationID string    `json:"evaluation_id,omitempty"`
	Task         string    `json:"task"`
	Outcome      string    `json:"outcome"`
	Error        string    `json:"error,omitempty"`
	At           time.Time `json:"at"`
}

// NoticeRecord is one alert sent about an incident.
type NoticeRecord struct {
	Event string    `json:"event"`
	At    time.Time `json:"at"`
}

// Incident is one problem a monitor found, from open to resolved.
type Incident struct {
	ID          string         `json:"id"`
	Monitor     string         `json:"monitor"`
	Kind        string         `json:"kind"`
	State       string         `json:"state"`
	Title       string         `json:"title"`
	Detail      string         `json:"detail"`
	OpenedAt    time.Time      `json:"opened_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	ResolvedAt  *time.Time     `json:"resolved_at,omitempty"`
	Baseline    *Window        `json:"baseline,omitempty"` // frozen when the incident opened
	Current     *Window        `json:"current,omitempty"`  // the latest window
	PValue      *float64       `json:"p_value,omitempty"`
	Checks      int            `json:"checks"` // checks since it opened, the opening ones included
	FailingRuns []RunRef       `json:"failing_runs"`
	Evaluations []string       `json:"evaluations"`
	Notices     []NoticeRecord `json:"notices"`
	// OpenedOn is the time of the newest check that opened the incident;
	// only later checks can resolve it.
	OpenedOn time.Time `json:"opened_on"`
	// Floor is true for a quality incident opened by the min_pass_rate floor.
	Floor bool `json:"floor,omitempty"`
	// NotifiedValue is the pass rate (quality) or median latency (latency)
	// last sent, so an update goes out only when it gets materially worse.
	NotifiedValue *float64 `json:"notified_value,omitempty"`
	// Cause is the infra cause last sent: unreachable, infra_runs or cannot_run.
	Cause string `json:"cause,omitempty"`
}

// Notice is one alert to send.
type Notice struct {
	Event    string   `json:"event"` // opened | updated | resolved
	Incident Incident `json:"incident"`
}

// Step runs the incident rules after the newest sample (the last of
// samples) was added. It returns the samples (the newest marked with the
// open quality or latency incident), the incidents and the notices to send.
// Nothing is changed in place.
func Step(m Monitor, samples []Sample, incidents []Incident, now time.Time) ([]Sample, []Incident, []Notice) {
	if len(samples) == 0 {
		return samples, incidents, nil
	}
	e := &engine{m: m, r: m.Rule.Effective(), now: now,
		samples: append([]Sample(nil), samples...), incidents: append([]Incident(nil), incidents...)}
	e.infra()
	if e.latest().Status == SampleOK {
		e.quality()
		if e.r.LatencyFactor > 0 {
			e.latency()
		}
	}
	return e.samples, e.incidents, e.notices
}

type engine struct {
	m         Monitor
	r         Rule
	now       time.Time
	samples   []Sample
	incidents []Incident
	notices   []Notice
}

func (e *engine) latest() *Sample { return &e.samples[len(e.samples)-1] }

func (e *engine) open(kind string) *Incident {
	for i := range e.incidents {
		if e.incidents[i].Kind == kind && e.incidents[i].State == StateOpen {
			return &e.incidents[i]
		}
	}
	return nil
}

func (e *engine) notify(inc *Incident, event string) {
	inc.UpdatedAt = e.now
	inc.Notices = append(inc.Notices, NoticeRecord{Event: event, At: e.now})
	e.notices = append(e.notices, Notice{Event: event, Incident: *inc})
}

func (e *engine) start(kind, title, detail string, on time.Time) *Incident {
	e.incidents = append(e.incidents, Incident{ID: "inc_" + RandHex(6), Monitor: e.m.ID, Kind: kind, State: StateOpen,
		Title: title, Detail: detail, OpenedAt: e.now, UpdatedAt: e.now, OpenedOn: on,
		FailingRuns: []RunRef{}, Evaluations: []string{}, Notices: []NoticeRecord{}})
	return &e.incidents[len(e.incidents)-1]
}

func (e *engine) resolve(inc *Incident, detail string) {
	t := e.now
	inc.State, inc.ResolvedAt, inc.Detail = StateResolved, &t, detail
	e.notify(inc, NoticeResolved)
}

// attach adds the failing runs and the evaluation of s to inc.
func attach(inc *Incident, s Sample, failing func(RunSample) bool) {
	if s.EvaluationID != "" && len(inc.Evaluations) < maxEvaluations {
		inc.Evaluations = append(inc.Evaluations, s.EvaluationID)
	}
	var refs []RunRef
	for _, r := range s.Runs {
		if failing(r) && r.RunID != "" {
			refs = append(refs, RunRef{RunID: r.RunID, EvaluationID: s.EvaluationID, Task: r.Task, Outcome: r.Outcome, Error: r.Error, At: s.At})
		}
	}
	inc.FailingRuns = append(refs, inc.FailingRuns...) // newest first
	if len(inc.FailingRuns) > maxFailingRuns {
		inc.FailingRuns = inc.FailingRuns[:maxFailingRuns]
	}
}

func failedRun(r RunSample) bool { return r.Pass != nil && !*r.Pass }
func infraRun(r RunSample) bool  { return r.Infra || r.Pass == nil }

// ---- infra ----

func (e *engine) infra() {
	var streak []Sample
	for i := len(e.samples) - 1; i >= 0; i-- {
		s := e.samples[i]
		if s.Status == SampleSkipped {
			continue
		}
		if s.Status != SampleInfra && s.Status != SampleError {
			break
		}
		streak = append(streak, s)
	}
	latest := *e.latest()
	inc := e.open(KindInfra)
	if inc == nil {
		if len(streak) < e.r.OutageAfter {
			return
		}
		cause, title := infraCause(streak[0])
		inc = e.start(KindInfra, title, infraDetail(streak), streak[0].At)
		inc.Cause = cause
		for i := len(streak) - 1; i >= 0; i-- {
			attach(inc, streak[i], infraRun)
		}
		inc.Checks = len(streak)
		e.notify(inc, NoticeOpened)
		return
	}
	switch latest.Status {
	case SampleOK:
		e.resolve(inc, fmt.Sprintf("The check at %s ran normally again after %d failed checks.", latest.At.UTC().Format(time.RFC3339), inc.Checks))
	case SampleInfra, SampleError:
		inc.Checks++
		attach(inc, latest, infraRun)
		cause, title := infraCause(latest)
		inc.Detail = infraDetail(streak)
		if cause != inc.Cause {
			inc.Cause, inc.Title = cause, title
			e.notify(inc, NoticeUpdated)
		} else if (inc.Checks-e.r.OutageAfter)%infraRemindEvery == 0 {
			e.notify(inc, NoticeUpdated)
		} else {
			inc.UpdatedAt = e.now
		}
	}
}

// infraCause names why a failed check failed.
func infraCause(s Sample) (cause, title string) {
	switch {
	case s.Reachable != nil && !*s.Reachable:
		return "unreachable", "Agent endpoint is not answering"
	case s.Status == SampleError:
		return "cannot_run", "Checks cannot run"
	}
	return "infra_runs", "Checks fail for infrastructure reasons"
}

func infraDetail(streak []Sample) string {
	last := streak[0]
	why := last.Reason
	if why == "" {
		for _, r := range last.Runs {
			if r.Error != "" {
				why = r.Error
				break
			}
		}
	}
	d := fmt.Sprintf("The last %d checks in a row failed before the agent's answers could be graded, so this is not counted as a quality drop.", len(streak))
	if why != "" {
		d += " Latest error: " + clip(why, 300)
	}
	return d
}

// ---- quality ----

func (e *engine) okSamples() []int {
	var idx []int
	for i, s := range e.samples {
		if s.Status == SampleOK {
			idx = append(idx, i)
		}
	}
	return idx
}

func (e *engine) window(idx []int) Window {
	w := Window{Checks: len(idx)}
	var lat []float64
	for n, i := range idx {
		s := e.samples[i]
		w.Passed += s.Passed
		w.Scored += s.Scored
		if n == 0 {
			w.From = s.At
		}
		w.To = s.At
		for _, r := range s.Runs {
			if r.LatencyMS != nil && !r.Infra {
				lat = append(lat, float64(*r.LatencyMS))
			}
		}
	}
	if w.Scored > 0 {
		v := float64(w.Passed) / float64(w.Scored)
		w.PassRate = &v
	}
	if len(lat) > 0 {
		v := Median(lat)
		w.P50MS = &v
	}
	return w
}

// baseline is the up to Baseline ok checks before recent that ran with no
// incident open.
func (e *engine) baseline(oks []int, recentStart int) []int {
	var out []int
	for k := recentStart - 1; k >= 0 && len(out) < e.r.Baseline; k-- {
		if e.samples[oks[k]].Incident == "" {
			out = append([]int{oks[k]}, out...)
		}
	}
	return out
}

func (e *engine) quality() {
	oks := e.okSamples()
	start := max(0, len(oks)-e.r.Window)
	recent := oks[start:]
	if inc := e.open(KindQuality); inc != nil {
		e.latest().Incident = inc.ID
		inc.Checks++
		cur := e.window(e.marked(oks, inc.ID))
		inc.Current = &cur
		attach(inc, *e.latest(), failedRun)
		e.qualityOpen(inc, oks)
		return
	}
	cur := e.window(recent)
	if cur.Scored < e.r.MinRecentRuns {
		return
	}
	rate := *cur.PassRate
	sustain := e.r.Window/2 + 1
	base := e.window(e.baseline(oks, start))
	if base.Scored >= MinBaselineRuns && *base.PassRate-rate >= e.r.MinDrop-1e-9 {
		bad := e.below(recent, *base.PassRate-e.r.MinDrop)
		if p := FisherLess(cur.Passed, cur.Scored, base.Passed, base.Scored); p < e.r.Alpha && len(bad) >= sustain {
			title := fmt.Sprintf("Pass rate dropped from %s to %s", pct(*base.PassRate), pct(rate))
			detail := fmt.Sprintf("The last %d checks passed %d of %d runs (%s), against %s over the %d checks before (one-sided Fisher exact p = %s).",
				cur.Checks, cur.Passed, cur.Scored, pct(rate), pct(*base.PassRate), base.Checks, pval(p))
			e.openQuality(bad, &base, p, title, detail, false)
			return
		}
	}
	if f := e.r.MinPassRate; f > 0 && rate < f {
		bad := e.below(recent, f-1e-9)
		if p := BinomialLess(cur.Passed, cur.Scored, f); p < e.r.Alpha && len(bad) >= sustain {
			title := fmt.Sprintf("Pass rate %s is below the %s floor", pct(rate), pct(f))
			detail := fmt.Sprintf("The last %d checks passed %d of %d runs (%s), below the monitor's floor of %s (one-sided binomial p = %s).",
				cur.Checks, cur.Passed, cur.Scored, pct(rate), pct(f), pval(p))
			e.openQuality(bad, nil, p, title, detail, true)
		}
	}
}

// below keeps the checks of idx whose own pass rate is at most limit: a
// drop must show in most checks of the window, not in one.
func (e *engine) below(idx []int, limit float64) []int {
	var out []int
	for _, i := range idx {
		if r := e.samples[i].PassRate(); r != nil && *r <= limit+1e-9 {
			out = append(out, i)
		}
	}
	return out
}

// marked is the newest Window ok checks taken while incident id was open.
func (e *engine) marked(oks []int, id string) []int {
	var out []int
	for _, i := range oks {
		if e.samples[i].Incident == id {
			out = append(out, i)
		}
	}
	return out[max(0, len(out)-e.r.Window):]
}

func (e *engine) openQuality(bad []int, base *Window, p float64, title, detail string, floor bool) {
	inc := e.start(KindQuality, title, detail, e.latest().At)
	cur := e.window(bad)
	inc.Baseline, inc.Current, inc.PValue, inc.Floor, inc.Checks = base, &cur, &p, floor, len(bad)
	for _, i := range bad {
		e.samples[i].Incident = inc.ID
		attach(inc, e.samples[i], failedRun)
	}
	v := *cur.PassRate
	inc.NotifiedValue = &v
	e.notify(inc, NoticeOpened)
}

// qualityOpen resolves an open quality incident once the checks after it
// opened recover, or sends an update when it got materially worse.
func (e *engine) qualityOpen(inc *Incident, oks []int) {
	var post []int
	for _, i := range oks {
		if e.samples[i].At.After(inc.OpenedOn) {
			post = append(post, i)
		}
	}
	need := min(2, e.r.Window)
	if len(post) >= need {
		w := e.window(post[max(0, len(post)-e.r.Window):])
		if w.PassRate != nil && w.Scored >= min(e.r.MinRecentRuns, 3) {
			ok := true
			if inc.Baseline != nil && inc.Baseline.PassRate != nil {
				ok = *w.PassRate >= *inc.Baseline.PassRate-e.r.MinDrop/2-1e-9
			}
			if e.r.MinPassRate > 0 {
				ok = ok && *w.PassRate >= e.r.MinPassRate-1e-9
			}
			if ok {
				e.resolve(inc, fmt.Sprintf("Recovered: the last %d checks passed %d of %d runs (%s).", w.Checks, w.Passed, w.Scored, pct(*w.PassRate)))
				return
			}
		}
	}
	if inc.Current != nil && inc.Current.PassRate != nil && inc.NotifiedValue != nil && *inc.Current.PassRate <= *inc.NotifiedValue-worseBy+1e-9 {
		v := *inc.Current.PassRate
		inc.NotifiedValue = &v
		inc.Title = fmt.Sprintf("Pass rate down to %s", pct(v))
		e.notify(inc, NoticeUpdated)
		return
	}
	inc.UpdatedAt = e.now
}

// ---- latency ----

func (e *engine) latencies(idx []int) []float64 {
	var out []float64
	for _, i := range idx {
		for _, r := range e.samples[i].Runs {
			if r.LatencyMS != nil && !r.Infra {
				out = append(out, float64(*r.LatencyMS))
			}
		}
	}
	return out
}

func (e *engine) latency() {
	oks := e.okSamples()
	start := max(0, len(oks)-e.r.Window)
	recent := oks[start:]
	cur := e.window(recent)
	if inc := e.open(KindLatency); inc != nil {
		if e.latest().Incident == "" {
			e.latest().Incident = inc.ID
		}
		inc.Checks++
		inc.Current = &cur
		e.latencyOpen(inc, oks)
		return
	}
	a := e.latencies(recent)
	if len(a) < e.r.MinRecentRuns {
		return
	}
	baseIdx := e.baseline(oks, start)
	b := e.latencies(baseIdx)
	if len(b) < MinBaselineRuns {
		return
	}
	ma, mb := Median(a), Median(b)
	if mb <= 0 || ma < e.r.LatencyFactor*mb {
		return
	}
	p := MannWhitneyGreater(a, b)
	if p >= e.r.Alpha {
		return
	}
	base := e.window(baseIdx)
	inc := e.start(KindLatency, fmt.Sprintf("Median latency up from %s to %s", secs(mb), secs(ma)),
		fmt.Sprintf("Runs in the last %d checks took a median %s, against %s over the %d checks before (one-sided Mann-Whitney p = %s).",
			cur.Checks, secs(ma), secs(mb), base.Checks, pval(p)), e.latest().At)
	inc.Baseline, inc.Current, inc.PValue, inc.Checks = &base, &cur, &p, len(recent)
	for _, i := range recent {
		if e.samples[i].Incident == "" {
			e.samples[i].Incident = inc.ID
		}
	}
	inc.NotifiedValue = &ma
	e.notify(inc, NoticeOpened)
}

func (e *engine) latencyOpen(inc *Incident, oks []int) {
	var post []int
	for _, i := range oks {
		if e.samples[i].At.After(inc.OpenedOn) {
			post = append(post, i)
		}
	}
	if len(post) >= min(2, e.r.Window) && inc.Baseline != nil && inc.Baseline.P50MS != nil {
		a := e.latencies(post[max(0, len(post)-e.r.Window):])
		if len(a) > 0 && Median(a) <= (1+(e.r.LatencyFactor-1)/2)*(*inc.Baseline.P50MS) {
			e.resolve(inc, fmt.Sprintf("Recovered: median latency back to %s.", secs(Median(a))))
			return
		}
	}
	if inc.Current != nil && inc.Current.P50MS != nil && inc.NotifiedValue != nil && *inc.Current.P50MS >= *inc.NotifiedValue*1.5 {
		v := *inc.Current.P50MS
		inc.NotifiedValue = &v
		inc.Title = fmt.Sprintf("Median latency up to %s", secs(v))
		e.notify(inc, NoticeUpdated)
		return
	}
	inc.UpdatedAt = e.now
}

// ---- words ----

func pct(v float64) string { return fmt.Sprintf("%.0f%%", math.Round(v*100)) }

func pval(p float64) string {
	if p < 0.001 {
		return "< 0.001"
	}
	return fmt.Sprintf("%.3f", p)
}

func secs(ms float64) string {
	if ms < 1000 {
		return fmt.Sprintf("%.0f ms", ms)
	}
	return fmt.Sprintf("%.1f s", ms/1000)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
