package schemas

import "time"

// Failure inbox area (bench/traceinbox, handlers/inbox.go,
// docs/failure-inbox.md): production failures pulled from observability
// tools, deduplicated by signature, and turned into task drafts by a person.
// The key lists in handlers/testdata/contract/Inbox*.keys pin these types to
// the UI's.

// InboxItem is one failure seen in production, with every occurrence of it.
type InboxItem struct {
	ID             string              `json:"id"`
	State          string              `json:"state"`       // new | accepted | rejected | duplicate | snoozed
	Source         string              `json:"source"`      // name of the source that first saw it
	SourceKind     string              `json:"source_kind"` // langfuse | langsmith | bifrost | otlp_dir | webhook
	TraceID        string              `json:"trace_id"`    // the first occurrence's trace id (redacted)
	Link           string              `json:"link,omitempty"`
	Title          string              `json:"title"` // the first user request on one line (redacted)
	Flagged        []string            `json:"flagged"`
	Signature      string              `json:"signature"`
	SignatureParts InboxSignatureParts `json:"signature_parts"`
	Occurrences    int                 `json:"occurrences"`
	FirstSeen      time.Time           `json:"first_seen"`
	LastSeen       time.Time           `json:"last_seen"`
	Traces         []InboxTraceRef     `json:"traces"`
	SuggestedKind  string              `json:"suggested_kind"`
	SuggestedSuite string              `json:"suggested_suite,omitempty"`
	Tools          []string            `json:"tools,omitempty"`
	Model          string              `json:"model,omitempty"`
	// Agent is the agent the trace names (fix wave M7-12): the OTLP span's
	// gen_ai.agent.name or the resource's service.name, set at ingest.
	// Items stored before it, and traces that name none, have no agent.
	Agent   string `json:"agent,omitempty"`
	Outcome string `json:"outcome"`
	// Golden: a passing session sampled to become a regression golden; its
	// accept form starts from "the recorded session was right".
	Golden         bool                `json:"golden,omitempty"`
	Redacted       map[string]int      `json:"redacted,omitempty"` // values scrubbed, by kind; never the values
	Task           *InboxTaskLink      `json:"task,omitempty"`
	Recurrences    int                 `json:"recurrences,omitempty"` // occurrences seen after the item was accepted
	LastRecurrence *time.Time          `json:"last_recurrence,omitempty"`
	Release        *InboxReleaseStatus `json:"release,omitempty"`
	Reason         string              `json:"reason,omitempty"`
	DuplicateOf    string              `json:"duplicate_of,omitempty"`
	SnoozedUntil   *time.Time          `json:"snoozed_until,omitempty"`
	Updated        time.Time           `json:"updated"`
}

// InboxSignatureParts are the three parts a failure signature hashes.
type InboxSignatureParts struct {
	Request string `json:"request"`         // sketch of the first user request's word shingles
	Tool    string `json:"tool,omitempty"`  // the first failing tool
	Error   string `json:"error,omitempty"` // the normalized error line, or the flag kind when nothing errored
}

// InboxTraceRef is one occurrence of an item.
type InboxTraceRef struct {
	ID     string    `json:"id"`
	Source string    `json:"source"`
	At     time.Time `json:"at"`
	Link   string    `json:"link,omitempty"`
}

// InboxTaskLink is the task draft an accepted item became.
type InboxTaskLink struct {
	Suite      string    `json:"suite"`
	ID         string    `json:"id"`
	Dir        string    `json:"dir"`
	AcceptedAt time.Time `json:"accepted_at"`
	Admitted   bool      `json:"admitted"`
}

// InboxReleaseStatus is how an accepted item's task did in the latest
// experiment that ran it.
type InboxReleaseStatus struct {
	Status     string     `json:"status"` // passes | fails | not_run
	Experiment string     `json:"experiment,omitempty"`
	At         *time.Time `json:"at,omitempty"`
	Passed     int        `json:"passed"`
	Runs       int        `json:"runs"`
}

// InboxSourceStatus is one configured source and its last pull.
type InboxSourceStatus struct {
	Name      string     `json:"name"`
	Kind      string     `json:"kind"`
	LastPull  *time.Time `json:"last_pull,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
	LastError string     `json:"last_error,omitempty"`
	Pulled    int        `json:"pulled,omitempty"`
}

// InboxSuite is a suite an item can be accepted into.
type InboxSuite struct {
	ID       string `json:"id"`
	Template string `json:"template"`
}

// InboxList is GET /inbox.
type InboxList struct {
	Items           []InboxItem         `json:"items"`
	Counts          map[string]int      `json:"counts"`
	Sources         []InboxSourceStatus `json:"sources"`
	Suites          []InboxSuite        `json:"suites"`
	IngestEnabled   bool                `json:"ingest_enabled"`
	RegressionTasks int                 `json:"regression_tasks"` // tasks under the benchmarks folder with provenance.incident
}

// InboxItemDetail is GET /inbox/{id}.
type InboxItemDetail struct {
	Item    InboxItem `json:"item"`
	Preview string    `json:"preview"` // redacted transcript preview, plain text
	// Session is what the accept form shows to say what should have
	// happened: the redacted requests, tool calls and final answer.
	Session *InboxSession `json:"session,omitempty"`
}

// InboxSession is the redacted session of an item, for the accept form.
type InboxSession struct {
	Requests    []string           `json:"requests"`
	Calls       []InboxSessionCall `json:"calls"`
	FinalAnswer string             `json:"final_answer,omitempty"`
}

// InboxSessionCall is one recorded tool call: its arguments as JSON text,
// and its result cut short.
type InboxSessionCall struct {
	Seq     int    `json:"seq"`
	Tool    string `json:"tool"`
	Args    string `json:"args"`
	Result  string `json:"result,omitempty"`
	IsError bool   `json:"is_error"`
}

// InboxAcceptRequest is POST /inbox/{id}/accept.
type InboxAcceptRequest struct {
	Suite  string `json:"suite"`
	Kind   string `json:"kind,omitempty"`
	TaskID string `json:"task_id,omitempty"`
	// Guide is the "what should have happened" form: with it the task is
	// written finished, ready for its readiness check, instead of as a
	// draft with EDIT ME markers.
	Guide *InboxGuide `json:"guide,omitempty"`
	// SwitchGrader moves the benchmark to grader checks, which a guided
	// tool-use or mcp task needs; its other tasks keep their verifier.
	SwitchGrader bool `json:"switch_grader,omitempty"`
	// Judge pins the reply judge a guide's reply rubric needs.
	Judge *InboxJudge `json:"judge,omitempty"`
}

// InboxGuide says what should have happened in a recorded session.
type InboxGuide struct {
	// Instruction replaces the first user request; empty keeps it as sent.
	Instruction string `json:"instruction,omitempty"`
	// KeepTurns adds the user's later messages to the instruction, in order.
	KeepTurns bool `json:"keep_turns,omitempty"`
	// UseRecorded: the recorded answer or tool calls were right (a golden).
	UseRecorded bool `json:"use_recorded,omitempty"`
	// qa and memory: the right answer (one fact per entry) and its match
	// (exact, contains_all, regex, numeric, choice); wrong answers that must
	// fail, and whether the recorded answer is one.
	Answers       []string `json:"answers,omitempty"`
	Match         string   `json:"match,omitempty"`
	WrongAnswers  []string `json:"wrong_answers,omitempty"`
	RecordedWrong bool     `json:"recorded_wrong,omitempty"`
	// tool-use and mcp: the calls a correct agent makes, the calls it must
	// never make, whether the calls must happen in order, and a judged
	// rubric for the final reply.
	Calls       []InboxGuideCall `json:"calls,omitempty"`
	Forbidden   []InboxGuideCall `json:"forbidden,omitempty"`
	Ordered     bool             `json:"ordered,omitempty"`
	ReplyRubric string           `json:"reply_rubric,omitempty"`
}

// InboxGuideCall is one tool call of a guide; Args must be a subset of the
// call's arguments.
type InboxGuideCall struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args,omitempty"`
}

// InboxJudge pins the judge of a reply rubric.
type InboxJudge struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// InboxBulkAcceptRequest is POST /inbox/accept: several items accepted into
// one benchmark with one shared guide (for example one reply rubric, or
// "the recorded sessions were right" for goldens).
type InboxBulkAcceptRequest struct {
	IDs          []string    `json:"ids"`
	Suite        string      `json:"suite"`
	Kind         string      `json:"kind,omitempty"`
	Guide        *InboxGuide `json:"guide,omitempty"`
	SwitchGrader bool        `json:"switch_grader,omitempty"`
	Judge        *InboxJudge `json:"judge,omitempty"`
}

// InboxBulkAcceptResult answers a bulk accept: each item accepted or why not.
type InboxBulkAcceptResult struct {
	Accepted []InboxAcceptResult `json:"accepted"`
	Failed   []InboxBulkFailure  `json:"failed"`
}

// InboxBulkFailure is one item a bulk accept could not accept.
type InboxBulkFailure struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

// InboxRejectRequest is POST /inbox/{id}/reject.
type InboxRejectRequest struct {
	Reason      string `json:"reason"`
	DuplicateOf string `json:"duplicate_of,omitempty"`
}

// InboxSnoozeRequest is POST /inbox/{id}/snooze.
type InboxSnoozeRequest struct {
	Days int `json:"days"`
}

// InboxDraft is the task draft an accept wrote.
type InboxDraft struct {
	Task     string   `json:"task"` // suite/id
	Dir      string   `json:"dir"`
	Template string   `json:"template"`
	EditMe   []string `json:"edit_me"`
	// Finished: a guided accept wrote every file; only the readiness check is left.
	Finished bool     `json:"finished"`
	Next     []string `json:"next"`
	Warnings []string `json:"warnings,omitempty"`
}

// InboxAcceptResult answers an accept.
type InboxAcceptResult struct {
	Item  InboxItem  `json:"item"`
	Draft InboxDraft `json:"draft"`
}

// InboxActionResult answers a reject or a snooze.
type InboxActionResult struct {
	Item InboxItem `json:"item"`
}

// InboxIngestResult answers a webhook push.
type InboxIngestResult struct {
	ItemID      string `json:"item_id"`
	Created     bool   `json:"created"`
	Occurrences int    `json:"occurrences"`
	// Skipped counts passing sessions not kept because the source's daily
	// golden sample (golden_per_day) was full.
	Skipped int `json:"skipped,omitempty"`
}
