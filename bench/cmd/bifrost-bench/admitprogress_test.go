package main

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestAdmitProgress(t *testing.T) {
	if admitProgress(nil) != nil {
		t.Fatal("nil progress must give a nil callback")
	}
	var got []map[string]any
	f := admitProgress(func(v any) { got = append(got, v.(map[string]any)) })
	f(0, 2, "", nil)
	f(1, 2, "a", &schemas.AdmissionRecord{Admitted: true})
	f(2, 2, "b", &schemas.AdmissionRecord{})
	last := got[len(got)-1]
	if len(got) != 3 || last["done"] != 2 || last["total"] != 2 || last["admitted"] != 1 || last["refused"] != 1 || last["task"] != "b" {
		t.Fatalf("%v", got)
	}
}
