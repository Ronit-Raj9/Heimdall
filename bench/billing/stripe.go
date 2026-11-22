package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Stripe is a small client of Stripe's HTTP API (form-encoded requests,
// JSON answers). Only the calls billing needs are here; no Stripe SDK.
type Stripe struct {
	Key  string
	Base string // DefaultAPIBase unless a test points it elsewhere
	HTTP *http.Client
}

// NewStripe returns a client for c's key and address.
func NewStripe(c Config) *Stripe {
	return &Stripe{Key: c.SecretKey, Base: c.APIBase, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// StripeError is an error answer of the Stripe API.
type StripeError struct {
	Status  int
	Type    string
	Code    string
	Message string
}

func (e *StripeError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("stripe: %s (HTTP %d)", msg, e.Status)
}

// maxStripeBody caps an answer read from Stripe.
const maxStripeBody = 4 << 20

func (s *Stripe) do(ctx context.Context, method, path string, form url.Values, idem string, out any) error {
	if s == nil || s.Key == "" {
		return ErrNoStripe
	}
	base := s.Base
	if base == "" {
		base = DefaultAPIBase
	}
	u := base + path
	var body io.Reader
	if method == http.MethodGet {
		if len(form) > 0 {
			u += "?" + form.Encode()
		}
	} else {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.Key)
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if idem != "" {
			req.Header.Set("Idempotency-Key", idem)
		}
	}
	client := s.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("stripe: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxStripeBody))
	if err != nil {
		return fmt.Errorf("stripe: read answer: %w", err)
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Type    string `json:"type"`
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return &StripeError{Status: resp.StatusCode, Type: e.Error.Type, Code: e.Error.Code, Message: e.Error.Message}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("stripe: decode answer of %s: %w", path, err)
	}
	return nil
}

// CreateCustomer creates the Stripe customer of an org.
func (s *Stripe) CreateCustomer(ctx context.Context, orgID, name, email string) (string, error) {
	f := url.Values{}
	f.Set("name", name)
	if email != "" {
		f.Set("email", email)
	}
	f.Set("metadata[org_id]", orgID)
	var out struct {
		ID string `json:"id"`
	}
	if err := s.do(ctx, http.MethodPost, "/v1/customers", f, "heimdall-customer-"+orgID, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("stripe: the new customer has no id")
	}
	return out.ID, nil
}

// CheckoutParams start a Team subscription checkout.
type CheckoutParams struct {
	OrgID, Customer, Price string
	Seats                  int
	SuccessURL, CancelURL  string
}

// CreateCheckout opens a Checkout session in subscription mode and returns
// its URL.
func (s *Stripe) CreateCheckout(ctx context.Context, p CheckoutParams) (string, error) {
	f := url.Values{}
	f.Set("mode", "subscription")
	f.Set("customer", p.Customer)
	f.Set("client_reference_id", p.OrgID)
	f.Set("line_items[0][price]", p.Price)
	f.Set("line_items[0][quantity]", strconv.Itoa(max(p.Seats, 1)))
	f.Set("success_url", p.SuccessURL)
	f.Set("cancel_url", p.CancelURL)
	f.Set("metadata[org_id]", p.OrgID)
	f.Set("subscription_data[metadata][org_id]", p.OrgID)
	var out struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := s.do(ctx, http.MethodPost, "/v1/checkout/sessions", f, "", &out); err != nil {
		return "", err
	}
	if out.URL == "" {
		return "", errors.New("stripe: the checkout session has no URL")
	}
	return out.URL, nil
}

// CreatePortal opens a customer portal session and returns its URL.
func (s *Stripe) CreatePortal(ctx context.Context, customer, returnURL string) (string, error) {
	f := url.Values{}
	f.Set("customer", customer)
	f.Set("return_url", returnURL)
	var out struct {
		URL string `json:"url"`
	}
	if err := s.do(ctx, http.MethodPost, "/v1/billing_portal/sessions", f, "", &out); err != nil {
		return "", err
	}
	if out.URL == "" {
		return "", errors.New("stripe: the portal session has no URL")
	}
	return out.URL, nil
}

// Invoice is one invoice as the Billing tab lists it.
type Invoice struct {
	ID         string    `json:"id"`
	Number     string    `json:"number"`
	Status     string    `json:"status"`
	AmountDue  float64   `json:"amount_due"` // in the currency's major unit
	AmountPaid float64   `json:"amount_paid"`
	Currency   string    `json:"currency"`
	Created    time.Time `json:"created"`
	HostedURL  string    `json:"hosted_url,omitempty"`
	PDFURL     string    `json:"pdf_url,omitempty"`
}

// ListInvoices lists the customer's newest invoices.
func (s *Stripe) ListInvoices(ctx context.Context, customer string, limit int) ([]Invoice, error) {
	f := url.Values{}
	f.Set("customer", customer)
	f.Set("limit", strconv.Itoa(min(max(limit, 1), 100)))
	var out struct {
		Data []struct {
			ID         string `json:"id"`
			Number     string `json:"number"`
			Status     string `json:"status"`
			AmountDue  int64  `json:"amount_due"`
			AmountPaid int64  `json:"amount_paid"`
			Currency   string `json:"currency"`
			Created    int64  `json:"created"`
			Hosted     string `json:"hosted_invoice_url"`
			PDF        string `json:"invoice_pdf"`
		} `json:"data"`
	}
	if err := s.do(ctx, http.MethodGet, "/v1/invoices", f, "", &out); err != nil {
		return nil, err
	}
	inv := make([]Invoice, 0, len(out.Data))
	for _, d := range out.Data {
		inv = append(inv, Invoice{ID: d.ID, Number: d.Number, Status: d.Status, AmountDue: float64(d.AmountDue) / 100,
			AmountPaid: float64(d.AmountPaid) / 100, Currency: d.Currency, Created: time.Unix(d.Created, 0).UTC(),
			HostedURL: d.Hosted, PDFURL: d.PDF})
	}
	return inv, nil
}

// Subscription is the part of a Stripe subscription billing reads.
type Subscription struct {
	ID       string            `json:"id"`
	Status   string            `json:"status"`
	Customer string            `json:"customer"`
	Metadata map[string]string `json:"metadata"`
	// CurrentPeriodEnd is on the subscription in older API versions and on
	// each item in newer ones; PeriodEnd reads either.
	CurrentPeriodEnd int64 `json:"current_period_end"`
	Items            struct {
		Data []struct {
			ID               string `json:"id"`
			Quantity         int    `json:"quantity"`
			CurrentPeriodEnd int64  `json:"current_period_end"`
			Price            struct {
				ID string `json:"id"`
			} `json:"price"`
		} `json:"data"`
	} `json:"items"`
}

// Seats is the quantity of the subscription's first item.
func (s *Subscription) Seats() int {
	if len(s.Items.Data) == 0 {
		return 0
	}
	return s.Items.Data[0].Quantity
}

// PeriodEnd is the end of the current billing period, zero when unknown.
func (s *Subscription) PeriodEnd() time.Time {
	end := s.CurrentPeriodEnd
	if end == 0 && len(s.Items.Data) > 0 {
		end = s.Items.Data[0].CurrentPeriodEnd
	}
	if end == 0 {
		return time.Time{}
	}
	return time.Unix(end, 0).UTC()
}

// GetSubscription reads a subscription.
func (s *Stripe) GetSubscription(ctx context.Context, id string) (*Subscription, error) {
	var out Subscription
	if err := s.do(ctx, http.MethodGet, "/v1/subscriptions/"+url.PathEscape(id), nil, "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetSeats sets the quantity of a subscription item, prorated.
func (s *Stripe) SetSeats(ctx context.Context, itemID string, seats int) error {
	f := url.Values{}
	f.Set("quantity", strconv.Itoa(max(seats, 1)))
	f.Set("proration_behavior", "create_prorations")
	return s.do(ctx, http.MethodPost, "/v1/subscription_items/"+url.PathEscape(itemID), f, "", nil)
}
