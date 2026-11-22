package pii

import (
	"strings"
	"testing"
)

func kinds(ms []Match) map[Kind][]string {
	out := map[Kind][]string{}
	for _, m := range ms {
		out[m.Kind] = append(out[m.Kind], m.Value)
	}
	return out
}

func TestScanFindsEachKind(t *testing.T) {
	text := "Hi Priya Sharma,\nYour order shipped. Reach me at jane.doe@example.co.uk or +1 (415) 555-0132.\n" +
		"Card 4242 4242 4242 4242 was charged. SSN 123-45-6789, NINO AB 12 34 56 C, PAN ABCPE1234F,\n" +
		"Aadhaar 2341 2341 2346, SIN 130 692 544, IBAN GB82 WEST 1234 5698 7654 32.\nRegards,\nTom Baker"
	got := kinds(Scan(text, Policy{Mode: ModeWarn}))
	want := map[Kind][]string{
		KindEmail:      {"jane.doe@example.co.uk"},
		KindPhone:      {"+1 (415) 555-0132"},
		KindCard:       {"4242 4242 4242 4242"},
		KindNationalID: {"123-45-6789", "AB 12 34 56 C", "ABCPE1234F", "2341 2341 2346", "130 692 544"},
		KindBank:       {"GB82 WEST 1234 5698 7654 32"},
		KindName:       {"Priya Sharma", "Tom Baker"},
	}
	for k, vs := range want {
		if strings.Join(got[k], "|") != strings.Join(vs, "|") {
			t.Errorf("%s: got %q, want %q", k, got[k], vs)
		}
	}
}

func TestScanSkipsLookalikes(t *testing.T) {
	text := "Version 1.2.3 shipped 2026-11-22. Order 1234567812345678 failed (not Luhn). " +
		"SSN 000-12-3456 is invalid. Hi there, hello Team, Dear Customer. Aadhaar 2341 2341 2345 is wrong."
	if ms := Scan(text, Policy{}); len(ms) != 0 {
		t.Fatalf("lookalikes flagged: %+v", ms)
	}
}

func TestScanOffAndKinds(t *testing.T) {
	text := "mail a@b.io, call +44 20 7946 0958"
	if ms := Scan(text, Policy{Mode: ModeOff}); ms != nil {
		t.Fatalf("off scanned: %+v", ms)
	}
	ms := Scan(text, Policy{Mode: ModeWarn, Kinds: []Kind{KindPhone}})
	if len(ms) != 1 || ms[0].Kind != KindPhone {
		t.Fatalf("kinds filter: %+v", ms)
	}
}

func TestMaskKeepsShape(t *testing.T) {
	r := Redactor{Policy: Policy{Mode: ModeMask}}
	got := r.Apply("Dear Ann Lee, card 4242-4242-4242-4242, mail ann@example.com, phone +1 415 555 0132").Text
	want := "Dear A. L., card ****-****-****-4242, mail a***@example.com, phone +* *** *** **32"
	if got != want {
		t.Fatalf("mask:\n got %q\nwant %q", got, want)
	}
}

func TestRedactIsStable(t *testing.T) {
	r := Redactor{Policy: Policy{Mode: ModeRedact}, Key: []byte("k1")}
	a := r.Apply("from ann@example.com").Text
	b := r.Apply("cc ANN@example.com please").Text
	ta := strings.TrimPrefix(a, "from ")
	if !strings.HasPrefix(ta, "[EMAIL_") || !strings.Contains(b, ta) {
		t.Fatalf("tokens differ: %q %q", a, b)
	}
	other := Redactor{Policy: Policy{Mode: ModeRedact}, Key: []byte("k2")}.Apply("from ann@example.com").Text
	if other == a {
		t.Fatal("a different key gave the same token")
	}
	if strings.Contains(a, "ann") {
		t.Fatalf("value kept: %q", a)
	}
}

func TestWarnAndBlockKeepText(t *testing.T) {
	for _, m := range []Mode{ModeWarn, ModeBlock} {
		res := Redactor{Policy: Policy{Mode: m}}.Apply("ann@example.com")
		if res.Text != "ann@example.com" || len(res.Matches) != 1 {
			t.Fatalf("%s: %+v", m, res)
		}
	}
}

func TestPolicyValidate(t *testing.T) {
	if err := (Policy{Mode: "scrub"}).Validate(); err == nil {
		t.Fatal("bad mode accepted")
	}
	if err := (Policy{Mode: ModeMask, Kinds: []Kind{"dna"}}).Validate(); err == nil {
		t.Fatal("bad kind accepted")
	}
	if err := (Policy{Mode: ModeRedact, Kinds: []Kind{KindEmail}}).Validate(); err != nil {
		t.Fatal(err)
	}
	if (Policy{}).Effective() != ModeWarn {
		t.Fatal("empty policy should warn")
	}
}

func TestSummary(t *testing.T) {
	s := Summary{}
	s.Add(Scan("a@b.io c@d.io +1 415 555 0132", Policy{}))
	if s.Total() != 3 || s.String() != "2 emails, 1 phone number" {
		t.Fatalf("summary %v %q", s, s.String())
	}
}
