package livescore

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/traceimport"
)

// Signal kinds. A rate signal has values in [0, 1] (a share or a yes/no);
// the comparison tests the difference of means. An ordinal signal is
// unbounded (milliseconds, dollars, tokens); the comparison tests which
// version's value is larger in each pair, so one huge value cannot dominate.
const (
	KindBounded = "rate"
	KindOrdinal = "ordinal"
)

// SignalDef describes one signal: its name, kind, which way is better, how
// it is measured, and how to read it from a session.
type SignalDef struct {
	Name         string `json:"name"`
	Label        string `json:"label"`
	Kind         string `json:"kind"`
	LowerBetter  bool   `json:"lower_is_better"`
	Unit         string `json:"unit,omitempty"`
	Method       string `json:"method"` // how it is measured, and what it does not see
	value        func(*Signals) (float64, bool)
	defaultShown bool // compared unless compare.signals says otherwise
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// signalDefs is the registry, in display order.
var signalDefs = []SignalDef{
	{Name: "request_error_rate", Label: "Request errors", Kind: KindBounded, LowerBetter: true, defaultShown: true,
		Method: "share of the session's gateway requests whose Bifrost row has status error (provider errors after retries, governance refusals)",
		value: func(s *Signals) (float64, bool) {
			if s.RequestErrorRate == nil {
				return 0, false
			}
			return *s.RequestErrorRate, true
		}},
	{Name: "tool_error_rate", Label: "Tool errors", Kind: KindBounded, LowerBetter: true, defaultShown: true,
		Method: "share of tool results in the transcript that say they failed (an MCP isError result, an object with an error field, or text starting with Error:); unknown when the session made no tool call or Bifrost served no transcript. A tool that fails silently is not seen",
		value: func(s *Signals) (float64, bool) {
			if s.ToolErrorRate == nil {
				return 0, false
			}
			return *s.ToolErrorRate, true
		}},
	{Name: "loop", Label: "Loops", Kind: KindBounded, LowerBetter: true, defaultShown: true,
		Method: "the same tool call (name and arguments) 3 or more times in a row, the triage pattern_loop rule; unknown without a transcript",
		value: func(s *Signals) (float64, bool) {
			if s.Loop == nil {
				return 0, false
			}
			return boolValue(*s.Loop), true
		}},
	{Name: "repeated_calls", Label: "Repeated identical calls", Kind: KindOrdinal, LowerBetter: true, Unit: "calls",
		Method: "tool calls identical in name and arguments to an earlier call of the session; unknown without a transcript",
		value: func(s *Signals) (float64, bool) {
			if s.RepeatedCalls == nil {
				return 0, false
			}
			return float64(*s.RepeatedCalls), true
		}},
	{Name: "retried", Label: "Retried", Kind: KindBounded, LowerBetter: true, defaultShown: true,
		Method: "the session had a request Bifrost retried (number_of_retries above 0)",
		value:  func(s *Signals) (float64, bool) { return boolValue(s.Retries > 0), true }},
	{Name: "fallback", Label: "Fallbacks", Kind: KindBounded, LowerBetter: true, defaultShown: true,
		Method: "the session had a request served by a fallback (fallback_index above 0)",
		value:  func(s *Signals) (float64, bool) { return boolValue(s.Fallbacks > 0), true }},
	{Name: "refusal", Label: "Refusals", Kind: KindBounded, LowerBetter: true, defaultShown: true,
		Method: "a row with stop_reason content_filter, a refusal field on the model's message, or a final answer that opens with a refusal phrase (a heuristic: a polite refusal in other words is missed, and a correct refusal of a bad request counts the same)",
		value: func(s *Signals) (float64, bool) {
			if s.Refusal == nil {
				return 0, false
			}
			return boolValue(*s.Refusal), true
		}},
	{Name: "budget_hit", Label: "Budget hits", Kind: KindBounded, LowerBetter: true, defaultShown: true,
		Method: "a request refused by a governance budget (error type budget_exceeded or status 402)",
		value:  func(s *Signals) (float64, bool) { return boolValue(s.BudgetHits > 0), true }},
	{Name: "rate_limited", Label: "Rate limited", Kind: KindBounded, LowerBetter: true,
		Method: "a request answered 429 (a governance rate limit or the provider's)",
		value:  func(s *Signals) (float64, bool) { return boolValue(s.RateLimited > 0), true }},
	{Name: "injection_marker", Label: "Injection markers", Kind: KindBounded, LowerBetter: true, defaultShown: true,
		Method: "a tool result holding a prompt-injection phrase (ignore previous instructions, new system instructions, chat-template tags, requests to send credentials); a heuristic list, so a novel injection is missed. It says the agent was exposed, not that it obeyed",
		value: func(s *Signals) (float64, bool) {
			if s.InjectionMarkers == nil {
				return 0, false
			}
			return boolValue(*s.InjectionMarkers > 0), true
		}},
	{Name: "abandoned", Label: "Abandoned", Kind: KindBounded, LowerBetter: true, defaultShown: true,
		Method: "the session's last request failed and nothing came after it within the session gap",
		value:  func(s *Signals) (float64, bool) { return boolValue(s.Abandoned), true }},
	{Name: "turn_latency_ms", Label: "Latency per turn", Kind: KindOrdinal, LowerBetter: true, Unit: "ms", defaultShown: true,
		Method: "mean of Bifrost's latency over the session's requests",
		value: func(s *Signals) (float64, bool) {
			if s.TurnLatencyMS == nil {
				return 0, false
			}
			return *s.TurnLatencyMS, true
		}},
	{Name: "session_ms", Label: "Latency per session", Kind: KindOrdinal, LowerBetter: true, Unit: "ms", defaultShown: true,
		Method: "first request's timestamp to the last request's timestamp plus its latency; includes the agent's own time between requests",
		value: func(s *Signals) (float64, bool) {
			if s.SessionMS == nil {
				return 0, false
			}
			return *s.SessionMS, true
		}},
	{Name: "cost_usd", Label: "Cost per session", Kind: KindOrdinal, LowerBetter: true, Unit: "USD", defaultShown: true,
		Method: "Bifrost's own cost field summed over the session's rows (its pricing, not the bench's pinned snapshot); unknown when an answered row has no cost",
		value: func(s *Signals) (float64, bool) {
			if s.CostUSD == nil {
				return 0, false
			}
			return *s.CostUSD, true
		}},
	{Name: "tokens", Label: "Tokens per session", Kind: KindOrdinal, LowerBetter: true, Unit: "tokens", defaultShown: true,
		Method: "prompt plus completion tokens from Bifrost's token_usage, summed; unknown when an answered row has none",
		value: func(s *Signals) (float64, bool) {
			if s.Tokens == nil {
				return 0, false
			}
			return float64(*s.Tokens), true
		}},
	{Name: "judge_score", Label: "Judge score", Kind: KindBounded, LowerBetter: false,
		Method: "a model judge's 0 to 1 score of the redacted transcript, on a sample; reported only, and compared only when a judge calibration record covers the exact model and prompt",
		value: func(s *Signals) (float64, bool) {
			if s.JudgeScore == nil {
				return 0, false
			}
			return *s.JudgeScore, true
		}},
}

// Signals returns the signal registry.
func SignalDefs() []SignalDef { return slices.Clone(signalDefs) }

// SignalNames lists every signal name.
func SignalNames() []string {
	out := make([]string, len(signalDefs))
	for i, d := range signalDefs {
		out[i] = d.Name
	}
	return out
}

// ComparedByDefault lists the signals a comparison tests when the config
// names none: every signal but repeated_calls (loop covers it), rate_limited
// (usually traffic, not the agent) and judge_score (uncalibrated by default).
func ComparedByDefault() []string {
	var out []string
	for _, d := range signalDefs {
		if d.defaultShown {
			out = append(out, d.Name)
		}
	}
	return out
}

func signalByName(name string) (SignalDef, bool) {
	for _, d := range signalDefs {
		if d.Name == name {
			return d, true
		}
	}
	return SignalDef{}, false
}

// Value reads one signal of a session.
func (d SignalDef) Value(s *Signals) (float64, bool) { return d.value(s) }

// refusalOpen matches a final answer that opens with a refusal.
var refusalOpen = regexp.MustCompile(`(?i)^\s*(?:(?:i'?m|i am) sorry|sorry|unfortunately|apologies)?[,.!]?\s*(?:but\s+)?(?:i\s+(?:can(?:no|')t|cannot|won'?t|will not|am unable to|'m unable to|am not able to|must decline to)\s+(?:help|assist|comply|provide|do that|do this|support|answer|fulfil|fulfill|share|complete))|^\s*as an ai\b`)

// injectionMarkers are phrases that try to give the model instructions from
// inside a tool result.
var injectionMarkers = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:ignore|disregard|forget)\s+(?:all\s+|any\s+)?(?:the\s+|your\s+)?(?:previous|prior|above|earlier|preceding|original)\s+(?:instructions|prompts?|messages|rules|directions)`),
	regexp.MustCompile(`(?i)\bnew\s+(?:system\s+)?instructions?\s*:`),
	regexp.MustCompile(`(?i)\byou\s+are\s+now\s+(?:in\s+)?(?:developer\s+mode|dan\b|jailbr)`),
	regexp.MustCompile(`(?i)<\|?\s*(?:im_start|im_end|system|endoftext)\s*\|?>`),
	regexp.MustCompile(`(?i)\b(?:send|post|forward|exfiltrate|email|upload)\s+(?:the\s+|your\s+|all\s+)?(?:api[\s_-]?keys?|credentials|passwords?|secrets?|tokens?)\b`),
	regexp.MustCompile(`(?i)\bdo\s+not\s+(?:tell|inform|alert|mention\s+(?:this\s+)?to)\s+the\s+user\b`),
	regexp.MustCompile(`(?i)\b(?:reveal|print|output|repeat)\s+(?:your\s+|the\s+)?(?:system\s+prompt|hidden\s+instructions)\b`),
}

// hasInjectionMarker reports whether text holds an injection phrase.
func hasInjectionMarker(text string) bool {
	for _, re := range injectionMarkers {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// severity weights a flag for the worst-sessions ranking.
var severity = map[string]int{
	FlagAbandoned: 5, FlagInjection: 4, FlagLoop: 3, FlagBudget: 3, FlagToolError: 2, FlagRequestErr: 2,
	FlagRefusal: 1, FlagRateLimited: 1, FlagFallback: 0, FlagCut: 0,
}

// failureFlags are the flags that make a session an inbox candidate.
var failureFlags = []string{FlagAbandoned, FlagInjection, FlagLoop, FlagBudget, FlagToolError, FlagRequestErr, FlagRefusal}

// measure computes a session's signals from its rows (oldest first, detail
// rows when transcript is set). transcript is the session parsed from the
// last row's conversation, nil when Bifrost served none. It returns the
// signals and the flags.
func measure(rows []Row, transcript *traceimport.Session, contentHidden bool) (Signals, []string) {
	var s Signals
	var flags []string
	flag := func(f string) {
		if !slices.Contains(flags, f) {
			flags = append(flags, f)
		}
	}
	n := len(rows)
	if n == 0 {
		return s, nil
	}
	var latSum, latMax float64
	latN := 0
	cost, costKnown := 0.0, true
	var tokens int64
	tokensKnown := true
	contentFilter := false
	for i := range rows {
		r := &rows[i]
		if r.Status == statusError {
			s.RequestErrors++
			switch {
			case r.ErrorDetails.typ() == budgetErrorType || r.ErrorDetails.status() == 402:
				s.BudgetHits++
			case r.ErrorDetails.status() == 429:
				s.RateLimited++
			}
		}
		s.Retries += r.Retries
		if r.FallbackIndex > 0 {
			s.Fallbacks++
		}
		if r.Latency != nil && *r.Latency >= 0 {
			latSum += *r.Latency
			latN++
			latMax = max(latMax, *r.Latency)
		}
		switch {
		case r.Cost != nil:
			cost += *r.Cost
		case r.Status != statusError:
			costKnown = false
		}
		switch t := r.tokens(); {
		case t >= 0:
			tokens += t
		case r.Status != statusError:
			tokensKnown = false
		}
		if r.StopReason != nil && *r.StopReason == "content_filter" {
			contentFilter = true
		}
		if r.refusalField() {
			contentFilter = true
		}
	}
	rate := float64(s.RequestErrors) / float64(n)
	s.RequestErrorRate = &rate
	if latN > 0 {
		mean := latSum / float64(latN)
		s.TurnLatencyMS, s.MaxTurnLatencyMS = &mean, &latMax
	}
	first, last := rows[0], rows[n-1]
	span := float64(last.Timestamp.Sub(first.Timestamp).Milliseconds())
	if last.Latency != nil && *last.Latency > 0 {
		span += *last.Latency
	}
	if span >= 0 {
		s.SessionMS = &span
	}
	if costKnown {
		s.CostUSD = &cost
	}
	if tokensKnown {
		s.Tokens = &tokens
	}
	s.Abandoned = last.Status == statusError
	s.ContentHidden = contentHidden
	if transcript != nil && !contentHidden {
		calls := len(transcript.ToolCalls)
		errs, inj, repeated := 0, 0, 0
		seen := map[string]bool{}
		run, longest := 0, 0
		prev := ""
		withResult := 0
		for _, c := range transcript.ToolCalls {
			key := c.Name + "\x00" + canonicalJSON(c.Arguments)
			if seen[key] {
				repeated++
			}
			seen[key] = true
			if key == prev {
				run++
			} else {
				run = 1
			}
			prev = key
			longest = max(longest, run)
			if c.Result != "" || c.Error != "" || c.IsError {
				withResult++
			}
			if c.IsError {
				errs++
			}
			if hasInjectionMarker(c.Result) || hasInjectionMarker(c.Error) {
				inj++
			}
		}
		// Tool results the transcript holds outside a matched call (a tool
		// message whose call id is unknown) are scanned for markers too.
		for _, t := range transcript.Turns {
			if t.Role == "tool" && hasInjectionMarker(t.Text) && !toolTextCounted(transcript, t.Text) {
				inj++
			}
		}
		s.ToolCalls, s.ToolErrors, s.RepeatedCalls, s.InjectionMarkers = &calls, &errs, &repeated, &inj
		if withResult > 0 {
			r := float64(errs) / float64(withResult)
			s.ToolErrorRate = &r
		}
		loop := longest >= 3
		s.Loop = &loop
		refused := contentFilter || refusalOpen.MatchString(transcript.FinalAnswer)
		s.Refusal = &refused
	} else if contentFilter {
		t := true
		s.Refusal = &t
	}
	if s.Abandoned {
		flag(FlagAbandoned)
	}
	if s.RequestErrors > 0 {
		flag(FlagRequestErr)
	}
	if s.ToolErrors != nil && *s.ToolErrors > 0 {
		flag(FlagToolError)
	}
	if s.Loop != nil && *s.Loop {
		flag(FlagLoop)
	}
	if s.InjectionMarkers != nil && *s.InjectionMarkers > 0 {
		flag(FlagInjection)
	}
	if s.BudgetHits > 0 {
		flag(FlagBudget)
	}
	if s.RateLimited > 0 {
		flag(FlagRateLimited)
	}
	if s.Refusal != nil && *s.Refusal {
		flag(FlagRefusal)
	}
	if s.Fallbacks > 0 {
		flag(FlagFallback)
	}
	return s, flags
}

// toolTextCounted reports whether a tool turn's text is the result of a
// call already scanned.
func toolTextCounted(s *traceimport.Session, text string) bool {
	for _, c := range s.ToolCalls {
		if c.Result == text || c.Error == text {
			return true
		}
	}
	return false
}

// severityOf sums the weights of a session's flags.
func severityOf(flags []string) int {
	n := 0
	for _, f := range flags {
		n += severity[f]
	}
	return n
}

// isFailure reports whether flags make the session an inbox candidate.
func isFailure(flags []string) bool {
	for _, f := range flags {
		if slices.Contains(failureFlags, f) {
			return true
		}
	}
	return false
}

// canonicalJSON renders arguments with sorted keys and no spaces, so two
// identical calls compare equal whatever the agent's key order.
func canonicalJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return strings.TrimSpace(string(raw))
	}
	// A JSON string holding JSON (OpenAI's arguments) is decoded once more.
	if s, ok := v.(string); ok {
		var inner any
		d2 := json.NewDecoder(strings.NewReader(s))
		d2.UseNumber()
		if d2.Decode(&inner) == nil {
			v = inner
		}
	}
	b, err := json.Marshal(v) // encoding/json sorts map keys
	if err != nil {
		return strings.TrimSpace(string(raw))
	}
	return string(b)
}
