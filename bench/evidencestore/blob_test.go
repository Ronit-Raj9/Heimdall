package evidencestore

import "testing"

func TestValidKey(t *testing.T) {
	cases := []struct {
		key string
		ok  bool
	}{
		{"local/e_1/manifest.json", true},
		{"org_x/e_1/slots/s_1/runs/r_1/evidence/shim.log", true},
		{"a b/c+d=e~f$g/ü.txt", true},
		{"_backups/db/heimdall-20261118T010203Z.sqlite", true},
		{"", false},
		{"/abs", false},
		{"trailing/", false},
		{"a//b", false},
		{"a/./b", false},
		{"a/../b", false},
		{"..", false},
		{`a\b`, false},
		{"a/\x00b", false},
		{"a/\x7fb", false},
		{"bad/\xff", false},
		{string(make([]byte, 1025)), false},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			if err := ValidKey(c.key); (err == nil) != c.ok {
				t.Fatalf("ValidKey(%q) = %v, want ok=%v", c.key, err, c.ok)
			}
		})
	}
	for p, ok := range map[string]bool{"": true, "local/": true, "local/e_1": true, "/x": false, "a//": false} {
		if err := ValidPrefix(p); (err == nil) != ok {
			t.Errorf("ValidPrefix(%q) = %v, want ok=%v", p, err, ok)
		}
	}
}
