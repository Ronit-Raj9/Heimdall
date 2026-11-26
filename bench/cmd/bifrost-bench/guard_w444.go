package main

import "github.com/maximhq/bifrost/bench/schemas"

// taskByID is the loaded task id of s, or nil when s does not list it, for
// the quality guard of the hosted readiness record.
func taskByID(s *schemas.Suite, id string) *schemas.Task {
	if s == nil {
		return nil
	}
	return s.TaskByID(id)
}
