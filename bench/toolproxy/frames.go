package toolproxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Screen replay: every image an MCP tool call returns to the agent (a
// computer-use or browser screenshot) is saved next to the ledger as
// frames/NNNN.png (or .jpg) with one schemas.FrameRecord line in
// frames/frames.jsonl. Each frame is labelled with the last acting call
// before it (click, type, key, navigate...). Saving is best effort: a
// failure is logged and never touches the call or the ledger.

// maxFrameCount caps the frames saved in one run.
const maxFrameCount = 2000

// maxFrameImage caps one saved frame.
const maxFrameImage = 16 << 20

// maxActionLabel caps the action label kept on a frame.
const maxActionLabel = 200

type frameRecorder struct {
	mu     sync.Mutex
	dir    string // "" disables saving
	n      int
	action string // the last non-image call, as a label
}

func newFrameRecorder(ledgerPath string) *frameRecorder {
	if ledgerPath == "" {
		return &frameRecorder{}
	}
	f := &frameRecorder{dir: filepath.Join(filepath.Dir(ledgerPath), schemas.FramesDir)}
	// A restarted proxy continues numbering after the frames already saved.
	if b, err := os.ReadFile(filepath.Join(f.dir, schemas.FramesIndexFile)); err == nil {
		f.n = bytes.Count(b, []byte("\n"))
	}
	return f
}

// mcpImages returns the decoded images of an MCP tools/call result: content
// blocks of type image with base64 data.
func mcpImages(result []byte) []struct {
	data []byte
	mime string
} {
	var r struct {
		Content []struct {
			Type     string `json:"type"`
			Data     string `json:"data"`
			MimeType string `json:"mimeType"`
		} `json:"content"`
	}
	if len(result) == 0 || json.Unmarshal(result, &r) != nil {
		return nil
	}
	var out []struct {
		data []byte
		mime string
	}
	for _, c := range r.Content {
		if c.Type != "image" || c.Data == "" {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(c.Data)
		if err != nil || len(b) == 0 || len(b) > maxFrameImage {
			continue
		}
		out = append(out, struct {
			data []byte
			mime string
		}{b, c.MimeType})
	}
	return out
}

// record looks at one committed tool call row: a call whose full result
// carries images saves each as a frame; any other tool call becomes the
// action label of the next frame.
func (f *frameRecorder) record(row *schemas.ToolCall, full []byte) {
	if f == nil || f.dir == "" || row == nil || row.Kind != "" {
		return
	}
	imgs := mcpImages(full)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(imgs) == 0 {
		if !row.IsError && row.Tool != "tools/list" {
			label := row.Tool
			if row.Arguments != "" && row.Arguments != "{}" && row.Arguments != "null" {
				label += " " + row.Arguments
			}
			if len(label) > maxActionLabel {
				label = label[:maxActionLabel] + "..."
			}
			f.action = label
		}
		return
	}
	for _, img := range imgs {
		if f.n >= maxFrameCount {
			return
		}
		ext, mime := ".png", "image/png"
		if len(img.data) > 2 && img.data[0] == 0xFF && img.data[1] == 0xD8 {
			ext, mime = ".jpg", "image/jpeg"
		} else if len(img.data) < 8 || string(img.data[:8]) != "\x89PNG\r\n\x1a\n" {
			continue
		}
		if err := os.MkdirAll(f.dir, 0o755); err != nil {
			return
		}
		f.n++
		name := fmt.Sprintf("%04d%s", f.n, ext)
		if err := os.WriteFile(filepath.Join(f.dir, name), img.data, 0o644); err != nil {
			continue
		}
		rec := schemas.FrameRecord{File: name, Seq: row.Seq, Time: row.Started, Tool: row.Tool, Action: f.action, Mime: mime}
		line, _ := json.Marshal(rec)
		fh, err := os.OpenFile(filepath.Join(f.dir, schemas.FramesIndexFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			continue
		}
		_, _ = fh.Write(append(line, '\n'))
		_ = fh.Close()
	}
	f.action = ""
}
