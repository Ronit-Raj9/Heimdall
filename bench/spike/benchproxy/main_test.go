package main

import "testing"

func TestEnvOr(t *testing.T) {
	t.Setenv("BENCHPROXY_TEST_SET", "x")
	cases := []struct{ key, def, want string }{
		{"BENCHPROXY_TEST_SET", "d", "x"},
		{"BENCHPROXY_TEST_UNSET", "d", "d"},
	}
	for _, tc := range cases {
		if got := envOr(tc.key, tc.def); got != tc.want {
			t.Errorf("envOr(%s) = %q, want %q", tc.key, got, tc.want)
		}
	}
}
