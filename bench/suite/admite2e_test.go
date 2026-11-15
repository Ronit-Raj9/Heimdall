//go:build integration

package suite_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// TestAdmitScenarioTasks admits the hand-written scenario pack's state task
// (script controls, one service) and answer task (answer controls, two
// sessions) through the real docker environment and verifier.
func TestAdmitScenarioTasks(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	for _, img := range []string{"bench/coding-core-base:1", "python:3.12-slim"} {
		if err := exec.Command("docker", "image", "inspect", img).Run(); err != nil {
			t.Skipf("image %s is not present locally", img)
		}
	}
	loader := suite.Builtin(filepath.Join("testdata", "scenario"))[0]
	s, err := loader.Load(context.Background(), "hd-run-e2e")
	if err != nil {
		t.Fatal(err)
	}
	env := environments.Builtin(environments.DockerWorkspaceConfig{HelperImage: "bench/coding-core-base:1"})[0]
	var grader schemas.Grader
	for _, g := range graders.Builtin(graders.HiddenTestsConfig{}) {
		if g.GetName() == s.Grader {
			grader = g
		}
	}
	if grader == nil {
		t.Fatalf("grader %s not built in", s.Grader)
	}
	for _, id := range []string{"state", "recall"} {
		t.Run(id, func(t *testing.T) {
			rec, err := suite.Admit(context.Background(), suite.AdmitDeps{Env: env, Grader: grader}, s, s.TaskByID(id))
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range rec.Checks {
				t.Logf("%s want %s got %s passed %v: %s", c.Name, c.Want, c.Got, c.Passed, c.Detail)
			}
			if !rec.Admitted {
				t.Fatalf("task %s not admitted", id)
			}
		})
	}
}

// TestAdmitImpossibleSWE admits the local Impossible-SWEbench task
// (benchmarks/hd-swe-ib-swe, imported, git-ignored): its gold patch is the
// honest reference, which must pass the original tests 3 times and fail the
// mutated tests. It is skipped when the pack or its task image is missing.
func TestAdmitImpossibleSWE(t *testing.T) {
	const image = "swerebench/sweb.eval.x86_64.fabfuel_1776_circuitbreaker-67:latest"
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	for _, img := range []string{image, "bench/hd-swe-ib-swe-base:1", "bench/coding-core-base:1"} {
		if err := exec.Command("docker", "image", "inspect", img).Run(); err != nil {
			t.Skipf("image %s is not present locally", img)
		}
	}
	loader := suite.Builtin(filepath.Join("..", "benchmarks"))[0]
	s, err := loader.Load(context.Background(), "hd-swe-ib-swe")
	if err != nil {
		t.Skipf("local pack hd-swe-ib-swe not loadable: %v", err)
	}
	task := s.TaskByID("fabfuel--circuitbreaker-67")
	if task == nil || task.Controls.Honest == "" {
		t.Skip("task has no honest reference")
	}
	env := environments.Builtin(environments.DockerWorkspaceConfig{HelperImage: "bench/coding-core-base:1"})[0]
	var grader schemas.Grader
	for _, g := range graders.Builtin(graders.HiddenTestsConfig{Image: "bench/coding-core-base:1"}) {
		if g.GetName() == s.Grader {
			grader = g
		}
	}
	rec, err := suite.Admit(context.Background(), suite.AdmitDeps{Env: env, Grader: grader}, s, task)
	if err != nil {
		t.Fatal(err)
	}
	passed := map[string]bool{}
	for _, c := range rec.Checks {
		t.Logf("%s want %s got %s passed %v: %s", c.Name, c.Want, c.Got, c.Passed, c.Detail)
		passed[c.Name] = c.Passed
	}
	for _, n := range []string{"honest_original_1", "honest_original_2", "honest_original_3", "honest_mutated"} {
		if !passed[n] {
			t.Errorf("check %s did not pass", n)
		}
	}
	if !rec.Admitted {
		t.Errorf("not admitted")
	}
}
