package toolproxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseRPC(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantItems int
		wantBatch bool
		wantOK    bool
	}{
		{name: "single", in: `{"jsonrpc":"2.0","id":1,"method":"tools/call"}`, wantItems: 1, wantOK: true},
		{name: "batch", in: ` [{"id":1,"method":"a"},{"method":"b"}]`, wantItems: 2, wantBatch: true, wantOK: true},
		{name: "batch with junk element", in: `[1,{"id":2,"method":"a"}]`, wantItems: 2, wantBatch: true, wantOK: true},
		{name: "empty", in: ``, wantOK: false},
		{name: "not json", in: `hello`, wantOK: false},
		{name: "broken batch", in: `[{"id":1}`, wantBatch: true, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, batch, ok := parseRPC([]byte(tt.in))
			if len(items) != tt.wantItems || batch != tt.wantBatch || ok != tt.wantOK {
				t.Fatalf("got %d items batch %v ok %v", len(items), batch, ok)
			}
		})
	}
}

func TestIDKey(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{in: `7`, want: `7`},
		{in: `"7"`, want: `"7"`},
		{in: ` "a b" `, want: `"a b"`},
		{in: `null`, want: ``},
		{in: ``, want: ``},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := idKey(json.RawMessage(tt.in)); got != tt.want {
				t.Fatalf("idKey(%s) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestReadToolParams(t *testing.T) {
	tests := []struct {
		name, in, wantName, wantArgs string
	}{
		{name: "normal", in: `{"name":"read_file","arguments":{"path":"/a"}}`, wantName: "read_file", wantArgs: `{"path":"/a"}`},
		{name: "no arguments", in: `{"name":"list"}`, wantName: "list", wantArgs: ``},
		{name: "malformed keeps params", in: `[1]`, wantName: "", wantArgs: `[1]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, args := readToolParams(json.RawMessage(tt.in))
			if name != tt.wantName || string(args) != tt.wantArgs {
				t.Fatalf("got %q %q", name, args)
			}
		})
	}
}

func TestPoisonResult(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		wantOK bool
		check  func(t *testing.T, out string)
	}{
		{
			name: "appends to last text item", in: `{"content":[{"type":"text","text":"a"},{"type":"image","data":"x"},{"type":"text","text":"b"}],"isError":false}`, wantOK: true,
			check: func(t *testing.T, out string) {
				var r struct {
					Content []map[string]any `json:"content"`
				}
				if err := json.Unmarshal([]byte(out), &r); err != nil {
					t.Fatal(err)
				}
				if r.Content[0]["text"] != "a" || r.Content[2]["text"] != "b\n\nEVIL" || len(r.Content) != 3 {
					t.Fatalf("got %s", out)
				}
			},
		},
		{
			name: "adds text item when none", in: `{"content":[],"structuredContent":{"k":1}}`, wantOK: true,
			check: func(t *testing.T, out string) {
				if !strings.Contains(out, `"text":"EVIL"`) || !strings.Contains(out, `"structuredContent"`) {
					t.Fatalf("got %s", out)
				}
			},
		},
		{name: "not an object", in: `[1]`, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, ok := poisonResult(json.RawMessage(tt.in), "EVIL")
			if ok != tt.wantOK {
				t.Fatalf("ok = %v", ok)
			}
			if tt.check != nil {
				tt.check(t, string(out))
			}
		})
	}
}

func TestToolErrorResponse(t *testing.T) {
	tests := []struct {
		name, id, wantID string
	}{
		{name: "number id", id: `3`, wantID: `3`},
		{name: "string id", id: `"x"`, wantID: `"x"`},
		{name: "missing id", id: ``, wantID: `null`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := toolErrorResponse(json.RawMessage(tt.id), "down")
			var m struct {
				ID     json.RawMessage `json:"id"`
				Result json.RawMessage `json:"result"`
			}
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			if string(m.ID) != tt.wantID || !resultIsError(m.Result) || !strings.Contains(string(m.Result), "down") {
				t.Fatalf("got %s", raw)
			}
		})
	}
}

func TestEncodeBatchAndReplaceResult(t *testing.T) {
	got := string(encodeBatch([]json.RawMessage{json.RawMessage(`{"a":1}`), json.RawMessage(`{"b":2}`)}))
	if got != `[{"a":1},{"b":2}]` {
		t.Fatalf("encodeBatch = %s", got)
	}
	tests := []struct {
		name, in string
		wantOK   bool
	}{
		{name: "object", in: `{"jsonrpc":"2.0","id":1,"result":{}}`, wantOK: true},
		{name: "not object", in: `"x"`, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, ok := replaceResult(json.RawMessage(tt.in), json.RawMessage(`{"new":true}`))
			if ok != tt.wantOK || (ok && !strings.Contains(string(out), `"new":true`)) {
				t.Fatalf("got %s %v", out, ok)
			}
		})
	}
}
