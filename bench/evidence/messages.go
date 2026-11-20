package evidence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// mailboxRecord is one line of the mailbox service's own record
// (state/mailbox/messages.jsonl, docs/multi-agent.md).
type mailboxRecord struct {
	Seq       int             `json:"seq"`
	ID        json.RawMessage `json:"id"`
	From      string          `json:"from"`
	Sender    string          `json:"sender"`
	To        string          `json:"to"`
	Body      string          `json:"body"`
	Fields    map[string]any  `json:"fields"`
	At        time.Time       `json:"at"`
	Planted   bool            `json:"planted"`
	Fault     string          `json:"fault"`
	Delivered *string         `json:"delivered"`
	ReadBy    []string        `json:"read_by"`
	ReadAt    json.RawMessage `json:"read_at"`
	// ReceivedBy is the text each reader got (newer mailbox records).
	ReceivedBy map[string]string `json:"received_by"`
	Nonce      string            `json:"nonce"`
}

// mailboxMessage is a message as a send body or a receive answer carries it.
type mailboxMessage struct {
	ID     json.RawMessage `json:"id"`
	From   string          `json:"from"`
	To     string          `json:"to"`
	Body   string          `json:"body"`
	Agent  string          `json:"agent"`
	Fields map[string]any  `json:"fields"`
}

// BuildMessages derives a team run's mailbox messages from the tool proxy
// ledger and the mailbox service's own record. The record is the reference
// for what was stored and what each reader got (faults, planted messages);
// the ledger adds the proven sender (the member route a send came in on) and
// the rows that carried each message. Without a record the messages come
// from the ledger alone. It is pure.
func BuildMessages(calls []schemas.ToolCall, records []mailboxRecord) []schemas.AgentMessage {
	sends, recvs := mailboxRows(calls)
	sendByID := map[string]schemas.ToolCall{}
	for _, c := range sends {
		var ans mailboxMessage
		if json.Unmarshal([]byte(c.Result), &ans) == nil && idString(ans.ID) != "" {
			sendByID[idString(ans.ID)] = c
		}
	}
	var out []schemas.AgentMessage
	if len(records) > 0 {
		for _, r := range records {
			m := schemas.AgentMessage{ID: idString(r.ID), From: r.From, To: r.To, Body: r.Body, Fields: stringFields(r.Fields),
				SenderAgentID: r.Sender, At: r.At, Fault: r.Fault, Nonce: r.Nonce, DeliveredTo: r.ReadBy}
			if len(r.ReceivedBy) > 0 {
				recordReceived(&m, r)
			} else if r.Delivered != nil && *r.Delivered != r.Body {
				m.Received, m.Altered = *r.Delivered, true
			}
			m.DeliveredAt = firstReadAt(r.ReadAt)
			if c, ok := sendByID[m.ID]; ok {
				m.SendSeq = c.Seq
				if m.SenderAgentID == "" {
					m.SenderAgentID = c.AgentID
				}
			}
			out = append(out, m)
		}
	} else {
		for _, c := range sends {
			var in mailboxMessage
			if json.Unmarshal([]byte(c.Arguments), &in) != nil {
				continue
			}
			var ans mailboxMessage
			_ = json.Unmarshal([]byte(c.Result), &ans) // an answer that is not JSON leaves the id empty
			out = append(out, schemas.AgentMessage{ID: idString(ans.ID), From: in.From, To: in.To, Body: in.Body,
				Fields: extraFields(c.Arguments), SenderAgentID: c.AgentID, At: c.Started, SendSeq: c.Seq, Fault: c.Fault, Nonce: c.Nonce})
		}
	}
	joinReceives(out, recvs)
	for i := range out {
		out[i].Seq = i + 1
		out[i].BodySHA256 = utils.SHA256Bytes([]byte(out[i].Body))
		out[i].Size = int64(len(out[i].Body))
		out[i].Delivered = len(out[i].DeliveredTo) > 0
	}
	return out
}

// mailboxRows are the mailbox's send and receive rows of the ledger, in
// ledger order.
func mailboxRows(calls []schemas.ToolCall) ([]schemas.ToolCall, []schemas.ToolCall) {
	var sends, recvs []schemas.ToolCall
	for _, c := range calls {
		if c.Server != schemas.MailboxServer || !c.IsToolCall() {
			continue
		}
		switch c.Tool {
		case schemas.MailboxSend:
			sends = append(sends, c)
		case schemas.MailboxRecv:
			recvs = append(recvs, c)
		}
	}
	sort.SliceStable(sends, func(i, j int) bool { return sends[i].Seq < sends[j].Seq })
	sort.SliceStable(recvs, func(i, j int) bool { return recvs[i].Seq < recvs[j].Seq })
	return sends, recvs
}

// joinReceives marks which receive rows carried each message, who got it and
// what they got when it differs from what was sent. A receive row a fault
// answered carries its fault and nonce onto every message it held.
func joinReceives(msgs []schemas.AgentMessage, recvs []schemas.ToolCall) {
	byID := map[string]int{}
	for i, m := range msgs {
		if m.ID != "" {
			byID[m.ID] = i
		}
	}
	for _, c := range recvs {
		var in mailboxMessage
		_ = json.Unmarshal([]byte(c.Arguments), &in) // a malformed receive names no reader
		reader := c.AgentID
		if reader == "" {
			reader = in.Agent
		}
		var ans struct {
			Messages []mailboxMessage `json:"messages"`
		}
		if json.Unmarshal([]byte(c.Result), &ans) != nil {
			continue
		}
		for _, got := range ans.Messages {
			i, ok := byID[idString(got.ID)]
			if !ok {
				continue
			}
			m := &msgs[i]
			m.RecvSeqs = append(m.RecvSeqs, c.Seq)
			if reader != "" && !slices.Contains(m.DeliveredTo, reader) {
				m.DeliveredTo = append(m.DeliveredTo, reader)
			}
			if m.DeliveredAt.IsZero() || c.Started.Before(m.DeliveredAt) {
				m.DeliveredAt = c.Started
			}
			if got.Body != m.Body {
				if m.Received == "" {
					m.Received, m.Altered = got.Body, true
				}
				if reader != "" {
					if m.ReceivedBy == nil {
						m.ReceivedBy = map[string]string{}
					}
					if _, seen := m.ReceivedBy[reader]; !seen {
						m.ReceivedBy[reader] = got.Body
					}
				}
			}
			if c.Fault != "" && m.Fault == "" {
				m.Fault, m.Nonce = c.Fault, c.Nonce
			}
		}
	}
}

// readMailboxRecord reads the mailbox service's exported record from the
// run folder; a run without one has none.
func readMailboxRecord(dir string) ([]mailboxRecord, error) {
	if dir == "" {
		return nil, nil
	}
	p := filepath.Join(dir, "state", schemas.MailboxServer, schemas.MailboxExport)
	return readJSONLFile[mailboxRecord](p)
}

// idString reads a message id that is a JSON string or number.
func idString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// stringFields renders extra message fields as strings: a string as itself,
// anything else as compact JSON.
func stringFields(f map[string]any) map[string]string {
	if len(f) == 0 {
		return nil
	}
	out := make(map[string]string, len(f))
	for k, v := range f {
		if s, ok := v.(string); ok {
			out[k] = s
			continue
		}
		b, err := json.Marshal(v)
		if err != nil {
			b = []byte(fmt.Sprint(v))
		}
		out[k] = string(b)
	}
	return out
}

// extraFields are the keys of a send body other than from, to and body.
func extraFields(args string) map[string]string {
	var all map[string]any
	if json.Unmarshal([]byte(args), &all) != nil {
		return nil
	}
	for _, k := range []string{"from", "to", "body"} {
		delete(all, k)
	}
	return stringFields(all)
}

// firstReadAt reads read_at: one time, or a map of reader to time (the
// earliest wins).
// recordReceived sets a message's per-reader altered text from a record that
// names what each reader got: ReceivedBy holds only the readers whose text
// differs from the body, and Received is the text of the earliest of them
// by read time (reader id breaks ties and orders readers without a time).
func recordReceived(m *schemas.AgentMessage, r mailboxRecord) {
	var at map[string]time.Time
	_ = json.Unmarshal(r.ReadAt, &at) // an old single time names no reader
	var readers []string
	for reader, body := range r.ReceivedBy {
		if body == r.Body {
			continue
		}
		if m.ReceivedBy == nil {
			m.ReceivedBy = map[string]string{}
		}
		m.ReceivedBy[reader] = body
		readers = append(readers, reader)
	}
	if len(readers) == 0 {
		return
	}
	sort.Slice(readers, func(i, j int) bool {
		ti, tj := at[readers[i]], at[readers[j]]
		if !ti.Equal(tj) {
			return tj.IsZero() || (!ti.IsZero() && ti.Before(tj))
		}
		return readers[i] < readers[j]
	})
	m.Received, m.Altered = m.ReceivedBy[readers[0]], true
}

func firstReadAt(raw json.RawMessage) time.Time {
	var t time.Time
	if json.Unmarshal(raw, &t) == nil {
		return t
	}
	var m map[string]time.Time
	if json.Unmarshal(raw, &m) != nil {
		return time.Time{}
	}
	var first time.Time
	for _, v := range m {
		if first.IsZero() || v.Before(first) {
			first = v
		}
	}
	return first
}
