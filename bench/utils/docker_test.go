package utils

import (
	"reflect"
	"testing"
)

func TestLabelArgs(t *testing.T) {
	got := LabelArgs("e_x", "r_y")
	want := []string{"--label", "bifrost-bench=1", "--label", "bifrost-bench.exp=e_x", "--label", "bifrost-bench.run=r_y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
