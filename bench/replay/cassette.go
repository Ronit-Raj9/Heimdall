package replay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Run folder files a cassette is built from.
const (
	RawFile    = "gateway.raw.jsonl"  // evidence/gateway.go rawRequestsFile
	LedgerFile = "proxy.ledger.jsonl" // evidence/proxyledger.go ledgerFile
)

// Reasons an entry carries no body (CassetteEntry.Missing).
const (
	MissingFault    = "fault"     // the proxy injected a fault on the call; replayed as that status
	MissingRejected = "rejected"  // the proxy refused the call
	MissingNoBody   = "no_body"   // the gateway stored no response body for the call
	MissingNoRecord = "no_record" // no gateway row matched the call
)

// ErrNothingToReplay is returned when the run recorded no model call.
var ErrNothingToReplay = errors.New("the run recorded no model call to replay")

// RawCall is one gateway row as gateway.raw.jsonl keeps it, decoded.
type RawCall struct {
	RequestID string
	Request   string // raw_request: the body Bifrost sent upstream
	Response  string // raw_response: the provider's body, or the stored stream
	Stream    bool
	Model     string
	Status    int // 200 for a successful row, else the error status (500 when unknown)
	Available bool
}

// ParseRaw decodes gateway.raw.jsonl. Lines that do not parse are skipped.
func ParseRaw(data []byte) []RawCall {
	var out []RawCall
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 64<<20)
	for sc.Scan() {
		var line struct {
			RequestID    string          `json:"request_id"`
			RawAvailable bool            `json:"raw_available"`
			RawDetail    json.RawMessage `json:"raw_detail"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.RequestID == "" {
			continue
		}
		c := RawCall{RequestID: line.RequestID, Available: line.RawAvailable, Status: 200}
		var d struct {
			RawRequest   string `json:"raw_request"`
			RawResponse  string `json:"raw_response"`
			Stream       bool   `json:"stream"`
			Model        string `json:"model"`
			Status       string `json:"status"`
			ErrorDetails *struct {
				StatusCode *int `json:"status_code"`
			} `json:"error_details"`
		}
		if len(line.RawDetail) > 0 && json.Unmarshal(line.RawDetail, &d) == nil {
			c.Request, c.Response, c.Stream, c.Model = d.RawRequest, d.RawResponse, d.Stream, d.Model
			if d.Status == "error" {
				c.Status = 500
				if d.ErrorDetails != nil && d.ErrorDetails.StatusCode != nil && *d.ErrorDetails.StatusCode >= 400 {
					c.Status = *d.ErrorDetails.StatusCode
				}
			}
		}
		out = append(out, c)
	}
	return out
}

// ParseLedger decodes a run's proxy.ledger.jsonl. Lines that do not parse
// are skipped.
func ParseLedger(data []byte) []schemas.LedgerEntry {
	var out []schemas.LedgerEntry
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		var e schemas.LedgerEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// Build makes the cassette of run runID from its ledger and raw gateway
// rows. Each conversation call of the ledger becomes one entry, in call
// order; its response is the gateway row whose raw request has the
// ledger's request hash, else the next row no ledger line names (a row
// whose stored body was redacted no longer hashes the same). Without a
// ledger the rows are taken in order. A response body larger than
// schemas.MaxCassetteBytes in total is an error.
func Build(runID string, ledger []schemas.LedgerEntry, raw []RawCall) (*schemas.Cassette, error) {
	c := &schemas.Cassette{SourceRun: runID, Entries: []schemas.CassetteEntry{}}
	var calls []schemas.LedgerEntry
	for _, e := range ledger {
		if e.Kind == "message" && e.Call > 0 && (runID == "" || e.RunID == "" || e.RunID == runID) {
			calls = append(calls, e)
		}
	}
	sort.SliceStable(calls, func(i, j int) bool { return calls[i].Call < calls[j].Call })
	if len(calls) == 0 {
		for i, r := range raw {
			c.Entries = append(c.Entries, fromRow(i+1, r.Status, r.Stream, &r))
		}
	} else {
		bySHA := map[string][]int{}
		named := map[string]bool{}
		for _, e := range calls {
			named[e.ReqSHA256] = true
		}
		var spare []int
		for i, r := range raw {
			sha := ""
			if r.Request != "" {
				sha = utils.SHA256Bytes([]byte(r.Request))
			}
			if sha != "" && named[sha] {
				bySHA[sha] = append(bySHA[sha], i)
			} else {
				spare = append(spare, i)
			}
		}
		for _, e := range calls {
			switch e.Action {
			case "inject":
				c.Entries = append(c.Entries, schemas.CassetteEntry{Call: e.Call, Stream: e.Stream, Status: e.Status, Missing: MissingFault})
				continue
			case "reject":
				c.Entries = append(c.Entries, schemas.CassetteEntry{Call: e.Call, Stream: e.Stream, Status: e.Status, Missing: MissingRejected})
				continue
			}
			idx := -1
			if q := bySHA[e.ReqSHA256]; len(q) > 0 {
				idx = q[0]
				if len(q) > 1 { // identical requests sent twice keep their own rows; the last one serves retries
					bySHA[e.ReqSHA256] = q[1:]
				}
			} else if len(spare) > 0 {
				idx, spare = spare[0], spare[1:]
			}
			status := e.Status
			if status == 0 {
				status = 200
			}
			if idx < 0 {
				c.Entries = append(c.Entries, schemas.CassetteEntry{Call: e.Call, Stream: e.Stream, Status: status, Missing: MissingNoRecord})
				continue
			}
			c.Entries = append(c.Entries, fromRow(e.Call, status, e.Stream, &raw[idx]))
		}
	}
	if len(c.Entries) == 0 {
		return nil, ErrNothingToReplay
	}
	n := 0
	for _, e := range c.Entries {
		n += len(e.Body)
	}
	if n > schemas.MaxCassetteBytes {
		return nil, fmt.Errorf("the run's model responses hold %d bytes, more than the %d a replay carries", n, schemas.MaxCassetteBytes)
	}
	return c, nil
}

// fromRow is the entry for call answered by row r with status.
func fromRow(call, status int, stream bool, r *RawCall) schemas.CassetteEntry {
	e := schemas.CassetteEntry{Call: call, ReqHash: RequestHash([]byte(r.Request)), Model: r.Model, Stream: stream || r.Stream, Status: status}
	if strings.TrimSpace(r.Response) == "" {
		e.Missing = MissingNoBody
		return e
	}
	if e.Stream {
		e.Body = strings.Join(StreamPayloads(r.Response), "\n\n")
	} else {
		e.Body = r.Response
	}
	return e
}

// Usable reports whether e can answer a call: it holds a body, or it is a
// recorded error that replays as its status.
func Usable(e schemas.CassetteEntry) bool {
	return e.Body != "" || e.Missing == MissingFault || e.Missing == MissingRejected || (e.Status >= 400 && e.Missing != MissingNoRecord)
}

// StreamPayloads returns the payload of every stored stream chunk with SSE
// framing stripped; the OpenAI terminator "[DONE]" is dropped. A body
// stored as bare JSON lines (no blank-line framing) gives one payload per
// line.
func StreamPayloads(raw string) []string {
	var out []string
	chunks := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n\n")
	if len(chunks) == 1 && !strings.Contains(raw, "data:") {
		chunks = strings.Split(raw, "\n")
	}
	for _, chunk := range chunks {
		var data []string
		for _, line := range strings.Split(strings.TrimSpace(chunk), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case line == "", strings.HasPrefix(line, "event:"), strings.HasPrefix(line, ":"):
			case strings.HasPrefix(line, "data:"):
				data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			default:
				data = append(data, line)
			}
		}
		if p := strings.Join(data, "\n"); p != "" && p != "[DONE]" {
			out = append(out, p)
		}
	}
	return out
}

// EventType is the "type" field of one stream payload, or "".
func EventType(payload string) string {
	var ev struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(payload), &ev) != nil {
		return ""
	}
	return ev.Type
}
