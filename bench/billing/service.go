package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Meters.
const (
	// MeterSharedWorkerMinutes counts the minutes leases held a slot on the
	// shared worker (Heimdall's machine), per org and day.
	MeterSharedWorkerMinutes = "shared_worker_minutes"
	// MeterStorageBytes is the day's newest reading of the org's stored
	// bytes (run evidence and project files).
	MeterStorageBytes = "storage_bytes"
	// MeterOperatorJudgeCalls counts judge model calls made on the
	// operator's keys. Every judge runs on the org's own key today, so it
	// stays 0; it is metered so a future operator-paid judge is counted.
	MeterOperatorJudgeCalls = "operator_judge_calls"
)

// maxLeaseMinutes caps one lease's metered time, so a clock jump can not
// bill a day of minutes.
const maxLeaseMinutes = 48 * 60

// ErrPlanLimit marks a request the org's plan does not cover while billing
// is on. handlers answer it 402 with type plan_limit.
var ErrPlanLimit = errors.New("plan limit")

// LimitError is a refusal of the org's plan.
type LimitError struct {
	Limit   string `json:"limit"`   // seats | shared_worker_minutes | a Feature
	Plan    string `json:"plan"`    // the org's plan
	Upgrade string `json:"upgrade"` // the plan that covers it
	Msg     string `json:"message"`
}

func (e *LimitError) Error() string { return e.Msg }
func (e *LimitError) Unwrap() error { return ErrPlanLimit }

// OrgInfo is what billing reads about an org from the platform.
type OrgInfo interface {
	OrgName(ctx context.Context, orgID string) (string, error)
	// Seats are the org's members and its open invitations.
	Seats(ctx context.Context, orgID string) (members, invited int, err error)
	// OwnerEmail is an owner's email for the Stripe customer; "" when none.
	OwnerEmail(ctx context.Context, orgID string) (string, error)
}

// UsageSource reads the usage that is derived from an org's data rather
// than metered by events. Any method may return an error; the report then
// says the figure is not available.
type UsageSource interface {
	RunsByDay(ctx context.Context, orgID string, from, to time.Time) (map[string]int, error)
	EvaluationsByDay(ctx context.Context, orgID string, from, to time.Time) (map[string]int, error)
	StorageBytes(ctx context.Context, orgID string) (int64, error)
}

// Service is billing over the store, Stripe and the platform.
type Service struct {
	Cfg    Config
	Store  *Store
	Stripe *Stripe
	Orgs   OrgInfo
	Data   UsageSource // nil: runs, evaluations and storage are not reported
	Now    func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Enforced reports whether limits are enforced (billing is on).
func (s *Service) Enforced() bool { return s != nil && s.Cfg.Enabled }

// monthOf is the first and last day of t's UTC month.
func monthOf(t time.Time) (time.Time, time.Time) {
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	return first, first.AddDate(0, 1, -1)
}

// RecordLease meters a lease that held slots on the shared worker from
// start to end.
func (s *Service) RecordLease(ctx context.Context, orgID string, start, end time.Time, slots int) error {
	if orgID == "" || start.IsZero() || !end.After(start) {
		return nil
	}
	mins := end.Sub(start).Minutes()
	if mins > maxLeaseMinutes {
		mins = maxLeaseMinutes
	}
	return s.Store.AddMeter(ctx, orgID, end, MeterSharedWorkerMinutes, mins*float64(max(slots, 1)))
}

// Plan is the org's effective plan and its account.
func (s *Service) Plan(ctx context.Context, orgID string) (Plan, Account, error) {
	a, err := s.Store.Account(ctx, orgID)
	if err != nil {
		return Plan{}, Account{}, err
	}
	return PlanByID(a.EffectivePlan()), a, nil
}

// SharedMinutesThisMonth is the org's shared worker minutes this UTC month.
func (s *Service) SharedMinutesThisMonth(ctx context.Context, orgID string) (float64, error) {
	from, to := monthOf(s.now())
	return s.Store.MeterSum(ctx, orgID, MeterSharedWorkerMinutes, from, to)
}

// upgradeFor is the plan that lifts a limit of plan p.
func upgradeFor(p Plan) string {
	if p.ID == PlanFree {
		return PlanTeam
	}
	return PlanEnterprise
}

// CheckFeature refuses f when billing is on and the org's plan lacks it.
func (s *Service) CheckFeature(ctx context.Context, orgID string, f Feature) error {
	if !s.Enforced() {
		return nil
	}
	p, _, err := s.Plan(ctx, orgID)
	if err != nil {
		return nil // a billing read failure never blocks work
	}
	if p.Has(f) {
		return nil
	}
	return &LimitError{Limit: string(f), Plan: p.ID, Upgrade: upgradeFor(p),
		Msg: fmt.Sprintf("%s is not part of the %s plan; upgrade to %s in Settings, Billing", featureLabel(f), p.Name, PlanByID(upgradeFor(p)).Name)}
}

// CheckSeats refuses a new invitation when billing is on and the org's
// members plus open invitations already fill the plan's seats.
func (s *Service) CheckSeats(ctx context.Context, orgID string) error {
	if !s.Enforced() || s.Orgs == nil {
		return nil
	}
	p, _, err := s.Plan(ctx, orgID)
	if err != nil || p.MaxSeats == 0 {
		return nil
	}
	members, invited, err := s.Orgs.Seats(ctx, orgID)
	if err != nil {
		return nil
	}
	if members+invited < p.MaxSeats {
		return nil
	}
	return &LimitError{Limit: "seats", Plan: p.ID, Upgrade: upgradeFor(p),
		Msg: fmt.Sprintf("the %s plan has %d seats and %d are taken (%d members, %d open invitations); remove someone or upgrade in Settings, Billing",
			p.Name, p.MaxSeats, members+invited, members, invited)}
}

// CheckSharedMinutes refuses new shared worker work when billing is on and
// the org used its plan's shared worker minutes this month.
func (s *Service) CheckSharedMinutes(ctx context.Context, orgID string) error {
	if !s.Enforced() {
		return nil
	}
	p, _, err := s.Plan(ctx, orgID)
	if err != nil || p.SharedWorkerMinutes == 0 {
		return nil
	}
	used, err := s.SharedMinutesThisMonth(ctx, orgID)
	if err != nil || used < float64(p.SharedWorkerMinutes) {
		return nil
	}
	return &LimitError{Limit: MeterSharedWorkerMinutes, Plan: p.ID, Upgrade: upgradeFor(p),
		Msg: fmt.Sprintf("the organization used %.0f of the %d shared worker minutes the %s plan includes this month (UTC); connect your own worker, wait for next month, or upgrade in Settings, Billing",
			used, p.SharedWorkerMinutes, p.Name)}
}

func featureLabel(f Feature) string {
	switch f {
	case FeaturePrivateBenchmarks:
		return "Private benchmarks"
	case FeatureCI:
		return "The CI gate"
	case FeatureAlerts:
		return "Alerts"
	case FeatureOwnWorker:
		return "Own worker support"
	case FeatureInvoice:
		return "Invoice billing"
	}
	return string(f)
}

// MeterView is one meter against its plan limit.
type MeterView struct {
	ID       string  `json:"id"`
	Used     float64 `json:"used"`
	Limit    float64 `json:"limit"`  // 0: no limit
	Unit     string  `json:"unit"`   // minutes | seats | runs | evaluations | bytes | calls
	Period   string  `json:"period"` // month | now
	Enforced bool    `json:"enforced"`
	Reached  bool    `json:"reached"`
	// Available is false when the figure could not be read.
	Available bool `json:"available"`
}

// FeatureView is one feature of the org's plan.
type FeatureView struct {
	ID       Feature `json:"id"`
	Included bool    `json:"included"`
}

// View is the Billing tab's data.
type View struct {
	Enabled         bool          `json:"enabled"`
	StripeMode      string        `json:"stripe_mode"` // test | live | ""
	CheckoutReady   bool          `json:"checkout_ready"`
	CheckoutBlocker string        `json:"checkout_blocker,omitempty"`
	PortalReady     bool          `json:"portal_ready"`
	Plan            Plan          `json:"plan"`
	Status          string        `json:"status"`
	PaidSeats       int           `json:"paid_seats"`
	PeriodEnd       *time.Time    `json:"period_end,omitempty"`
	PeriodFrom      string        `json:"period_from"`
	PeriodTo        string        `json:"period_to"`
	Meters          []MeterView   `json:"meters"`
	Features        []FeatureView `json:"features"`
	Contact         string        `json:"contact,omitempty"`
	// Plans are every plan, for the comparison under the current one.
	Plans []Plan `json:"plans"`
}

// View builds the Billing tab of an org.
func (s *Service) View(ctx context.Context, orgID string) (*View, error) {
	p, a, err := s.Plan(ctx, orgID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	from, to := monthOf(now)
	v := &View{Enabled: s.Cfg.Enabled, StripeMode: s.Cfg.Mode(), Plan: p, Status: a.Status, PaidSeats: a.Seats,
		PeriodFrom: from.Format(DayLayout), PeriodTo: to.Format(DayLayout), Contact: s.Cfg.Contact,
		Meters: []MeterView{}, Features: []FeatureView{}, Plans: Plans}
	if !a.PeriodEnd.IsZero() {
		pe := a.PeriodEnd
		v.PeriodEnd = &pe
	}
	if err := s.Cfg.StripeReady(); err != nil {
		v.CheckoutBlocker = err.Error()
	} else {
		v.CheckoutReady = true
		v.PortalReady = a.CustomerID != ""
	}
	enforced := s.Enforced()
	mins, err := s.Store.MeterSum(ctx, orgID, MeterSharedWorkerMinutes, from, to)
	v.Meters = append(v.Meters, limitMeter(MeterSharedWorkerMinutes, mins, err == nil, float64(p.SharedWorkerMinutes), "minutes", "month", enforced))
	seats := MeterView{ID: "seats", Unit: "seats", Period: "now", Limit: float64(p.MaxSeats), Enforced: enforced && p.MaxSeats > 0}
	if s.Orgs != nil {
		if m, inv, err := s.Orgs.Seats(ctx, orgID); err == nil {
			seats.Used, seats.Available = float64(m+inv), true
			seats.Reached = seats.Limit > 0 && seats.Used >= seats.Limit
		}
	}
	v.Meters = append(v.Meters, seats)
	if s.Data != nil {
		runs, rerr := s.Data.RunsByDay(ctx, orgID, from, to)
		v.Meters = append(v.Meters, MeterView{ID: "runs", Unit: "runs", Period: "month", Used: float64(sumInts(runs)), Available: rerr == nil})
		evs, eerr := s.Data.EvaluationsByDay(ctx, orgID, from, to)
		v.Meters = append(v.Meters, MeterView{ID: "evaluations", Unit: "evaluations", Period: "month", Used: float64(sumInts(evs)), Available: eerr == nil})
		b, berr := s.storage(ctx, orgID)
		v.Meters = append(v.Meters, MeterView{ID: MeterStorageBytes, Unit: "bytes", Period: "now", Used: float64(b), Available: berr == nil})
	}
	judge, err := s.Store.MeterSum(ctx, orgID, MeterOperatorJudgeCalls, from, to)
	v.Meters = append(v.Meters, MeterView{ID: MeterOperatorJudgeCalls, Unit: "calls", Period: "month", Used: judge, Available: err == nil})
	for _, f := range AllFeatures {
		v.Features = append(v.Features, FeatureView{ID: f, Included: p.Has(f)})
	}
	return v, nil
}

func limitMeter(id string, used float64, ok bool, limit float64, unit, period string, enforced bool) MeterView {
	return MeterView{ID: id, Used: used, Limit: limit, Unit: unit, Period: period, Available: ok,
		Enforced: enforced && limit > 0, Reached: ok && limit > 0 && used >= limit}
}

func sumInts(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// storage reads the org's stored bytes and keeps today's reading.
func (s *Service) storage(ctx context.Context, orgID string) (int64, error) {
	b, err := s.Data.StorageBytes(ctx, orgID)
	if err != nil {
		return 0, err
	}
	_ = s.Store.SetMeter(ctx, orgID, s.now(), MeterStorageBytes, float64(b))
	return b, nil
}

// UsageDay is one day of metered usage.
type UsageDay struct {
	Day                 string   `json:"day"`
	SharedWorkerMinutes float64  `json:"shared_worker_minutes"`
	Runs                int      `json:"runs"`
	Evaluations         int      `json:"evaluations"`
	StorageBytes        *float64 `json:"storage_bytes,omitempty"` // the day's reading, when one was taken
	OperatorJudgeCalls  float64  `json:"operator_judge_calls"`
}

// UsageTotals sums a usage report.
type UsageTotals struct {
	SharedWorkerMinutes float64 `json:"shared_worker_minutes"`
	Runs                int     `json:"runs"`
	Evaluations         int     `json:"evaluations"`
	OperatorJudgeCalls  float64 `json:"operator_judge_calls"`
}

// UsageReport is the org's metered usage per day.
type UsageReport struct {
	From         string      `json:"from"`
	To           string      `json:"to"`
	Days         []UsageDay  `json:"days"`
	Totals       UsageTotals `json:"totals"`
	StorageBytes *int64      `json:"storage_bytes,omitempty"` // now
	Unavailable  []string    `json:"unavailable,omitempty"`   // figures that could not be read
}

// MaxUsageDays bounds a usage report.
const MaxUsageDays = 366

// Usage reports the org's meters for each day of [from, to].
func (s *Service) Usage(ctx context.Context, orgID string, from, to time.Time) (*UsageReport, error) {
	from, to = from.UTC().Truncate(24*time.Hour), to.UTC().Truncate(24*time.Hour)
	if to.Before(from) {
		return nil, errors.New("from must be on or before to")
	}
	if to.Sub(from) > (MaxUsageDays-1)*24*time.Hour {
		return nil, fmt.Errorf("the range can be at most %d days", MaxUsageDays)
	}
	meters, err := s.Store.Meters(ctx, orgID, from, to)
	if err != nil {
		return nil, err
	}
	rep := &UsageReport{From: from.Format(DayLayout), To: to.Format(DayLayout), Days: []UsageDay{}}
	var runs, evs map[string]int
	if s.Data != nil {
		if runs, err = s.Data.RunsByDay(ctx, orgID, from, to); err != nil {
			rep.Unavailable = append(rep.Unavailable, "runs")
		}
		if evs, err = s.Data.EvaluationsByDay(ctx, orgID, from, to); err != nil {
			rep.Unavailable = append(rep.Unavailable, "evaluations")
		}
		if b, err := s.storage(ctx, orgID); err == nil {
			rep.StorageBytes = &b
			if day := s.now().Format(DayLayout); day >= rep.From && day <= rep.To {
				if meters[day] == nil {
					meters[day] = map[string]float64{}
				}
				meters[day][MeterStorageBytes] = float64(b)
			}
		} else {
			rep.Unavailable = append(rep.Unavailable, "storage")
		}
	} else {
		rep.Unavailable = append(rep.Unavailable, "runs", "evaluations", "storage")
	}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		k := d.Format(DayLayout)
		m := meters[k]
		day := UsageDay{Day: k, SharedWorkerMinutes: m[MeterSharedWorkerMinutes], Runs: runs[k], Evaluations: evs[k],
			OperatorJudgeCalls: m[MeterOperatorJudgeCalls]}
		if b, ok := m[MeterStorageBytes]; ok {
			day.StorageBytes = &b
		}
		rep.Days = append(rep.Days, day)
		rep.Totals.SharedWorkerMinutes += day.SharedWorkerMinutes
		rep.Totals.Runs += day.Runs
		rep.Totals.Evaluations += day.Evaluations
		rep.Totals.OperatorJudgeCalls += day.OperatorJudgeCalls
	}
	return rep, nil
}

// ErrAlreadyPaid is a checkout for an org that already pays.
var ErrAlreadyPaid = errors.New("the organization is already on a paid plan; change seats or cancel in the billing portal")

// ErrNoCustomer is a portal or invoices request of an org that never
// subscribed.
var ErrNoCustomer = errors.New("the organization has no billing account yet; upgrade to Team first")

// ensureCustomer returns the org's Stripe customer, creating it.
func (s *Service) ensureCustomer(ctx context.Context, a Account) (Account, error) {
	if a.CustomerID != "" {
		return a, nil
	}
	name := a.OrgID
	email := ""
	if s.Orgs != nil {
		if n, err := s.Orgs.OrgName(ctx, a.OrgID); err == nil && n != "" {
			name = n
		}
		if e, err := s.Orgs.OwnerEmail(ctx, a.OrgID); err == nil {
			email = e
		}
	}
	id, err := s.Stripe.CreateCustomer(ctx, a.OrgID, name, email)
	if err != nil {
		return a, err
	}
	a.CustomerID = id
	if err := s.Store.PutAccount(ctx, a); err != nil {
		return a, err
	}
	return a, nil
}

// Checkout opens a Stripe Checkout session for the Team plan, with one
// seat per current member, and returns its URL. appURL is the app's public
// address (the session returns to Settings, Billing).
func (s *Service) Checkout(ctx context.Context, orgID, actor, appURL string) (string, error) {
	if err := s.Cfg.StripeReady(); err != nil {
		return "", err
	}
	_, a, err := s.Plan(ctx, orgID)
	if err != nil {
		return "", err
	}
	if a.EffectivePlan() != PlanFree {
		return "", ErrAlreadyPaid
	}
	if a.Plan != PlanTeam && a.Plan != PlanFree {
		return "", ErrAlreadyPaid
	}
	a.UpdatedBy = actor
	if a, err = s.ensureCustomer(ctx, a); err != nil {
		return "", err
	}
	seats := 1
	if s.Orgs != nil {
		if m, _, err := s.Orgs.Seats(ctx, orgID); err == nil && m > 0 {
			seats = m
		}
	}
	base := strings.TrimRight(appURL, "/") + "/settings?tab=billing"
	return s.Stripe.CreateCheckout(ctx, CheckoutParams{OrgID: orgID, Customer: a.CustomerID, Price: s.Cfg.TeamPrice, Seats: seats,
		SuccessURL: base + "&checkout=done", CancelURL: base + "&checkout=cancelled"})
}

// Portal opens the Stripe customer portal of the org and returns its URL.
func (s *Service) Portal(ctx context.Context, orgID, appURL string) (string, error) {
	if err := s.Cfg.StripeReady(); err != nil {
		return "", err
	}
	a, err := s.Store.Account(ctx, orgID)
	if err != nil {
		return "", err
	}
	if a.CustomerID == "" {
		return "", ErrNoCustomer
	}
	return s.Stripe.CreatePortal(ctx, a.CustomerID, strings.TrimRight(appURL, "/")+"/settings?tab=billing")
}

// Invoices lists the org's newest invoices; none for an org that never
// subscribed.
func (s *Service) Invoices(ctx context.Context, orgID string) ([]Invoice, error) {
	if err := s.Cfg.StripeReady(); err != nil {
		return nil, err
	}
	a, err := s.Store.Account(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if a.CustomerID == "" {
		return []Invoice{}, nil
	}
	return s.Stripe.ListInvoices(ctx, a.CustomerID, 24)
}

// SeatSync is the result of a seat sync.
type SeatSync struct {
	Members int  `json:"members"`
	Seats   int  `json:"seats"` // paid seats after the sync
	Changed bool `json:"changed"`
}

// SyncSeats sets the subscription's quantity to the org's member count.
func (s *Service) SyncSeats(ctx context.Context, orgID string) (*SeatSync, error) {
	if err := s.Cfg.StripeReady(); err != nil {
		return nil, err
	}
	a, err := s.Store.Account(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if a.SubscriptionID == "" || a.EffectivePlan() != PlanTeam {
		return nil, ErrNoCustomer
	}
	if s.Orgs == nil {
		return nil, errors.New("members are not available")
	}
	members, _, err := s.Orgs.Seats(ctx, orgID)
	if err != nil {
		return nil, err
	}
	sub, err := s.Stripe.GetSubscription(ctx, a.SubscriptionID)
	if err != nil {
		return nil, err
	}
	if len(sub.Items.Data) == 0 {
		return nil, errors.New("stripe: the subscription has no items")
	}
	want := max(members, 1)
	out := &SeatSync{Members: members, Seats: sub.Seats()}
	if sub.Seats() != want {
		if err := s.Stripe.SetSeats(ctx, sub.Items.Data[0].ID, want); err != nil {
			return nil, err
		}
		out.Seats, out.Changed = want, true
	}
	if a.Seats != out.Seats {
		a.Seats = out.Seats
		if err := s.Store.PutAccount(ctx, a); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SyncAllSeats syncs every paid org's seats and returns how many changed.
func (s *Service) SyncAllSeats(ctx context.Context) (int, error) {
	if s.Cfg.StripeReady() != nil {
		return 0, nil
	}
	list, err := s.Store.PaidAccounts(ctx)
	if err != nil {
		return 0, err
	}
	changed := 0
	var errs []error
	for _, a := range list {
		if a.EffectivePlan() != PlanTeam {
			continue
		}
		r, err := s.SyncSeats(ctx, a.OrgID)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.OrgID, err))
			continue
		}
		if r.Changed {
			changed++
		}
	}
	return changed, errors.Join(errs...)
}

// SetPlan sets an org's plan by hand (server admins): Enterprise for a
// signed contract, Free to end it, or Team granted without a subscription.
func (s *Service) SetPlan(ctx context.Context, orgID, plan, actor string) (Account, error) {
	if !ValidPlan(plan) {
		return Account{}, fmt.Errorf("unknown plan %q: use free, team or enterprise", plan)
	}
	a, err := s.Store.Account(ctx, orgID)
	if err != nil {
		return Account{}, err
	}
	a.Plan, a.UpdatedBy = plan, actor
	if plan == PlanTeam && !paidStatuses[a.Status] {
		a.Status = StatusManual
	}
	if plan != PlanTeam && a.Status == StatusManual {
		a.Status = ""
	}
	return a, s.Store.PutAccount(ctx, a)
}

// StatusManual is the status of a plan a server admin set without Stripe.
const StatusManual = "manual"

func init() { paidStatuses[StatusManual] = true }

// WebhookResult says what a delivery did.
type WebhookResult struct {
	Event   string `json:"event"`
	Type    string `json:"type"`
	Applied bool   `json:"applied"`
	Note    string `json:"note,omitempty"`
}

// HandleWebhook verifies and applies one Stripe delivery. A signature
// failure wraps ErrSignature; an apply failure forgets the event so
// Stripe's retry is applied.
func (s *Service) HandleWebhook(ctx context.Context, payload []byte, signature string) (*WebhookResult, error) {
	if err := s.Cfg.WebhookReady(); err != nil {
		return nil, err
	}
	if err := VerifySignature(payload, signature, s.Cfg.WebhookSecret, s.now(), SignatureTolerance); err != nil {
		return nil, err
	}
	ev, err := ParseEvent(payload)
	if err != nil {
		return nil, err
	}
	res := &WebhookResult{Event: ev.ID, Type: ev.Type}
	fresh, err := s.Store.ClaimEvent(ctx, ev.ID, ev.Type)
	if err != nil {
		return nil, err
	}
	if !fresh {
		res.Note = "already applied"
		return res, nil
	}
	applied, note, err := s.apply(ctx, ev)
	if err != nil {
		_ = s.Store.ForgetEvent(context.WithoutCancel(ctx), ev.ID)
		return nil, err
	}
	res.Applied, res.Note = applied, note
	return res, nil
}

func (s *Service) apply(ctx context.Context, ev *Event) (bool, string, error) {
	at := time.Unix(ev.Created, 0).UTC()
	switch ev.Type {
	case "checkout.session.completed":
		var cs checkoutSession
		if err := json.Unmarshal(ev.Data.Object, &cs); err != nil {
			return false, "", fmt.Errorf("decode checkout session: %w", err)
		}
		if cs.Mode != "" && cs.Mode != "subscription" {
			return false, "not a subscription checkout", nil
		}
		org := cs.Metadata["org_id"]
		if org == "" {
			org = cs.ClientReferenceID
		}
		if !s.knownOrg(ctx, org) {
			return false, "no such organization", nil
		}
		a, err := s.Store.Account(ctx, org)
		if err != nil {
			return false, "", err
		}
		a.CustomerID, a.SubscriptionID, a.UpdatedBy = cs.Customer, cs.Subscription, "stripe"
		if a.Plan != PlanEnterprise {
			a.Plan = PlanTeam
		}
		if a.Status == "" || a.Status == StatusManual {
			a.Status = "active"
		}
		if cs.Subscription != "" && s.Stripe != nil && s.Stripe.Key != "" {
			sub, err := s.Stripe.GetSubscription(ctx, cs.Subscription)
			if err != nil {
				return false, "", err
			}
			a.Status, a.Seats, a.PeriodEnd = sub.Status, sub.Seats(), sub.PeriodEnd()
		}
		if at.After(a.EventAt) {
			a.EventAt = at
		}
		return true, "", s.Store.PutAccount(ctx, a)
	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted",
		"customer.subscription.paused", "customer.subscription.resumed":
		var sub Subscription
		if err := json.Unmarshal(ev.Data.Object, &sub); err != nil {
			return false, "", fmt.Errorf("decode subscription: %w", err)
		}
		org := sub.Metadata["org_id"]
		var a Account
		var err error
		if org != "" && s.knownOrg(ctx, org) {
			a, err = s.Store.Account(ctx, org)
		} else {
			var ok bool
			a, ok, err = s.Store.AccountByCustomer(ctx, sub.Customer)
			if err == nil && !ok {
				return false, "no organization for this subscription", nil
			}
		}
		if err != nil {
			return false, "", err
		}
		if a.EventAt.After(at) {
			return false, "older than the state already applied", nil
		}
		if a.SubscriptionID != "" && a.SubscriptionID != sub.ID && paidStatuses[a.Status] && ev.Type == "customer.subscription.deleted" {
			return false, "another subscription is current", nil
		}
		a.CustomerID, a.SubscriptionID, a.Status, a.EventAt, a.UpdatedBy = sub.Customer, sub.ID, sub.Status, at, "stripe"
		if ev.Type == "customer.subscription.deleted" && a.Status != "canceled" {
			a.Status = "canceled"
		}
		a.Seats, a.PeriodEnd = sub.Seats(), sub.PeriodEnd()
		if a.Plan != PlanEnterprise {
			a.Plan = PlanTeam // EffectivePlan is Team only while the status is paid
		}
		return true, "", s.Store.PutAccount(ctx, a)
	}
	return false, "event type not used", nil
}

func (s *Service) knownOrg(ctx context.Context, orgID string) bool {
	if strings.TrimSpace(orgID) == "" {
		return false
	}
	if s.Orgs == nil {
		return true
	}
	_, err := s.Orgs.OrgName(ctx, orgID)
	return err == nil
}

// PublicPricing is the body of GET /api/public/pricing.
type PublicPricing struct {
	Currency       string    `json:"currency"`
	Plans          []Plan    `json:"plans"`
	Features       []Feature `json:"features"`
	BillingEnabled bool      `json:"billing_enabled"`
	Contact        string    `json:"contact,omitempty"`
	Notes          []string  `json:"notes"`
}

// Pricing is the public pricing data.
func (s *Service) Pricing() PublicPricing {
	p := PublicPricing{Currency: "usd", Plans: Plans, Features: AllFeatures, Notes: []string{
		"Every plan runs your agent with your own model provider key; Heimdall never bills model usage.",
		"Shared worker minutes are the time your runs hold a slot on Heimdall's shared worker, per UTC calendar month. Runs on your own worker do not count.",
		"Team is billed per member per month; adding or removing members changes the seat count at the next sync, prorated.",
	}}
	if s != nil {
		p.BillingEnabled, p.Contact = s.Cfg.Enabled, s.Cfg.Contact
	}
	return p
}
