package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SignatureHeader is the header Stripe signs webhook deliveries with.
const SignatureHeader = "Stripe-Signature"

// SignatureTolerance is how old a signed delivery may be (Stripe's default).
const SignatureTolerance = 5 * time.Minute

// ErrSignature is a webhook delivery whose signature does not verify.
var ErrSignature = errors.New("stripe webhook signature does not verify")

// VerifySignature checks header (t=<unix>,v1=<hex>[,v1=...]) against the
// HMAC-SHA256 of "<t>.<payload>" under secret, and that t is within
// tolerance of now. Comparisons are constant time.
func VerifySignature(payload []byte, header, secret string, now time.Time, tolerance time.Duration) error {
	if secret == "" {
		return fmt.Errorf("%w: no signing secret", ErrSignature)
	}
	var ts int64
	var sigs [][]byte
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fmt.Errorf("%w: bad timestamp", ErrSignature)
			}
			ts = n
		case "v1":
			b, err := hex.DecodeString(v)
			if err == nil {
				sigs = append(sigs, b)
			}
		}
	}
	if ts == 0 || len(sigs) == 0 {
		return fmt.Errorf("%w: no timestamp or v1 signature", ErrSignature)
	}
	if tolerance > 0 {
		d := now.Sub(time.Unix(ts, 0))
		if d < 0 {
			d = -d
		}
		if d > tolerance {
			return fmt.Errorf("%w: timestamp outside the tolerance", ErrSignature)
		}
	}
	want := Sign(payload, secret, ts)
	for _, s := range sigs {
		if hmac.Equal(s, want) {
			return nil
		}
	}
	return ErrSignature
}

// Sign is the v1 signature of payload at unix time ts (tests and the fake
// Stripe use it to sign deliveries).
func Sign(payload []byte, secret string, ts int64) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(ts, 10)))
	m.Write([]byte("."))
	m.Write(payload)
	return m.Sum(nil)
}

// SignatureFor is the Stripe-Signature header value of payload at ts.
func SignatureFor(payload []byte, secret string, ts int64) string {
	return "t=" + strconv.FormatInt(ts, 10) + ",v1=" + hex.EncodeToString(Sign(payload, secret, ts))
}

// Event is a Stripe webhook event.
type Event struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Created int64  `json:"created"`
	Data    struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

// checkoutSession is the part of a checkout.session object billing reads.
type checkoutSession struct {
	ID                string            `json:"id"`
	Mode              string            `json:"mode"`
	ClientReferenceID string            `json:"client_reference_id"`
	Customer          string            `json:"customer"`
	Subscription      string            `json:"subscription"`
	Metadata          map[string]string `json:"metadata"`
}

// ParseEvent decodes a verified delivery.
func ParseEvent(payload []byte) (*Event, error) {
	var e Event
	if err := json.Unmarshal(payload, &e); err != nil {
		return nil, fmt.Errorf("decode stripe event: %w", err)
	}
	if e.ID == "" || e.Type == "" {
		return nil, errors.New("stripe event has no id or type")
	}
	return &e, nil
}
