// Package billing holds Heimdall's plans, the metering of what Heimdall
// pays for, and Stripe billing (lane Y16, audit G4-10).
//
//   - Plans are data (Plans): Free, Team and Enterprise, each with a seat
//     limit, a monthly allowance of shared worker minutes and a feature
//     list. Every org brings its own model provider key on every plan.
//   - Meters (meters.go) count shared worker minutes per org and UTC day,
//     fed by the coordinator when a lease on the shared worker ends, and
//     keep a daily storage reading.
//   - Billing is off unless the operator sets HEIMDALL_BILLING=on
//     (config.go). Off, limits are shown but never enforced and checkout is
//     refused; on, the Service refuses what the org's plan does not cover
//     with a LimitError (HTTP 402, type plan_limit).
//   - Stripe is reached over its HTTP API (stripe.go), in test mode unless
//     the operator allows a live key; webhooks are verified with the
//     signing secret (webhook.go).
package billing

// Plan ids.
const (
	PlanFree       = "free"
	PlanTeam       = "team"
	PlanEnterprise = "enterprise"
)

// Feature is something a plan includes.
type Feature string

// Features a plan may include. Free has none of them.
const (
	// FeaturePrivateBenchmarks: create and edit the org's own benchmarks.
	FeaturePrivateBenchmarks Feature = "private_benchmarks"
	// FeatureCI: install the CI gate on a GitHub repository.
	FeatureCI Feature = "ci"
	// FeatureAlerts: scheduled checks with alerts and outgoing webhooks.
	FeatureAlerts Feature = "alerts"
	// FeatureOwnWorker: support for running every evaluation on the org's
	// own worker. Own workers work on every plan; Enterprise adds support.
	FeatureOwnWorker Feature = "own_worker_support"
	// FeatureInvoice: pay by invoice instead of card.
	FeatureInvoice Feature = "invoice_billing"
)

// AllFeatures is every feature, in display order.
var AllFeatures = []Feature{FeaturePrivateBenchmarks, FeatureCI, FeatureAlerts, FeatureOwnWorker, FeatureInvoice}

// Plan is one plan as the pricing page and the Billing tab show it.
type Plan struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
	// PriceUSD is the monthly price per seat; 0 for Free. Contact plans
	// have no price.
	PriceUSD  float64 `json:"price_usd"`
	PriceUnit string  `json:"price_unit"` // "seat_month" | "free" | "contact"
	Contact   bool    `json:"contact"`
	// MaxSeats is how many members the org may have; 0 means no limit.
	MaxSeats int `json:"max_seats"`
	// SharedWorkerMinutes is the monthly (UTC calendar month) allowance of
	// shared worker time; 0 means no limit.
	SharedWorkerMinutes int       `json:"shared_worker_minutes"`
	Features            []Feature `json:"features"`
	// OwnKey: runs use the org's own model provider key (always true).
	OwnKey bool `json:"own_key"`
}

// Has reports whether the plan includes f.
func (p Plan) Has(f Feature) bool {
	for _, x := range p.Features {
		if x == f {
			return true
		}
	}
	return false
}

// TeamSeatUSD is the Team plan's monthly price per seat. The operator's
// Stripe price (HEIMDALL_STRIPE_TEAM_PRICE) must charge the same amount.
const TeamSeatUSD = 39

// Plans are Heimdall's plans in display order. Prices and limits are set
// against the hosted evaluation tools' published tiers (audit G4, section
// 4): free tiers of 1 to 5 seats, paid tiers from $29 to $49 per seat.
var Plans = []Plan{
	{
		ID: PlanFree, Name: "Free", PriceUnit: "free", OwnKey: true,
		Summary:  "Benchmark your agent on the library benchmarks with your own model key.",
		MaxSeats: 3, SharedWorkerMinutes: 300, Features: []Feature{},
	},
	{
		ID: PlanTeam, Name: "Team", PriceUSD: TeamSeatUSD, PriceUnit: "seat_month", OwnKey: true,
		Summary:  "Private benchmarks, the CI gate and alerts for a team that ships an agent.",
		MaxSeats: 50, SharedWorkerMinutes: 3000,
		Features: []Feature{FeaturePrivateBenchmarks, FeatureCI, FeatureAlerts},
	},
	{
		ID: PlanEnterprise, Name: "Enterprise", PriceUnit: "contact", Contact: true, OwnKey: true,
		Summary:  "Custom limits, your own worker with support, and invoice billing.",
		Features: []Feature{FeaturePrivateBenchmarks, FeatureCI, FeatureAlerts, FeatureOwnWorker, FeatureInvoice},
	},
}

// PlanByID returns the plan with id, or Free for an unknown id.
func PlanByID(id string) Plan {
	for _, p := range Plans {
		if p.ID == id {
			return p
		}
	}
	return Plans[0]
}

// ValidPlan reports whether id names a plan.
func ValidPlan(id string) bool {
	for _, p := range Plans {
		if p.ID == id {
			return true
		}
	}
	return false
}
