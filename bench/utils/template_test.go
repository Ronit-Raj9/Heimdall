package utils

import "testing"

func TestRender(t *testing.T) {
	tests := []struct {
		name    string
		tmpl    string
		vars    map[string]string
		want    string
		wantErr bool
	}{
		{"simple", "{{gateway.url}}/anthropic", map[string]string{"gateway.url": "http://bifrost:8080"}, "http://bifrost:8080/anthropic", false},
		{"spaces", "x={{ run.id }}", map[string]string{"run.id": "r_1"}, "x=r_1", false},
		{"no vars", "plain", nil, "plain", false},
		{"unknown", "{{nope}}", map[string]string{}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Render(tt.tmpl, tt.vars)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
