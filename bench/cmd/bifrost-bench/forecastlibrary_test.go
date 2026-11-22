package main

import "testing"

func TestPriceRatio(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		fromIn, fromOut, toIn, toOut float64
		lo, mid, hi                  float64
		ok                           bool
	}{
		{"same", 1, 4, 1, 4, 1, 1, 1, true},
		{"input dearer", 1, 4, 3, 8, 2, 11.0 / 5, 3, true},
		{"cheaper", 2, 10, 1, 2, 0.2, 0.25, 0.5, true},
		{"free source", 0, 4, 1, 4, 0, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lo, mid, hi, ok := priceRatio(tc.fromIn, tc.fromOut, tc.toIn, tc.toOut)
			if ok != tc.ok || lo != tc.lo || mid != tc.mid || hi != tc.hi {
				t.Fatalf("got %v %v %v %v", lo, mid, hi, ok)
			}
		})
	}
}
