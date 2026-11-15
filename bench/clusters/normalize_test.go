package clusters

import "testing"

func TestNormalizeLine(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{name: "timestamp", in: "2026-11-14T01:01:05.709416652Z request failed", want: "<ts> request failed"},
		{name: "clock time", in: "at 12:04:59 the run stopped", want: "at <ts> the run stopped"},
		{name: "uuid", in: "key 3f2504e0-4f89-11d3-9a0c-0305e82c3301 rejected", want: "key <uuid> rejected"},
		{name: "hex", in: "panic at 0x7ffd1234 sha d26abfec5775810939b0", want: "panic at <hex> sha <hex>"},
		{name: "path with line", in: `File "/workspace/ledger/money.py:42" broke`, want: `file "<path>" broke`},
		{name: "line reference", in: "SyntaxError at line 17", want: "syntaxerror at line <n>"},
		{name: "numbers", in: "7 failures in 12 tests after 3.5s", want: "<n> failures in <n> tests after <n>s"},
		{name: "http status kept", in: "unexpected status 503 Service Unavailable", want: "unexpected status 503 service unavailable"},
		{name: "measurement is not a status", in: "took 250ms", want: "took <n>ms"},
		{name: "four digit number is not a status", in: "port 5432 refused", want: "port <n> refused"},
		{name: "ids and key-shaped tokens", in: "ref nonce_5l7m7njw691pcbwiqhszkmqf with sk-proj-abc123def456ghi789", want: "ref <id> with <id>"},
		{name: "plain long words stay", in: "test_hidden_rounding failed", want: "test_hidden_rounding failed"},
		{name: "relative path", in: "cannot open tests/test_x.py", want: "cannot open <path>"},
		{name: "fraction is not a path", in: "reconnecting 2/5", want: "reconnecting <n>/<n>"},
		{name: "whitespace", in: "  Error:\t  bad   thing  ", want: "error: bad thing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeLine(tt.in); got != tt.want {
				t.Fatalf("NormalizeLine(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
	t.Run("clipped", func(t *testing.T) {
		long := ""
		for range 50 {
			long += "error word "
		}
		if got := []rune(NormalizeLine(long)); len(got) > maxLineRunes+3 {
			t.Fatalf("length %d, want at most %d", len(got), maxLineRunes+3)
		}
	})
}

func TestFirstStableLine(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		needError bool
		want      string
	}{
		{name: "first informative line", text: "\n---\n42\nconnection reset by peer\nother", want: "connection reset by peer"},
		{name: "banner skipped when an error word is needed", text: "Reading additional input from stdin...\nfatal: not a git repository", needError: true, want: "fatal: not a git repository"},
		{name: "banner alone gives nothing", text: "Reading additional input from stdin...\n", needError: true, want: ""},
		{name: "traceback gives the exception", text: "Traceback (most recent call last):\n  File \"/w/app.py\", line 3, in <module>\n    main()\nValueError: bad window 42\n", needError: true, want: "valueerror: bad window <n>"},
		{name: "placeholder only lines skipped", text: "2026-11-14T01:01:05Z\n0xdeadbeef\nreal error here", want: "real error here"},
		{name: "empty", text: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FirstStableLine(tt.text, tt.needError); got != tt.want {
				t.Fatalf("FirstStableLine = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHash(t *testing.T) {
	if Hash("a", "b") != Hash("a", "b") {
		t.Fatal("hash is not stable")
	}
	if Hash("a", "b") == Hash("ab") || Hash("a", "b") == Hash("b", "a") {
		t.Fatal("hash ignores part boundaries or order")
	}
	if len(Hash("x")) != 12 {
		t.Fatalf("hash length %d, want 12", len(Hash("x")))
	}
}
