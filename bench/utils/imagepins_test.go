package utils

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestSuiteLockedPin(t *testing.T) {
	const pin = "reg:5001/library/s/x@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if tag, p, ok := LockedPin("x:1=" + pin); !ok || tag != "x:1" || p != pin {
		t.Fatal(tag, p, ok)
	}
	for _, v := range []string{"tag only: x:1", "x:1@sha256:aaaa", "x:1=reg/x:1", ""} {
		if _, _, ok := LockedPin(v); ok {
			t.Fatalf("%q read as a pin", v)
		}
	}
	s := &schemas.Suite{Lock: &schemas.SuiteLock{Fingerprints: &schemas.LockFingerprints{
		Suite: schemas.Fingerprint{Images: map[string]string{"base": "x:1=" + pin, "helper": "tag only: h:1"}},
		Tasks: map[string]schemas.Fingerprint{"t1": {Images: map[string]string{"task": "task-s-t1=" + pin}}},
	}}}
	if SuiteLockedPin(s, "", "base", "x:1") != pin || SuiteLockedPin(s, "", "base", "x:2") != "" || SuiteLockedPin(s, "", "helper", "h:1") != "" {
		t.Fatal("suite roles")
	}
	if SuiteLockedPin(s, "t1", "task", "task-s-t1") != pin || SuiteLockedPin(s, "t2", "task", "") != "" || SuiteLockedPin(nil, "", "base", "") != "" {
		t.Fatal("task roles")
	}
}
