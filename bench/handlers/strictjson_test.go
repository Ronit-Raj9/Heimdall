package handlers

import "testing"

// 4-A-13: a valid object followed by more data is refused.
func TestDecodeJSONStrict(t *testing.T) {
	type body struct {
		Org string `json:"org"`
	}
	for _, in := range []string{`{"org":"a"} {}`, `{"org":"a"}x`, `{"org":"a"}{"org":"b"}`, `{"org":"a","x":1}`, ``} {
		var v body
		if err := decodeJSONStrict([]byte(in), &v); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
	var v body
	if err := decodeJSONStrict([]byte(" {\"org\":\"a\"} \n\t"), &v); err != nil || v.Org != "a" {
		t.Fatalf("single value with whitespace: %+v, %v", v, err)
	}
}
