package agentevents

import (
	"encoding/json"
	"strconv"
)

// OTLPResponse builds the ExportTraceServiceResponse for one request. With
// no rejected spans it is the empty message (full success): "{}" in JSON,
// no bytes in protobuf. Otherwise it carries partial_success with the
// rejected count and msg, so the exporter knows spans were lost. In JSON the
// int64 count is a string, as the OTLP JSON mapping requires.
func OTLPResponse(protobuf bool, rejected int, msg string) (contentType string, body []byte) {
	if protobuf {
		if rejected <= 0 {
			return ContentTypeOTLPProto, nil
		}
		var inner []byte
		inner = appendVarintField(inner, 1, uint64(rejected))
		inner = appendBytesField(inner, 2, []byte(msg))
		return ContentTypeOTLPProto, appendBytesField(nil, 1, inner)
	}
	if rejected <= 0 {
		return ContentTypeOTLPJSON, []byte("{}")
	}
	type partial struct {
		RejectedSpans string `json:"rejectedSpans"`
		ErrorMessage  string `json:"errorMessage"`
	}
	raw, _ := json.Marshal(struct {
		PartialSuccess partial `json:"partialSuccess"`
	}{partial{RejectedSpans: strconv.Itoa(rejected), ErrorMessage: msg}})
	return ContentTypeOTLPJSON, raw
}
