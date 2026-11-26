package main

// Benchmark sources for share links (W4-48, private repositories): every
// path that puts tasks from a GitHub repository into a benchmark records the
// repository through recordRepoSource, the single caller of
// issuesync.RecordSource here, before it writes a task. A repository not
// proven public counts as private (handlers/shareprivate_w448.go).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/bench/issuesync"
)

// recordRepoSource records benchmark's source repository, public only when
// it was fetched without a token. No project folder refuses the write, so a
// private repository never reaches a benchmark unrecorded.
func recordRepoSource(projectRoot, benchmark, repo string, public bool) error {
	if projectRoot == "" {
		return errors.New("this server has no project folder to record where the benchmark's tasks come from")
	}
	if err := issuesync.RecordSource(projectRoot, benchmark, repo, public, time.Now()); err != nil {
		return fmt.Errorf("record where the benchmark's tasks come from: %w", err)
	}
	return nil
}

// recordSource records the GitHub repository of a suite op ("Task from a
// commit", "Find tasks"): public only when it was cloned without the
// organization's GitHub token. A local folder (local mode) records nothing.
func (r suiteOpsRepo) recordSource(ctx context.Context, env jobEnv, suite string) error {
	if r.GitHub == "" {
		return nil
	}
	token, err := suiteOpsGitHubToken(ctx, env)
	if err != nil {
		return err
	}
	return recordRepoSource(env.ProjectRoot, suite, r.GitHub, token == "")
}
