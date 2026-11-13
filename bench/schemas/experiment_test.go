package schemas

import (
	"encoding/json"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTaskSelectorJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want TaskSelector
		out  string
	}{
		{"all", `"all"`, TaskSelector{All: true}, `"all"`},
		{"list", `["t01","t02"]`, TaskSelector{IDs: []string{"t01", "t02"}}, `["t01","t02"]`},
		{"legacy object", `{"all":false,"ids":["t03"]}`, TaskSelector{IDs: []string{"t03"}}, `["t03"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got TaskSelector
			if err := json.Unmarshal([]byte(tt.in), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			b, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tt.out {
				t.Fatalf("marshal %s, want %s", b, tt.out)
			}
		})
	}
	var bad TaskSelector
	if err := json.Unmarshal([]byte(`"some"`), &bad); err == nil {
		t.Fatal("want an error for a string other than all")
	}
}

func TestTaskSelectorYAMLRoundTrip(t *testing.T) {
	for _, in := range []TaskSelector{{All: true}, {IDs: []string{"t01", "t05"}}} {
		b, err := yaml.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		var out TaskSelector
		if err := yaml.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(in, out) {
			t.Fatalf("round trip %+v -> %s -> %+v", in, b, out)
		}
	}
}
