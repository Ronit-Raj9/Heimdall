// Package livescore scores real production sessions read from the Bifrost
// gateway a team's agents already use. It samples sessions read-only from
// the gateway's log API, measures signals that need no ground truth (tool
// errors, loops, retries and fallbacks, refusals, latency, cost and tokens,
// budget hits, injection markers in tool results, abandonment), compares two
// live agent versions with an always-valid sequential test per signal,
// alerts, and sends the worst failing sessions to the failure inbox
// (bench/traceinbox) with provenance live.
//
// Live scores are their own evidence class, "live". They never enter an
// experiment's verdict: production traffic has no ground truth (nobody knows
// whether a session's answer was right) and no control of inputs (the two
// versions saw different users asking different things), so a live score
// can say "version B errors more often than version A on the traffic each
// one received", never "B fails more tasks".
//
// The package never writes to the gateway: every request it sends is a GET
// to the log API, enforced at the transport (readOnlyTransport), and it
// never stores a prompt or an output. Each session is reduced to numbers
// and flags in memory; only a failing session pushed to the inbox keeps a
// transcript, redacted by traceimport's redactor as every inbox item is.
// docs/live-scoring.md is the user guide.
package livescore

import (
	"errors"
	"time"
)

// EvidenceClass labels every live score (the contract name in
// docs/extensions-design.md).
const EvidenceClass = "live"

// Files under the project's livescore folder.
const (
	Dir          = "livescore"      // under the project root
	ConfigFile   = "config.yaml"    // sources, versions, comparison, thresholds
	SessionsFile = "sessions.jsonl" // one scored session per line: numbers and flags, never text
	StateFile    = "state.json"     // the pull cursor, the comparison and the alert state
	LogFile      = "log.jsonl"      // pulls, alerts and comparison decisions, appended
	lockFileName = ".lock"
)

// Caps. A pull stops at its caps and says so; the store never grows past
// MaxSessions.
const (
	MaxSessions        = 50000 // scored sessions kept, newest last; sample busy traffic to stay under it
	MaxRowsPerPull     = 20000 // list rows scanned per version per pull
	MaxDetailsPerPull  = 5000  // detail rows fetched per pull
	MaxRowsPerSession  = 400   // rows one session may hold; a longer one is cut and marked
	MaxQueue           = 5000  // unpaired sessions one comparison arm keeps per signal
	MaxInboxPerPull    = 50    // the largest inbox.max_per_pull
	DefaultInboxPush   = 5     // failure signatures pushed to the inbox per pull
	DefaultGap         = 10 * time.Minute
	DefaultWindow      = 24 * time.Hour
	DefaultOverlap     = 15 * time.Minute
	DefaultRetention   = 30 * 24 * time.Hour
	DefaultMinPairs    = 30
	DefaultAlpha       = 0.05
	DefaultSettle      = 10 * time.Second // rows younger than this may still be written by Bifrost's batched writer
	maxLogLineBytes    = 4 << 10
	maxSessionsFileLen = 512 << 20
)

// Session grouping methods (SessionRecord.Grouping).
const (
	GroupSessionID = "session_id" // Bifrost's session_id: the x-bf-session-id header or a harness session header
	GroupGap       = "gap"        // no session id: the same virtual key (and user) with no gap longer than sessions.gap
)

// Flags a scored session can carry (SessionRecord.Flags). A flagged
// session is a failure candidate for the inbox.
const (
	FlagAbandoned   = "abandoned"        // the session ended on an error
	FlagRequestErr  = "request_error"    // a request failed at the gateway
	FlagToolError   = "tool_error"       // a tool result said it failed
	FlagLoop        = "loop"             // the same tool call 3 or more times in a row
	FlagInjection   = "injection_marker" // a tool result held a prompt-injection marker
	FlagBudget      = "budget_hit"       // a governance budget refused a request
	FlagRateLimited = "rate_limited"     // a request got 429
	FlagRefusal     = "refusal"          // the model refused
	FlagFallback    = "fallback"         // a request was served by a fallback
	FlagCut         = "cut"              // the session held more rows than MaxRowsPerSession
)

// Comparison outcomes (Comparison.Result).
const (
	ResultBetter  = "better"
	ResultWorse   = "worse"
	ResultUnknown = "not_yet_known"
)

// ErrNotConfigured marks a project without livescore/config.yaml.
// ErrOffline is a pull refused by a server started with -offline: a pull
// reads the gateway and may call the judge, both outbound.
var ErrOffline = errors.New("live pull is off: this server runs -offline (no gateway, no outbound calls); stored live scores stay readable")

var ErrNotConfigured = errors.New("live scoring is not configured: run `bifrost-bench livescore init` (docs/live-scoring.md)")

// SessionRecord is one scored session: who (version, grouping), when, how
// many requests, the signals and the flags. It never holds a prompt, an
// output, a tool argument or a tool result.
type SessionRecord struct {
	Seq        int64     `json:"seq"`      // order of scoring, increasing; the comparison consumes by it
	ID         string    `json:"id"`       // ls_ and 16 hex characters of a hash of the version and the session key
	Version    string    `json:"version"`  // the configured version whose selector matched
	Evidence   string    `json:"evidence"` // always "live"
	Grouping   string    `json:"grouping"` // session_id | gap
	Concurrent bool      `json:"concurrent,omitempty"`
	Started    time.Time `json:"started"`
	Ended      time.Time `json:"ended"`
	ScoredAt   time.Time `json:"scored_at"`
	Requests   int       `json:"requests"`
	Model      string    `json:"model,omitempty"`    // provider/model of the last request
	LastRow    string    `json:"last_row,omitempty"` // the last request's Bifrost log row id, for the link back
	Signals    Signals   `json:"signals"`
	Flags      []string  `json:"flags,omitempty"`
	Severity   int       `json:"severity,omitempty"`
	Signature  string    `json:"signature,omitempty"`  // failure inbox signature, for flagged sessions with a transcript
	InboxItem  string    `json:"inbox_item,omitempty"` // the inbox item this session was pushed to
}

// Signals are the measurements of one session. A nil pointer is unknown
// (the transcript was hidden, the session made no tool call, Bifrost
// reported no cost), never zero.
type Signals struct {
	RequestErrors    int      `json:"request_errors"`
	RequestErrorRate *float64 `json:"request_error_rate,omitempty"`
	ToolCalls        *int     `json:"tool_calls,omitempty"`
	ToolErrors       *int     `json:"tool_errors,omitempty"`
	ToolErrorRate    *float64 `json:"tool_error_rate,omitempty"`
	RepeatedCalls    *int     `json:"repeated_calls,omitempty"` // tool calls identical (name and arguments) to an earlier call
	Loop             *bool    `json:"loop,omitempty"`
	Retries          int      `json:"retries"`
	Fallbacks        int      `json:"fallbacks"`
	Refusal          *bool    `json:"refusal,omitempty"`
	TurnLatencyMS    *float64 `json:"turn_latency_ms,omitempty"` // mean over requests with a latency
	MaxTurnLatencyMS *float64 `json:"max_turn_latency_ms,omitempty"`
	SessionMS        *float64 `json:"session_ms,omitempty"` // first request start to last request end
	CostUSD          *float64 `json:"cost_usd,omitempty"`   // Bifrost's own cost per row, summed; unknown when any row has none
	Tokens           *int64   `json:"tokens,omitempty"`     // prompt plus completion tokens; unknown when any answered row has none
	BudgetHits       int      `json:"budget_hits"`
	RateLimited      int      `json:"rate_limited"`
	InjectionMarkers *int     `json:"injection_markers,omitempty"`
	Abandoned        bool     `json:"abandoned"`
	JudgeScore       *float64 `json:"judge_score,omitempty"` // 0 to 1, higher is better
	JudgeCalibrated  bool     `json:"judge_calibrated,omitempty"`
	ContentHidden    bool     `json:"content_hidden,omitempty"` // Bifrost served no transcript for some row; transcript signals are unknown
}
