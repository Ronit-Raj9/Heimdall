// Package pii finds personal data in imported text and masks or redacts it
// by policy (lane Y15). It is pattern based: emails, phone numbers, payment
// card numbers (Luhn checked), national ids (US SSN, UK National Insurance,
// India Aadhaar (Verhoeff checked) and PAN, Canada SIN (Luhn checked)), bank
// account numbers (IBAN, mod-97 checked) and personal names in the places
// people write them (greetings, sign-offs, "my name is", honorifics).
//
// Pattern matching misses names that appear without such a cue and can flag
// a number that only looks like an id. A scan is a strong first line, not a
// guarantee; docs/pii.md says so to the customer.
package pii

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Kind is one category of personal data.
type Kind string

// The kinds a scan finds.
const (
	KindEmail      Kind = "email"
	KindPhone      Kind = "phone"
	KindCard       Kind = "card"
	KindNationalID Kind = "national_id"
	KindBank       Kind = "bank_account"
	KindName       Kind = "name"
)

// Kinds lists every kind, in the order matches win an overlap.
func Kinds() []Kind {
	return []Kind{KindEmail, KindCard, KindBank, KindNationalID, KindPhone, KindName}
}

// ValidKind reports whether k is a known kind.
func ValidKind(k string) bool {
	for _, x := range Kinds() {
		if string(x) == k {
			return true
		}
	}
	return false
}

// Mode is what an import does with what a scan finds.
type Mode string

// Policy modes.
const (
	ModeOff    Mode = "off"    // no scan
	ModeWarn   Mode = "warn"   // scan and report; the text is kept as is
	ModeMask   Mode = "mask"   // keep the shape, hide most characters (j***@example.com, card ending 4242)
	ModeRedact Mode = "redact" // replace with a stable placeholder: the same value gives the same token in every row
	ModeBlock  Mode = "block"  // refuse a row that holds personal data
)

// Modes lists the modes.
func Modes() []Mode { return []Mode{ModeOff, ModeWarn, ModeMask, ModeRedact, ModeBlock} }

// ValidMode reports whether m is a known mode.
func ValidMode(m string) bool {
	for _, x := range Modes() {
		if string(x) == m {
			return true
		}
	}
	return false
}

// Policy is one benchmark's personal data policy.
type Policy struct {
	Mode Mode `json:"mode"`
	// Kinds limits the scan to these kinds; empty scans every kind.
	Kinds []Kind `json:"kinds,omitempty"`
}

// Validate checks the mode and kinds.
func (p Policy) Validate() error {
	if p.Mode != "" && !ValidMode(string(p.Mode)) {
		return fmt.Errorf("pii mode %q: want off, warn, mask, redact or block", p.Mode)
	}
	for _, k := range p.Kinds {
		if !ValidKind(string(k)) {
			return fmt.Errorf("pii kind %q: want email, phone, card, national_id, bank_account or name", k)
		}
	}
	return nil
}

// Effective is the mode in force: empty reads as warn, so an import always
// tells the customer what it saw.
func (p Policy) Effective() Mode {
	if p.Mode == "" {
		return ModeWarn
	}
	return p.Mode
}

func (p Policy) wants(k Kind) bool {
	if len(p.Kinds) == 0 {
		return true
	}
	for _, x := range p.Kinds {
		if x == k {
			return true
		}
	}
	return false
}

// Match is one finding in a text: byte offsets [Start, End).
type Match struct {
	Kind  Kind
	Start int
	End   int
	Value string
}

type detector struct {
	kind  Kind
	re    *regexp.Regexp
	group int                     // capture group holding the value; 0 = whole match
	check func(value string) bool // nil accepts every match
}

var detectors = []detector{
	{kind: KindEmail, re: regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)},
	{kind: KindCard, re: regexp.MustCompile(`\b\d(?:[ \-]?\d){12,18}\b`), check: cardOK},
	{kind: KindBank, re: regexp.MustCompile(`\b[A-Z]{2}\d{2}(?: ?[A-Z0-9]{4}){2,7}(?: ?[A-Z0-9]{1,4})?\b`), check: ibanOK},
	{kind: KindNationalID, re: regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`), check: ssnOK},                                                // US SSN
	{kind: KindNationalID, re: regexp.MustCompile(`\b[A-CEGHJ-PR-TW-Z][A-CEGHJ-NPR-TW-Z] ?\d{2} ?\d{2} ?\d{2} ?[A-D]\b`), check: ninoOK}, // UK NINO
	{kind: KindNationalID, re: regexp.MustCompile(`\b[2-9]\d{3} ?\d{4} ?\d{4}\b`), check: aadhaarOK},                                     // India Aadhaar
	{kind: KindNationalID, re: regexp.MustCompile(`\b[A-Z]{3}[ABCFGHLJPT][A-Z]\d{4}[A-Z]\b`)},                                            // India PAN
	{kind: KindNationalID, re: regexp.MustCompile(`\b\d{3}[ \-]\d{3}[ \-]\d{3}\b`), check: sinOK},                                        // Canada SIN
	{kind: KindPhone, re: regexp.MustCompile(`(?:\+\d{1,3}[ .\-]?)?(?:\(\d{1,4}\)[ .\-]?)?\d{2,5}(?:[ .\-]\d{2,5}){1,4}\b|\+\d{8,15}\b`), check: phoneOK},
	{kind: KindName, re: regexp.MustCompile(`(?:\b(?i:my name is|my name's|name:|full name:|customer:|dear|hi|hello|hey|regards,|thanks,|thank you,|cheers,|sincerely,|best,|signed,)[ \t]*(?:\r?\n[ \t]*)?)(\p{Lu}\p{Ll}+(?:[ \-'](?:\p{Lu}\p{Ll}+|\p{Lu}\.)){0,2})`), group: 1, check: nameOK},
	{kind: KindName, re: regexp.MustCompile(`\b(?:Mr|Mrs|Ms|Mx|Miss|Dr|Prof)\.? (\p{Lu}\p{Ll}+(?: \p{Lu}\p{Ll}+)?)`), group: 1, check: nameOK},
}

// Scan finds the personal data in s that p asks for. Overlapping matches
// keep the one found first in Kinds order, then the longer one.
func Scan(s string, p Policy) []Match {
	if p.Effective() == ModeOff || s == "" {
		return nil
	}
	var all []Match
	for _, d := range detectors {
		if !p.wants(d.kind) {
			continue
		}
		for _, loc := range d.re.FindAllStringSubmatchIndex(s, -1) {
			a, b := loc[0], loc[1]
			if d.group > 0 {
				a, b = loc[2*d.group], loc[2*d.group+1]
			}
			if a < 0 {
				continue
			}
			v := s[a:b]
			if d.check != nil && !d.check(v) {
				continue
			}
			all = append(all, Match{Kind: d.kind, Start: a, End: b, Value: v})
		}
	}
	rank := map[Kind]int{}
	for i, k := range Kinds() {
		rank[k] = i
	}
	sort.SliceStable(all, func(i, j int) bool {
		if rank[all[i].Kind] != rank[all[j].Kind] {
			return rank[all[i].Kind] < rank[all[j].Kind]
		}
		return all[i].End-all[i].Start > all[j].End-all[j].Start
	})
	var kept []Match
	for _, m := range all {
		clash := false
		for _, k := range kept {
			if m.Start < k.End && k.Start < m.End {
				clash = true
				break
			}
		}
		if !clash {
			kept = append(kept, m)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Start < kept[j].Start })
	return kept
}

// Redactor applies a policy. Key makes redact placeholders stable: the
// same value under the same key always gives the same token, so a support
// thread that names one customer twice still reads as one customer. An
// empty key still gives tokens that are stable within one process.
type Redactor struct {
	Policy Policy
	Key    []byte
}

// Result is what Apply did to one text.
type Result struct {
	Text    string
	Matches []Match
}

// Apply scans s and, for mask and redact, rewrites it. warn and block keep
// the text; the caller decides what block refuses.
func (r Redactor) Apply(s string) Result {
	ms := Scan(s, r.Policy)
	mode := r.Policy.Effective()
	if len(ms) == 0 || (mode != ModeMask && mode != ModeRedact) {
		return Result{Text: s, Matches: ms}
	}
	var b strings.Builder
	last := 0
	for _, m := range ms {
		b.WriteString(s[last:m.Start])
		if mode == ModeMask {
			b.WriteString(Mask(m.Kind, m.Value))
		} else {
			b.WriteString(r.Token(m.Kind, m.Value))
		}
		last = m.End
	}
	b.WriteString(s[last:])
	return Result{Text: b.String(), Matches: ms}
}

// Token is the redact placeholder of one value: [EMAIL_3f9a1c].
func (r Redactor) Token(k Kind, v string) string {
	mac := hmac.New(sha256.New, r.Key)
	mac.Write([]byte(string(k) + "\x00" + normalize(k, v)))
	return "[" + strings.ToUpper(string(k)) + "_" + hex.EncodeToString(mac.Sum(nil))[:6] + "]"
}

func normalize(k Kind, v string) string {
	switch k {
	case KindEmail, KindName:
		return strings.ToLower(strings.TrimSpace(v))
	}
	return digitsAndLetters(v)
}

// Mask hides most of a value and keeps its shape: the first letter and the
// domain of an email, the last 4 digits of a card or account, the last 2 of
// a phone or id, the initials of a name.
func Mask(k Kind, v string) string {
	switch k {
	case KindEmail:
		at := strings.LastIndex(v, "@")
		if at <= 0 {
			return "***"
		}
		return v[:1] + "***" + v[at:]
	case KindName:
		parts := strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == '-' })
		for i, p := range parts {
			rs := []rune(p)
			parts[i] = string(rs[0]) + "."
		}
		return strings.Join(parts, " ")
	case KindCard, KindBank:
		return keepLast(v, 4)
	default:
		return keepLast(v, 2)
	}
}

// keepLast replaces every letter and digit but the last n with '*',
// keeping separators.
func keepLast(v string, n int) string {
	rs := []rune(v)
	seen := 0
	for i := len(rs) - 1; i >= 0; i-- {
		if unicode.IsDigit(rs[i]) || unicode.IsLetter(rs[i]) {
			seen++
			if seen > n {
				rs[i] = '*'
			}
		}
	}
	return string(rs)
}

// Summary counts matches per kind.
type Summary map[Kind]int

// Add counts ms.
func (s Summary) Add(ms []Match) {
	for _, m := range ms {
		s[m.Kind]++
	}
}

// Total is the number of matches.
func (s Summary) Total() int {
	n := 0
	for _, v := range s {
		n += v
	}
	return n
}

// String lists the counts, for example "2 emails, 1 phone".
func (s Summary) String() string {
	var parts []string
	for _, k := range Kinds() {
		if n := s[k]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, label(k, n)))
		}
	}
	return strings.Join(parts, ", ")
}

func label(k Kind, n int) string {
	names := map[Kind][2]string{
		KindEmail: {"email", "emails"}, KindPhone: {"phone number", "phone numbers"}, KindCard: {"card number", "card numbers"},
		KindNationalID: {"national id", "national ids"}, KindBank: {"bank account", "bank accounts"}, KindName: {"name", "names"},
	}
	if n == 1 {
		return names[k][0]
	}
	return names[k][1]
}

func digitsAndLetters(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) || unicode.IsLetter(r) {
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return b.String()
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func luhn(d string) bool {
	sum, alt := 0, false
	for i := len(d) - 1; i >= 0; i-- {
		n := int(d[i] - '0')
		if alt {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		alt = !alt
	}
	return sum%10 == 0
}

func cardOK(v string) bool {
	d := digits(v)
	if len(d) < 13 || len(d) > 19 || strings.Count(d, d[:1]) == len(d) {
		return false
	}
	// Major networks start with 2 to 6.
	if d[0] < '2' || d[0] > '6' {
		return false
	}
	return luhn(d)
}

func ibanOK(v string) bool {
	s := digitsAndLetters(v)
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	r := s[4:] + s[:4]
	mod := 0
	for _, c := range r {
		var n int
		switch {
		case c >= '0' && c <= '9':
			n = int(c - '0')
			mod = (mod*10 + n) % 97
		case c >= 'A' && c <= 'Z':
			n = int(c-'A') + 10
			mod = (mod*100 + n) % 97
		default:
			return false
		}
	}
	return mod == 1
}

func ssnOK(v string) bool {
	d := digits(v)
	area, group, serial := d[:3], d[3:5], d[5:]
	return area != "000" && area != "666" && area[0] != '9' && group != "00" && serial != "0000"
}

func ninoOK(v string) bool {
	p := strings.ToUpper(v[:2])
	switch p {
	case "BG", "GB", "NK", "KN", "TN", "NT", "ZZ":
		return false
	}
	return true
}

var verhoeffD = [10][10]int{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, {1, 2, 3, 4, 0, 6, 7, 8, 9, 5}, {2, 3, 4, 0, 1, 7, 8, 9, 5, 6},
	{3, 4, 0, 1, 2, 8, 9, 5, 6, 7}, {4, 0, 1, 2, 3, 9, 5, 6, 7, 8}, {5, 9, 8, 7, 6, 0, 4, 3, 2, 1},
	{6, 5, 9, 8, 7, 1, 0, 4, 3, 2}, {7, 6, 5, 9, 8, 2, 1, 0, 4, 3}, {8, 7, 6, 5, 9, 3, 2, 1, 0, 4},
	{9, 8, 7, 6, 5, 4, 3, 2, 1, 0},
}

var verhoeffP = [8][10]int{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, {1, 5, 7, 6, 2, 8, 3, 0, 9, 4}, {5, 8, 0, 3, 7, 9, 6, 1, 4, 2},
	{8, 9, 1, 6, 0, 4, 3, 5, 2, 7}, {9, 4, 5, 3, 1, 2, 6, 8, 7, 0}, {4, 2, 8, 6, 5, 7, 3, 9, 0, 1},
	{2, 7, 9, 3, 8, 0, 6, 4, 1, 5}, {7, 0, 4, 6, 9, 1, 3, 2, 5, 8},
}

func aadhaarOK(v string) bool {
	d := digits(v)
	if len(d) != 12 {
		return false
	}
	c := 0
	for i := 0; i < len(d); i++ {
		n := int(d[len(d)-1-i] - '0')
		c = verhoeffD[c][verhoeffP[i%8][n]]
	}
	return c == 0
}

func sinOK(v string) bool {
	d := digits(v)
	return len(d) == 9 && d[0] != '0' && d[0] != '8' && luhn(d)
}

func phoneOK(v string) bool {
	d := digits(v)
	if len(d) < 9 || len(d) > 15 {
		return false
	}
	// A date (2026-11-22) or a version (1.2.3) is not a phone number: a
	// phone has a leading +, a parenthesized area code, or at least 10
	// digits in groups of 3 or more (at most 11: a longer bare run is an
	// account or id number).
	t := strings.TrimSpace(v)
	if strings.HasPrefix(t, "+") || strings.Contains(t, "(") {
		return true
	}
	if len(d) < 10 || len(d) > 11 {
		return false
	}
	for _, g := range strings.FieldsFunc(t, func(r rune) bool { return r == ' ' || r == '.' || r == '-' }) {
		if len(g) < 3 {
			return false
		}
	}
	return true
}

// notNames are capitalized words that follow a greeting but are no name.
var notNames = map[string]bool{
	"there": true, "team": true, "all": true, "everyone": true, "support": true, "sir": true, "madam": true,
	"customer": true, "folks": true, "guys": true, "again": true, "friend": true, "friends": true, "world": true,
	"i": true, "we": true, "the": true, "this": true, "my": true, "our": true, "your": true, "thanks": true,
	"thank": true, "please": true, "can": true, "could": true, "would": true, "is": true, "it": true, "just": true,
	"so": true, "yes": true, "no": true, "ok": true, "okay": true, "hello": true, "hi": true, "regards": true,
	"best": true, "kind": true, "warm": true, "sincerely": true, "admin": true, "manager": true, "user": true,
	"agent": true, "bot": true, "assistant": true, "help": true, "how": true, "what": true, "when": true,
	"why": true, "where": true, "who": true, "which": true, "do": true, "does": true, "did": true,
}

func nameOK(v string) bool {
	first := strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == '-' || r == '\'' })
	if len(first) == 0 {
		return false
	}
	return !notNames[strings.ToLower(first[0])]
}
