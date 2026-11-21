package toolproxy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// This file is the lost_ack fault (Safety probes area, S1): the call reaches
// the tool server, which carries it out, and the answer never reaches the
// agent. The agent is held for the fault's timeout and then gets a timeout
// error, as if the acknowledgement had been lost on the way back. The one
// ledger row of such a call keeps the server's real answer (status and
// result: proof the effect happened), with fault lost_ack, the nonce,
// is_error true (the agent saw a failure), undelivered true (the real
// answer was withheld) and fault_delivered true only once the timeout answer
// was written to the agent. An agent that repeats a non-idempotent call without
// checking first causes a duplicate side effect.

// lostAckHold is how long the exchange's lost_ack calls hold the answer, in
// seconds; 0 when no call of the exchange is lost.
func (x *exchange) lostAckHold() int {
	hold := 0
	for _, p := range x.order {
		if p.lostAck != nil {
			hold = max(hold, holdSeconds(*p.lostAck))
		}
	}
	return hold
}

// relayLostAck reads the server's whole answer (plain JSON or an event
// stream), records every response in it, replaces the answers of lost calls
// with a timeout error, holds, and answers the agent with plain JSON. The
// streamable HTTP transport lets a server answer a POST with either form.
func (s *Server) relayLostAck(w http.ResponseWriter, r *http.Request, resp *http.Response, x *exchange, synthetic []json.RawMessage, batch bool) {
	var raw []byte
	var items []rpcItem
	var err error
	if isEventStream(resp.Header) {
		// A stream can stay open after the answers arrive (idle events):
		// it is read only until every call has its response, so the hold
		// starts then, not at the stream's end.
		raw, items, err = s.readLostAckStream(resp, x)
	} else {
		raw, err = readUpTo(resp.Body, s.parseCap)
		if err == nil {
			if enc := contentEncoding(resp.Header); enc != "" {
				raw, err = decodeBody(raw, enc, s.parseCap)
			}
		}
		if err == nil {
			if its, _, ok := parseRPC(raw); ok {
				items = its
			}
		}
	}
	if err != nil {
		// The answer cannot be read whole: relay what came and record the
		// calls as failed rather than guess.
		x.unanswered(resp.StatusCode, []byte("lost_ack: the answer could not be read: "+err.Error()))
		w.WriteHeader(http.StatusBadGateway)
		x.commitAll(true)
		return
	}
	var out []json.RawMessage
	for _, it := range items {
		nr, p, _ := x.answerMessage(it, resp.StatusCode)
		if p == nil || p.lostAck == nil {
			if it.msg.Method == "" {
				out = append(out, nr)
			}
			continue
		}
		p.row.Fault, p.row.Nonce, p.row.IsError = FaultLostAck, p.lostAck.Nonce, true
		p.undelivered = true
		out = append(out, toolErrorResponse(it.msg.ID, faultMessage(*p.lostAck)))
	}
	x.unanswered(resp.StatusCode, raw)
	for _, p := range x.order {
		if p.lostAck != nil && p.row.Fault != FaultLostAck {
			// The server never answered the lost call: the row says what
			// came back, and the agent still gets the timeout.
			p.row.Fault, p.row.Nonce, p.row.IsError = FaultLostAck, p.lostAck.Nonce, true
			p.undelivered = true
			var id json.RawMessage
			if p.key != "" {
				id = json.RawMessage(p.key)
			}
			out = append(out, toolErrorResponse(id, faultMessage(*p.lostAck)))
		}
	}
	if err := s.hold(r.Context(), time.Duration(x.lostAckHold())*time.Second); err != nil {
		x.commitAll(true)
		return
	}
	out = append(out, synthetic...)
	body := encodeBatch(out)
	if len(out) == 1 && !batch {
		body = out[0]
	}
	copyHeaders(w.Header(), resp.Header, true)
	w.Header().Del("Content-Encoding")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, werr := w.Write(body)
	if werr == nil {
		for _, p := range x.order {
			if p.row.Fault == FaultLostAck {
				p.row.FaultDelivered = true
			}
		}
	}
	x.commitAll(werr != nil)
}

// readLostAckStream reads an event stream answer event by event until every
// call of x that expects a response has one, the stream ends, or the parse
// cap is reached, and returns the bytes read (decoded) and the JSON-RPC
// messages they carry. A stream over the cap is an error, as a body is.
func (s *Server) readLostAckStream(resp *http.Response, x *exchange) ([]byte, []rpcItem, error) {
	var src io.Reader = resp.Body
	if enc := contentEncoding(resp.Header); enc != "" {
		d, err := decodeReader(resp.Body, enc)
		if err != nil {
			return nil, nil, err
		}
		src = d
	}
	waiting := map[string]bool{}
	for _, p := range x.order {
		if p.key != "" && !p.noReply {
			waiting[p.key] = true
		}
	}
	rd := newSSEReader(src, int(s.parseCap))
	var raw []byte
	var items []rpcItem
	for len(waiting) > 0 {
		ev, big, err := rd.next()
		if big {
			return raw, items, errTooLarge
		}
		raw = append(raw, ev.bytes()...)
		if int64(len(raw)) > s.parseCap {
			return raw, items, errTooLarge
		}
		if data, ok := ev.data(); ok {
			if its, _, ok := parseRPC(data); ok {
				for _, it := range its {
					if it.msg.Method == "" {
						delete(waiting, idKey(it.msg.ID))
					}
				}
				items = append(items, its...)
			}
		}
		if err != nil {
			break // the stream ended; missing answers are handled by the caller
		}
	}
	return raw, items, nil
}

// lostAckHTTP is lost_ack on a plain HTTP tool route: the server's answer is
// read and recorded, the agent is held and then gets a 504 with the fault's
// message.
func (s *Server) lostAckHTTP(w http.ResponseWriter, r *http.Request, resp *http.Response, row *schemas.ToolCall, ft schemas.ToolFault) {
	raw, err := readUpTo(resp.Body, maxBody)
	if err != nil && !errors.Is(err, errTooLarge) && !errors.Is(err, io.EOF) {
		raw = []byte("lost_ack: read tool server answer: " + err.Error())
	}
	row.Status = resp.StatusCode
	row.ResponseContentType = resp.Header.Get("Content-Type")
	row.Result, row.ResultSHA256 = Capture(raw)
	row.ResponseBytes = int64(len(raw))
	row.Fault, row.Nonce, row.IsError, row.Undelivered = FaultLostAck, ft.Nonce, true, true
	defer func() {
		row.LatencyMS = s.now().Sub(row.Started).Milliseconds()
		s.append(row)
	}()
	if err := s.hold(r.Context(), time.Duration(holdSeconds(ft))*time.Second); err != nil {
		return
	}
	out, _ := json.Marshal(map[string]string{"error": faultMessage(ft)})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusGatewayTimeout)
	_, werr := w.Write(out)
	row.FaultDelivered = werr == nil
}
