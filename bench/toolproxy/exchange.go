package toolproxy

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// pending is one recorded call of an exchange, from the request until its
// ledger row is written.
type pending struct {
	key         string
	row         schemas.ToolCall
	poison      *schemas.ToolFault // a poison fault that takes this call's result
	answered    bool
	committed   bool
	undelivered bool
	noReply     bool // sent without an id: a notification, never answered
}

// exchange is one request to an MCP endpoint and the calls it carries. Each
// call is written to the ledger once, when its answer has been relayed (or
// is known never to come), so the row can say whether the agent got it.
type exchange struct {
	s       *Server
	route   Route
	started time.Time
	byKey   map[string]*pending
	order   []*pending
}

// track starts the record of one call. A duplicate id in one request is
// recorded but only the first is matched to an answer.
func (x *exchange) track(key, tool string, args []byte) *pending {
	p := &pending{key: key, row: schemas.ToolCall{
		Server:   x.route.Service,
		Tool:     tool,
		Protocol: ProtocolMCP,
		Started:  x.started,
	}}
	p.row.Arguments, p.row.ArgsSHA256 = Capture(args)
	if key == "" {
		p.noReply = true
	} else if _, dup := x.byKey[key]; !dup {
		x.byKey[key] = p
	}
	x.order = append(x.order, p)
	return p
}

// answer fills the result side of the row.
func (p *pending) answer(result []byte, isError bool, status int, now time.Time) {
	p.row.Result, p.row.ResultSHA256 = Capture(result)
	p.row.IsError = isError
	p.row.Status = status
	p.row.LatencyMS = now.Sub(p.row.Started).Milliseconds()
	p.answered = true
}

// answerMessage records a response message that answers a tracked call and
// applies the call's poison fault. It returns the message to relay, the call
// it answered (nil for anything else: server requests, notifications,
// unknown ids) and whether the message was rewritten.
func (x *exchange) answerMessage(it rpcItem, status int) (json.RawMessage, *pending, bool) {
	if it.msg.Method != "" {
		return it.raw, nil, false
	}
	key := idKey(it.msg.ID)
	p := x.byKey[key]
	if key == "" || p == nil || p.answered {
		return it.raw, nil, false
	}
	now := x.s.now()
	if e := bytes.TrimSpace(it.msg.Error); len(e) > 0 && string(e) != "null" {
		p.answer(e, true, status, now)
		return it.raw, p, false
	}
	raw, result, rewritten := it.raw, it.msg.Result, false
	if p.poison != nil && len(result) > 0 {
		if np, ok := poisonResult(result, poisonText(*p.poison)); ok {
			if nr, ok := replaceResult(raw, np); ok {
				raw, result, rewritten = nr, np, true
				p.row.Fault, p.row.Nonce = FaultPoison, p.poison.Nonce
			}
		}
	}
	p.answer(result, resultIsError(result), status, now)
	return raw, p, rewritten
}

// answerEvent records every response in one server-sent event and rewrites
// the event's data when a poison fault changed a result.
func (x *exchange) answerEvent(ev *sseEvent, status int) []*pending {
	data, ok := ev.data()
	if !ok {
		return nil
	}
	items, batch, ok := parseRPC(data)
	if !ok {
		return nil
	}
	var done []*pending
	changed := false
	raws := make([]json.RawMessage, len(items))
	for i, it := range items {
		nr, p, rw := x.answerMessage(it, status)
		raws[i] = nr
		if p != nil {
			done = append(done, p)
		}
		changed = changed || rw
	}
	if changed {
		if batch {
			ev.setData(encodeBatch(raws))
		} else {
			ev.setData(raws[0])
		}
	}
	return done
}

// unanswered closes every call that has no answer yet. A notification is
// done when the server accepted the request; a call with an id that got no
// answer failed, and the row keeps what the server sent instead.
func (x *exchange) unanswered(status int, body []byte) {
	now := x.s.now()
	for _, p := range x.order {
		if p.answered {
			continue
		}
		if p.noReply {
			p.answer(nil, status >= 400, status, now)
			continue
		}
		p.answer(body, true, status, now)
	}
}

// abandon closes every open call when the agent went away before the proxy
// could answer.
func (x *exchange) abandon(reason string, undelivered bool) {
	now := x.s.now()
	for _, p := range x.order {
		if !p.answered {
			p.answer([]byte(reason), true, 0, now)
		}
	}
	x.commitAll(undelivered)
}

// commit writes one call's row, once.
func (x *exchange) commit(p *pending) {
	if p.committed {
		return
	}
	p.committed = true
	p.row.Undelivered = p.undelivered
	x.s.append(&p.row)
}

// commitFaulted writes the rows of calls an error or timeout fault answered.
func (x *exchange) commitFaulted(undelivered bool) {
	for _, p := range x.order {
		if p.row.Fault == FaultError || p.row.Fault == FaultTimeout {
			p.undelivered = p.undelivered || undelivered
			x.commit(p)
		}
	}
}

// commitAll writes every row not yet written, in request order.
func (x *exchange) commitAll(undelivered bool) {
	for _, p := range x.order {
		if !p.committed {
			p.undelivered = p.undelivered || undelivered
			x.commit(p)
		}
	}
}
