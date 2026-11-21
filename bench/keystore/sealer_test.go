package keystore

import (
	"bytes"
	"testing"
)

func TestSealerRoundTripAndBinding(t *testing.T) {
	master := bytes.Repeat([]byte{7}, 32)
	a, err := NewSealer(master, "org_a", "webhook")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := a.Seal("wh_1", "whsec_x")
	if err != nil || enc == "" || enc == "whsec_x" {
		t.Fatalf("seal %q %v", enc, err)
	}
	if got, err := a.Open("wh_1", enc); err != nil || got != "whsec_x" {
		t.Fatalf("open %q %v", got, err)
	}
	b, _ := NewSealer(master, "org_b", "webhook")
	other, _ := NewSealer(master, "org_a", "other")
	for name, try := range map[string]func() (string, error){
		"other id":      func() (string, error) { return a.Open("wh_2", enc) },
		"other org":     func() (string, error) { return b.Open("wh_1", enc) },
		"other purpose": func() (string, error) { return other.Open("wh_1", enc) },
	} {
		if _, err := try(); err == nil {
			t.Errorf("%s opened", name)
		}
	}
	if _, err := NewSealer([]byte("short"), "org_a", "webhook"); err == nil {
		t.Fatal("short master key accepted")
	}
}
