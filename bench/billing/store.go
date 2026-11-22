package billing

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

func init() { db.Register("billing", migrations) }

// DayLayout is the layout of a meter day.
const DayLayout = "2006-01-02"

// Account is one org's plan and Stripe link.
type Account struct {
	OrgID          string
	Plan           string
	Status         string // Stripe subscription status; "" when never subscribed
	CustomerID     string
	SubscriptionID string
	Seats          int // paid seats on the subscription
	PeriodEnd      time.Time
	EventAt        time.Time // newest Stripe event applied
	UpdatedBy      string
	UpdatedAt      time.Time
}

// paidStatuses are the subscription statuses that keep a paid plan.
var paidStatuses = map[string]bool{"active": true, "trialing": true, "past_due": true}

// EffectivePlan is the plan the org gets: Enterprise when a server admin set
// it, Team while the subscription is paid (active, trialing or past due),
// else Free.
func (a Account) EffectivePlan() string {
	switch a.Plan {
	case PlanEnterprise:
		return PlanEnterprise
	case PlanTeam:
		if paidStatuses[a.Status] {
			return PlanTeam
		}
	}
	return PlanFree
}

// Store keeps accounts, meters and applied webhook events.
type Store struct {
	DB  *db.DB
	Now func() time.Time
}

// NewStore returns the store over d (migrated: the billing migration set
// is registered by this package).
func NewStore(d *db.DB) *Store { return &Store{DB: d, Now: time.Now} }

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// Account returns the org's account; an org with no row is Free.
func (s *Store) Account(ctx context.Context, orgID string) (Account, error) {
	var a Account
	var periodEnd, eventAt, updatedAt int64
	err := s.DB.QueryRow(ctx, `SELECT org_id, plan, status, customer_id, subscription_id, seats, period_end, event_at, updated_by, updated_at
		FROM billing_accounts WHERE org_id = ?`, orgID).Scan(&a.OrgID, &a.Plan, &a.Status, &a.CustomerID, &a.SubscriptionID,
		&a.Seats, &periodEnd, &eventAt, &a.UpdatedBy, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{OrgID: orgID, Plan: PlanFree}, nil
	}
	if err != nil {
		return Account{}, fmt.Errorf("read billing account: %w", err)
	}
	a.PeriodEnd, a.EventAt, a.UpdatedAt = fromMillis(periodEnd), fromMillis(eventAt), fromMillis(updatedAt)
	return a, nil
}

// AccountByCustomer returns the account linked to a Stripe customer.
func (s *Store) AccountByCustomer(ctx context.Context, customerID string) (Account, bool, error) {
	if strings.TrimSpace(customerID) == "" {
		return Account{}, false, nil
	}
	var org string
	err := s.DB.QueryRow(ctx, `SELECT org_id FROM billing_accounts WHERE customer_id = ?`, customerID).Scan(&org)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, false, nil
	}
	if err != nil {
		return Account{}, false, fmt.Errorf("find billing account: %w", err)
	}
	a, err := s.Account(ctx, org)
	return a, err == nil, err
}

// PutAccount writes a. Plan must be a known plan.
func (s *Store) PutAccount(ctx context.Context, a Account) error {
	if strings.TrimSpace(a.OrgID) == "" {
		return errors.New("billing account: no organization")
	}
	if !ValidPlan(a.Plan) {
		return fmt.Errorf("billing account: unknown plan %q", a.Plan)
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO billing_accounts (org_id, plan, status, customer_id, subscription_id, seats, period_end, event_at, updated_by, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (org_id) DO UPDATE SET plan = excluded.plan, status = excluded.status, customer_id = excluded.customer_id,
			subscription_id = excluded.subscription_id, seats = excluded.seats, period_end = excluded.period_end,
			event_at = excluded.event_at, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		a.OrgID, a.Plan, a.Status, a.CustomerID, a.SubscriptionID, a.Seats, millis(a.PeriodEnd), millis(a.EventAt), a.UpdatedBy, s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("write billing account: %w", err)
	}
	return nil
}

// PaidAccounts lists the accounts with a Stripe subscription (seat sync).
func (s *Store) PaidAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.DB.Query(ctx, `SELECT org_id FROM billing_accounts WHERE subscription_id <> '' ORDER BY org_id`)
	if err != nil {
		return nil, fmt.Errorf("list billing accounts: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Account, 0, len(ids))
	for _, id := range ids {
		a, err := s.Account(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// AddMeter adds q to a counter meter of the org on day (UTC).
func (s *Store) AddMeter(ctx context.Context, orgID string, day time.Time, meter string, q float64) error {
	if q == 0 {
		return nil
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO billing_meters (org_id, day, meter, quantity, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (org_id, day, meter) DO UPDATE SET quantity = billing_meters.quantity + excluded.quantity, updated_at = excluded.updated_at`,
		orgID, day.UTC().Format(DayLayout), meter, q, s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("add meter %s: %w", meter, err)
	}
	return nil
}

// SetMeter sets a gauge meter of the org on day (UTC) to q.
func (s *Store) SetMeter(ctx context.Context, orgID string, day time.Time, meter string, q float64) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO billing_meters (org_id, day, meter, quantity, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (org_id, day, meter) DO UPDATE SET quantity = excluded.quantity, updated_at = excluded.updated_at`,
		orgID, day.UTC().Format(DayLayout), meter, q, s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("set meter %s: %w", meter, err)
	}
	return nil
}

// Meters returns the org's meters from day from to day to (inclusive), as
// day -> meter -> quantity.
func (s *Store) Meters(ctx context.Context, orgID string, from, to time.Time) (map[string]map[string]float64, error) {
	rows, err := s.DB.Query(ctx, `SELECT day, meter, quantity FROM billing_meters WHERE org_id = ? AND day >= ? AND day <= ?`,
		orgID, from.UTC().Format(DayLayout), to.UTC().Format(DayLayout))
	if err != nil {
		return nil, fmt.Errorf("read meters: %w", err)
	}
	defer rows.Close()
	out := map[string]map[string]float64{}
	for rows.Next() {
		var day, meter string
		var q float64
		if err := rows.Scan(&day, &meter, &q); err != nil {
			return nil, err
		}
		if out[day] == nil {
			out[day] = map[string]float64{}
		}
		out[day][meter] = q
	}
	return out, rows.Err()
}

// MeterSum is the sum of a counter meter over [from, to] (inclusive days).
func (s *Store) MeterSum(ctx context.Context, orgID, meter string, from, to time.Time) (float64, error) {
	var q sql.NullFloat64
	err := s.DB.QueryRow(ctx, `SELECT SUM(quantity) FROM billing_meters WHERE org_id = ? AND meter = ? AND day >= ? AND day <= ?`,
		orgID, meter, from.UTC().Format(DayLayout), to.UTC().Format(DayLayout)).Scan(&q)
	if err != nil {
		return 0, fmt.Errorf("sum meter %s: %w", meter, err)
	}
	return q.Float64, nil
}

// ClaimEvent records a webhook event id. It reports false when the event
// was already recorded (a redelivery).
func (s *Store) ClaimEvent(ctx context.Context, id, typ string) (bool, error) {
	res, err := s.DB.Exec(ctx, `INSERT INTO billing_events (id, type, received_at) VALUES (?, ?, ?) ON CONFLICT (id) DO NOTHING`,
		id, typ, s.now().UnixMilli())
	if err != nil {
		return false, fmt.Errorf("record webhook event: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// ForgetEvent removes a recorded event whose handling failed, so Stripe's
// retry is applied.
func (s *Store) ForgetEvent(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM billing_events WHERE id = ?`, id)
	return err
}
