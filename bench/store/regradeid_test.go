package store

import "testing"

func TestValidRegradeID(t *testing.T) {
	for id, want := range map[string]bool{
		"zz_test_rescore": true,
		"rg_0a9z":         true,
		"zz-test-rescore": false,
		"Upper":           false,
		"a.b":             false,
		"":                false,
	} {
		if got := ValidRegradeID(id); got != want {
			t.Errorf("ValidRegradeID(%q) = %v, want %v", id, got, want)
		}
	}
	long := make([]byte, maxIDLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if ValidRegradeID(string(long)) {
		t.Error("an id longer than the store allows was accepted")
	}
}
