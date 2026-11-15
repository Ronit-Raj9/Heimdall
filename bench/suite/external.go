package suite

import (
	"fmt"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// externalParts hashes a task's external APIs (docs/external-apis.md) into
// out: each API's spec, the suite's first, and the content of each cassette
// that exists. A cassette is the recorded answers replay runs and admission
// are graded against, so re-recording it changes the lock like editing a
// fixture. A task and suite without external APIs add nothing, so their lock
// hashes as before.
func externalParts(out map[string]string, s *schemas.Suite, t *schemas.Task) []error {
	var errs []error
	suiteN := 0
	if s != nil {
		suiteN = len(s.External)
	}
	for i, r := range utils.ExternalAPIs(s, t) {
		key := fmt.Sprintf("external[%d]", i-suiteN)
		if i < suiteN {
			key = fmt.Sprintf("suite.external[%d]", i)
		}
		sum, err := utils.HashJSON(r.API)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
			continue
		}
		out[key] = sum
		p, have, err := utils.ExternalCassette(r)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
			continue
		}
		if !have {
			continue
		}
		if sum, err = utils.SHA256File(p); err != nil {
			errs = append(errs, fmt.Errorf("%s.cassette: %w", key, err))
			continue
		}
		out[key+".cassette"] = sum
	}
	return errs
}

// lintExternalSuite checks the suite's own external APIs, reported against
// suite.yaml: each well formed (utils.CheckExternalAPIs), not a suite service
// id, and its cassette present.
func lintExternalSuite(s *schemas.Suite) []Problem {
	refs := utils.ExternalAPIs(s, nil)
	services := map[string]bool{}
	for _, sv := range s.Services {
		services[sv.ID] = true
	}
	return lintExternalRefs(SuiteFile, "external", refs, services, nil)
}

// lintExternalTask checks one task's external APIs, reported against its
// task.yaml: each well formed, its id used by no suite API and no service of
// the suite or the task, and its cassette present.
func lintExternalTask(s *schemas.Suite, t *schemas.Task, taskYAML string) []Problem {
	if len(t.External) == 0 {
		return nil
	}
	services := map[string]bool{}
	for _, sv := range utils.TaskServices(s, t) {
		services[sv.ID] = true
	}
	suiteIDs := map[string]bool{}
	for _, a := range s.External {
		suiteIDs[a.ID] = true
	}
	return lintExternalRefs(taskYAML, "external", utils.ExternalAPIs(nil, t), services, suiteIDs)
}

// lintExternalRefs checks external APIs declared in one file. A missing
// cassette is a warning, not an error: a task is written before its first
// recording, which an experiment with external: record makes. Admission and
// replay runs need it, and refuse without it.
func lintExternalRefs(file, field string, refs []utils.ExternalRef, services, earlier map[string]bool) []Problem {
	var out []Problem
	add := func(sev, format string, args ...any) {
		out = append(out, Problem{Path: file, Severity: sev, Message: fmt.Sprintf(format, args...)})
	}
	if err := utils.CheckExternalAPIs(refs); err != nil {
		add(SeverityError, "%s: %v", field, err)
	}
	for i, r := range refs {
		at := fmt.Sprintf("%s[%d]", field, i)
		switch {
		case services[r.API.ID]:
			add(SeverityError, "%s.id: %q is also a service id; the run reaches both by id", at, r.API.ID)
		case earlier[r.API.ID]:
			add(SeverityError, "%s.id: %q is already an external api in %s", at, r.API.ID, SuiteFile)
		}
		if len(r.API.Allow) == 0 {
			add(SeverityWarning, "%s.allow: empty, so the tool proxy refuses every call to %s", at, r.API.ID)
		}
		if r.API.Mode == schemas.ExternalLive || r.API.Mode == schemas.ExternalRecord {
			add(SeverityWarning, "%s.mode: %s calls the real host and is not repeatable; it runs only when an experiment sets external: %s, and admission never runs it", at, r.API.Mode, r.API.Mode)
		}
		if r.API.Cassette == "" {
			add(SeverityWarning, "%s.cassette: not set; admission and replay runs need one (record it with an experiment that sets external: record)", at)
			continue
		}
		_, have, err := utils.ExternalCassette(r)
		switch {
		case err != nil:
			add(SeverityError, "%s.cassette: %v", at, err)
		case !have:
			add(SeverityWarning, "%s.cassette: %s does not exist yet; admission and replay runs need it (record it with an experiment that sets external: record, then copy external/%s%s from the run folder)",
				at, filepath.ToSlash(r.API.Cassette), r.API.ID, utils.ExternalCassetteSuffix)
		}
	}
	return out
}
