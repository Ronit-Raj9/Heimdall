package toolproxy

import (
	"io"
	"strings"
	"testing"
)

func TestSSEReader(t *testing.T) {
	stream := "event: message\r\nid: 1\r\ndata: {\"a\":\r\ndata: 1}\r\n\r\n: comment\n\ndata: tail"
	rd := newSSEReader(strings.NewReader(stream))
	tests := []struct {
		name     string
		wantData string
		wantHas  bool
		wantErr  bool
		wantRaw  string
	}{
		{name: "multi-line data, CRLF", wantData: "{\"a\":\n1}", wantHas: true, wantRaw: "event: message\r\nid: 1\r\ndata: {\"a\":\r\ndata: 1}\r\n\r\n"},
		{name: "comment only", wantHas: false, wantRaw: ": comment\n\n"},
		{name: "partial at end", wantData: "tail", wantHas: true, wantErr: true, wantRaw: "data: tail"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := rd.next()
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if err != nil && err != io.EOF {
				t.Fatalf("err = %v, want EOF", err)
			}
			d, ok := ev.data()
			if ok != tt.wantHas || string(d) != tt.wantData {
				t.Fatalf("data = %q %v", d, ok)
			}
			if string(ev.bytes()) != tt.wantRaw {
				t.Fatalf("raw = %q, want %q", ev.bytes(), tt.wantRaw)
			}
		})
	}
}

func TestSSESetData(t *testing.T) {
	tests := []struct {
		name string
		ev   sseEvent
		want string
	}{
		{
			name: "keeps other fields, one data line in place of the first",
			ev:   sseEvent{lines: [][]byte{[]byte("event: message\n"), []byte("data: {\"a\":\n"), []byte("id: 9\n"), []byte("data: 1}\n")}, end: []byte("\n")},
			want: "event: message\ndata: {\"b\":2}\nid: 9\n\n",
		},
		{
			name: "partial event gets an end",
			ev:   sseEvent{lines: [][]byte{[]byte("data: x")}},
			want: "data: {\"b\":2}\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.ev.setData([]byte(`{"b":2}`))
			if got := string(tt.ev.bytes()); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDataValue(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{in: "data: x\n", want: "x", ok: true},
		{in: "data:x\r\n", want: "x", ok: true},
		{in: "data\n", want: "", ok: true},
		{in: "event: data\n", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			v, ok := dataValue([]byte(tt.in))
			if ok != tt.ok || string(v) != tt.want {
				t.Fatalf("got %q %v", v, ok)
			}
		})
	}
}
