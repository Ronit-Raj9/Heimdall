package traceimport

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// plantedSecret is an API-key-shaped value the fixtures carry where they say
// PLANTED_SECRET. It is built at run time so no key-shaped string sits in the
// repository.
var plantedSecret = "sk-" + "TESTONLY" + strings.Repeat("x9", 10)

// fixture reads a testdata file with the planted secret filled in.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.ReplaceAll(b, []byte("PLANTED_SECRET"), []byte(plantedSecret))
}

func TestDetect(t *testing.T) {
	tests := []struct {
		file, want string
	}{
		{"otlp-support.json", FormatOTLP},
		{"langfuse-trace.json", FormatLangfuse},
		{"langfuse-observations.json", FormatLangfuse},
		{"chat-transcript.json", FormatChat},
		{"chat-multi.jsonl", FormatChat},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			got, err := Detect(fixture(t, tc.file))
			if err != nil || got != tc.want {
				t.Fatalf("Detect = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	if _, err := Detect([]byte(`{"hello": "world"}`)); err == nil || !strings.Contains(err.Error(), "-format") {
		t.Errorf("unknown shape: err = %v, want a hint to pass -format", err)
	}
}

func TestParseMalformed(t *testing.T) {
	deep := strings.Repeat("[", 20000) + strings.Repeat("]", 20000)
	tests := []struct {
		name, data, format, want string
	}{
		{"truncated", string(fixture(t, "malformed.json")), "", "invalid JSON"},
		{"empty", "  \n", "", "empty"},
		{"not utf8", "\xff\xfe{}", "", "not UTF-8"},
		{"deep nesting", deep, "", "invalid JSON"},
		{"wrong forced format", string(fixture(t, "chat-transcript.json")), FormatOTLP, "resourceSpans"},
		{"unknown format", "{}", "zipkin", "want one of"},
		{"no spans", `{"resourceSpans": []}`, "", "no session"},
		{"scalar chat", `[1, 2]`, FormatChat, "no message"},
		{"scalar line", "1\n2", FormatChat, "a top-level number"},
		{"langfuse junk", `{"foo": 1}`, FormatLangfuse, "neither"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.data), tc.format)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestParseNeverPanics feeds mutated fixtures and random bytes to Parse in
// every format: each must return a trace or an error, never panic.
func TestParseNeverPanics(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	seeds := [][]byte{fixture(t, "otlp-support.json"), fixture(t, "langfuse-trace.json"), fixture(t, "chat-transcript.json")}
	for i := 0; i < 300; i++ {
		b := append([]byte(nil), seeds[i%len(seeds)]...)
		for j := 0; j < 1+rng.Intn(8); j++ {
			b[rng.Intn(len(b))] = byte(" {}[]\":,0a\\"[rng.Intn(11)])
		}
		for _, f := range append(Formats(), "") {
			_, _ = Parse(b, f)
		}
	}
	for _, v := range []string{`{"resourceSpans":[{"scopeSpans":[{"spans":[null,1,"x",{"traceId":"t","attributes":[null,{"key":"gen_ai.input.messages","value":{"stringValue":"[1,{\"role\":3}]"}}]}]}]}]}`,
		`{"observations":[null,{"type":"TOOL"},{"type":"GENERATION","input":{"choices":[1]}}]}`,
		`{"messages":[{"role":"tool","content":[{"type":"tool_result"}]},{"role":"assistant","tool_calls":[null,{"function":1}]}]}`} {
		for _, f := range append(Formats(), "") {
			_, _ = Parse([]byte(v), f)
		}
	}
}

func TestReadFileCap(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.json")
	if err := os.WriteFile(p, bytes.Repeat([]byte(" "), 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(p, 1024); err == nil || !strings.Contains(err.Error(), "over the 1024 byte cap") {
		t.Errorf("err = %v, want the cap named", err)
	}
	if _, err := ReadFile(dir, 1024); err == nil {
		t.Error("a folder was read as a trace")
	}
	if _, err := ReadFile(filepath.Join(dir, "missing.json"), 0); err == nil {
		t.Error("a missing file was read")
	}
	if b, err := ReadFile(p, 0); err != nil || len(b) != 2048 {
		t.Errorf("default cap: %d bytes, %v", len(b), err)
	}
}

func TestPick(t *testing.T) {
	tr, err := Parse(fixture(t, "otlp-support.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(tr.Sessions))
	}
	if _, err := tr.Pick(""); err == nil || !strings.Contains(err.Error(), "-session") {
		t.Errorf("Pick(\"\") on two sessions: %v", err)
	}
	if s, err := tr.Pick("0af7651916cd43dd8448eb211c80319c"); err != nil || s.FirstRequest() != "What is the return window for tents?" {
		t.Errorf("Pick by id: %v %v", s, err)
	}
	if _, err := tr.Pick("nope"); err == nil || !strings.Contains(err.Error(), "-list") {
		t.Errorf("Pick unknown: %v", err)
	}
}

func TestFieldCaps(t *testing.T) {
	long := strings.Repeat("é", MaxFieldBytes)
	data := `{"messages":[{"role":"user","content":"` + long + `"},{"role":"assistant","content":"ok"}]}`
	tr, err := Parse([]byte(data), "")
	if err != nil {
		t.Fatal(err)
	}
	s := tr.Sessions[0]
	if s.Truncated != 1 || len(s.Turns[0].Text) > MaxFieldBytes+64 || !strings.Contains(s.Turns[0].Text, "[truncated:") {
		t.Errorf("truncated = %d, turn length %d", s.Truncated, len(s.Turns[0].Text))
	}
	var b strings.Builder
	b.WriteString(`{"messages":[{"role":"user","content":"go"}`)
	for i := 0; i < MaxToolCalls+5; i++ {
		b.WriteString(`,{"role":"assistant","tool_calls":[{"function":{"name":"f","arguments":"{}"}}]}`)
	}
	b.WriteString(`]}`)
	tr, err = Parse([]byte(b.String()), "")
	if err != nil {
		t.Fatal(err)
	}
	if s := tr.Sessions[0]; len(s.ToolCalls) != MaxToolCalls || s.Skipped["tool calls"] == 0 {
		t.Errorf("tool calls kept %d, skipped %v", len(s.ToolCalls), s.Skipped)
	}
}
