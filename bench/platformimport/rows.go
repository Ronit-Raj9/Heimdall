package platformimport

import (
	"encoding/json"
	"strings"

	"github.com/maximhq/bifrost/bench/dataset"
)

// ToRow maps one record to a benchmark row. A dataset record's expected
// output becomes the expected answer; a trace's output becomes the
// reference answer (what the agent said then, for a judge to compare
// against). A chat input stays a conversation. The platform, the record id
// and its version go into metadata so every row can be traced back.
func ToRow(rec Record, kind, platform string, m Mapping) dataset.Row {
	in := rec.Input
	if m.Input != "" {
		in = Pick(rec.Input, m.Input)
	}
	var row dataset.Row
	if m.Input == "" {
		msgs := Messages(in)
		if len(msgs) > 0 && msgs[len(msgs)-1]["role"] == "user" {
			for _, x := range msgs {
				row.Messages = append(row.Messages, dataset.Message{Role: x["role"], Content: x["content"]})
			}
		}
	}
	if len(row.Messages) == 0 {
		row.Input = strings.TrimSpace(Text(in, inputKeys))
	}
	out := rec.Expected
	if kind == KindTraces {
		out = rec.Output
	}
	if m.Expected != "" {
		out = Pick(out, m.Expected)
	}
	if m.Rubric != "" {
		row.Rubric = strings.TrimSpace(Text(Pick(rec.Expected, m.Rubric), nil))
	}
	if s := strings.TrimSpace(Text(out, outputKeys)); s != "" && s != "null" {
		if kind == KindTraces {
			row.Reference = s
		} else {
			row.Expected = dataset.Strings{s}
		}
	}
	if kind == KindTraces {
		row.Title = rec.Name
	}
	row.Metadata = FlatMetadata(rec.Metadata, dataset.MaxMetadataKeys-3)
	if row.Metadata == nil {
		row.Metadata = map[string]any{}
	}
	row.Metadata["source_platform"] = platform
	if rec.ID != "" {
		row.Metadata["source_id"] = rec.ID
	}
	if rec.Version != "" {
		row.Metadata["source_version"] = rec.Version
	}
	return row
}

// ChatTrace is a trace as the failure inbox's chat format reads it: the
// conversation, the agent's reply last, and the error when it failed.
func ChatTrace(rec Record) json.RawMessage {
	msgs := Messages(rec.Input)
	if len(msgs) == 0 {
		if s := strings.TrimSpace(Text(rec.Input, inputKeys)); s != "" {
			msgs = append(msgs, map[string]string{"role": "user", "content": s})
		}
	}
	if outMsgs := Messages(rec.Output); len(outMsgs) > 0 {
		for _, x := range outMsgs {
			if x["role"] == "assistant" {
				msgs = append(msgs, x)
			}
		}
	} else if s := strings.TrimSpace(Text(rec.Output, outputKeys)); s != "" && s != "null" {
		msgs = append(msgs, map[string]string{"role": "assistant", "content": s})
	}
	obj := map[string]any{"id": rec.ID, "messages": msgs}
	if rec.Error != "" {
		obj["status"] = "error"
		obj["error"] = rec.Error
	}
	b, _ := json.Marshal(obj)
	return b
}
