package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/livescore"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// Gap lane G03: livescore.pull and inbox.pull on the hosted server. They
// only make HTTP calls, so they run in the server process with the
// guardrails of handlers/liveinbox_pull.go: public https URLs checked again
// at connect time, credentials from org secrets only, capped requests,
// bytes and time, and the paid judge only on opt-in with the org's own
// provider key.

// pullNotReady turns the first missing step of a check into a queue-time
// refusal that says what to do.
func pullNotReady(c handlers.PullCheck) error {
	if len(c.Steps) == 0 {
		return nil
	}
	st := c.Steps[0]
	msg := st.Problem + ": " + st.Action
	if st.Link == handlers.PullLinkSecrets {
		msg += " (" + st.Link + ")"
	}
	if len(c.Steps) > 1 {
		msg += fmt.Sprintf(" (and %d more steps on the page)", len(c.Steps)-1)
	}
	return fmt.Errorf("%w: %s", jobs.ErrInvalidParams, msg)
}

func validateLivePullHosted(ctx context.Context, env jobEnv, p livePullParams) error {
	c := handlers.LivePullCheck(ctx, env.Services, env.App.cfg)
	if err := pullNotReady(c); err != nil {
		return err
	}
	if p.Judge {
		if c.Judge == nil {
			return liveInvalid("the judge is ticked but livescore/config.yaml has no judge section; untick it or add one")
		}
		if !c.Judge.KeyReady {
			return liveInvalid("%s", c.Judge.Next)
		}
	}
	return nil
}

func runLivePullHosted(ctx context.Context, env jobEnv, p livePullParams, log jobs.Logger) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, handlers.HostedPullTimeout)
	defer cancel()
	s, err := scorerWith(env.App, false)
	if err != nil {
		return nil, err
	}
	if err := handlers.CheckPullURL(s.SourceURL()); err != nil {
		return nil, fmt.Errorf("gateway URL: %w", err)
	}
	s.Getenv = handlers.OrgSecretGetenv(ctx, env.Services)
	s.HTTP = handlers.HostedPullClient()
	s.Notifier = handlers.NewAlertSender(ctx, env.Services, env.App.cfg.Path(livescore.Dir), false)
	if j := s.Config.Judge; j != nil {
		if !p.Judge {
			log.Warnf("the live judge is off for this pull (paid calls); tick Judge to use it with your org's provider key")
		} else {
			judge, err := hostedJudge(ctx, env, j)
			if err != nil {
				return nil, err
			}
			s.Judge = judge
			log.Printf("judge on: %s with this org's provider key, at most %d calls", j.Model, j.MaxPerPull)
		}
	}
	log.Printf("pulling %s (hosted: at most %d requests, %d MiB, %s)", s.SourceURL(), handlers.HostedPullMaxRequests, handlers.HostedPullMaxBytes>>20, handlers.HostedPullTimeout)
	res, err := s.Pull(ctx)
	if res != nil {
		printLivePull(log, res)
	}
	if err != nil {
		return res, fmt.Errorf("livescore pull: %w", err)
	}
	return res, nil
}

// hostedJudge builds the paid judge with the org's own provider key; its
// prompt and calibration files must be inside the project.
func hostedJudge(ctx context.Context, env jobEnv, j *livescore.JudgeConfig) (livescore.Judge, error) {
	key, err := handlers.OrgJudgeKey(ctx, env.Services, j.URL, j.KeyEnv)
	if err != nil {
		return nil, err
	}
	root := env.App.cfg.Root
	prompt := ""
	if j.PromptFile != "" {
		path, err := inProject(root, env.App.cfg.Path(j.PromptFile))
		if err != nil {
			return nil, fmt.Errorf("judge.prompt_file: %w", err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("judge prompt: %w", err)
		}
		prompt = string(b)
	}
	calib := ""
	if j.Calibration != "" {
		if calib, err = inProject(root, env.App.cfg.Path(j.Calibration)); err != nil {
			return nil, fmt.Errorf("judge.calibration: %w", err)
		}
	}
	return livescore.NewChatJudge(j, key, prompt, calib, handlers.HostedPullClient()), nil
}

// inProject resolves path (following symlinks) and refuses one outside root.
func inProject(root, path string) (string, error) {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		p = filepath.Clean(path)
	}
	rel, err := filepath.Rel(r, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errors.New("must be a file inside the project")
	}
	return p, nil
}

// hostedPullable reports whether the hosted server pulls a source kind
// (an otlp_dir source reads a server folder, so it is local only).
func hostedPullable(kind string) bool {
	return kind == traceinbox.KindBifrost || kind == traceinbox.KindLangfuse || kind == traceinbox.KindLangSmith
}

func validateInboxPullHosted(ctx context.Context, env jobEnv, p inboxPullParams, cfg *traceinbox.Config) error {
	for _, name := range p.Sources {
		for _, s := range cfg.Sources {
			if s.Name == name && !hostedPullable(s.Kind) {
				return liveInvalid("source %s is %s, which the hosted server does not pull", name, s.Kind)
			}
		}
	}
	return pullNotReady(handlers.InboxPullCheck(ctx, env.Services, env.App.cfg))
}

func runInboxPullHosted(ctx context.Context, env jobEnv, p inboxPullParams, log jobs.Logger) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, handlers.HostedPullTimeout)
	defer cancel()
	ib, err := inboxFor(env.App, false)
	if err != nil {
		return nil, err
	}
	ib.Getenv = handlers.OrgSecretGetenv(ctx, env.Services)
	ib.HTTP = handlers.HostedPullClient()
	opts := traceinbox.PullOptions{Sources: p.Sources}
	var names []string
	for _, s := range ib.Config.Sources {
		if len(p.Sources) > 0 && !slices.Contains(p.Sources, s.Name) {
			continue
		}
		if !hostedPullable(s.Kind) {
			if len(p.Sources) > 0 {
				return nil, fmt.Errorf("source %s is %s, which the hosted server does not pull", s.Name, s.Kind)
			}
			if s.Kind != traceinbox.KindWebhook {
				log.Warnf("skipped source %s: %s sources are pulled only by a local Heimdall", s.Name, s.Kind)
			}
			continue
		}
		u := s.Host
		if s.Kind == traceinbox.KindBifrost {
			u = s.URL
		}
		if err := handlers.CheckPullURL(u); err != nil {
			return nil, fmt.Errorf("source %s: %w", s.Name, err)
		}
		names = append(names, s.Name)
	}
	if len(names) == 0 {
		return nil, errors.New("no source the hosted server can pull: add a Bifrost, Langfuse or LangSmith source")
	}
	opts.Sources = names
	if p.Since != "" {
		t, err := parseSince(p.Since, time.Now())
		if err != nil {
			return nil, err
		}
		opts.Since = t
	}
	log.Printf("pulling %s (hosted: at most %d requests, %d MiB, %s)", strings.Join(names, ", "), handlers.HostedPullMaxRequests, handlers.HostedPullMaxBytes>>20, handlers.HostedPullTimeout)
	results, err := ib.Pull(ctx, opts)
	printPull(log, results)
	if err != nil {
		return results, fmt.Errorf("inbox pull: %w", err)
	}
	return results, nil
}
