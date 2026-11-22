package main

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestRegistryHtpasswd(t *testing.T) {
	pw := strings.Repeat("ab12", 12)
	line, err := registryHtpasswd([]string{"-user", "heimdall-pull"}, strings.NewReader(pw+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	user, hash, _ := strings.Cut(line, ":")
	if user != "heimdall-pull" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) != nil || strings.Contains(line, pw) {
		t.Fatalf("htpasswd line %q", line)
	}
	for _, c := range []struct {
		args []string
		in   string
	}{
		{[]string{"-user", "Bad:Name"}, pw},
		{[]string{}, pw},
		{[]string{"-user", "heimdall-pull"}, "short"},
	} {
		if _, err := registryHtpasswd(c.args, strings.NewReader(c.in)); err == nil {
			t.Errorf("accepted %v %q", c.args, c.in)
		}
	}
}
