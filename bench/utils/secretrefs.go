package utils

import (
	"regexp"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Org secrets in runs (gap G01). A run names org secrets in two places:
// the auth_env of an external API that runs live or record, and an agent env
// value of the form {{secret.NAME}}. Both resolve from the org's secrets
// (Settings > Secrets) at run time; the values only reach the tool proxy's
// environment and the agent container's environment.

var secretRefRe = regexp.MustCompile(`\{\{\s*secret\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

var secretNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// SecretNameOK reports whether name can be an org secret name, the same
// names a {{secret.NAME}} reference accepts.
func SecretNameOK(name string) bool { return secretNameRe.MatchString(name) }

// SecretRefs returns the secret names env values reference as
// {{secret.NAME}}, sorted and without duplicates.
func SecretRefs(env map[string]string) []string {
	seen := map[string]bool{}
	for _, v := range env {
		for _, m := range secretRefRe.FindAllStringSubmatch(v, -1) {
			seen[m[1]] = true
		}
	}
	return sortedSet(seen)
}

// ExpandSecretRefs replaces every {{secret.NAME}} in v with its value from
// secrets and returns the names it could not resolve.
func ExpandSecretRefs(v string, secrets map[string]string) (string, []string) {
	var missing []string
	out := secretRefRe.ReplaceAllStringFunc(v, func(ref string) string {
		name := secretRefRe.FindStringSubmatch(ref)[1]
		val, ok := secrets[name]
		if !ok || val == "" {
			missing = append(missing, name)
			return ref
		}
		return val
	})
	return out, missing
}

// ExternalSecretNames returns the auth_env names of the external APIs of
// task t (suite and task level) that run live or record under manifest m:
// a replay API never reads its credential. An API whose mode cannot be
// resolved is skipped; the plan refuses it with its own reason.
func ExternalSecretNames(m *schemas.Manifest, s *schemas.Suite, t *schemas.Task) []string {
	seen := map[string]bool{}
	rc := &schemas.RunContext{Manifest: m}
	for _, r := range ExternalAPIs(s, t) {
		if r.API.AuthEnv == "" {
			continue
		}
		mode, err := RunExternalMode(rc, r)
		if err != nil || mode == schemas.ExternalReplay {
			continue
		}
		seen[r.API.AuthEnv] = true
	}
	return sortedSet(seen)
}

// RunSecretNames returns every org secret name the run needs: its live and
// record external APIs' auth_env and its agent's {{secret.NAME}} env refs.
func RunSecretNames(rc *schemas.RunContext) []string {
	if rc == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, n := range ExternalSecretNames(rc.Manifest, rc.Suite, rc.Task) {
		seen[n] = true
	}
	if rc.Agent != nil {
		for _, n := range SecretRefs(rc.Agent.Env) {
			seen[n] = true
		}
		if r := rc.Agent.Remote; r != nil { // J3: remote.auth_secret and remote.sign_secret (adapters/remotehttp.go)
			for _, n := range []string{r.AuthSecret, r.SignSecret} {
				if SecretNameOK(n) {
					seen[n] = true
				}
			}
		}
		if o := rc.Agent.OpenAI; o != nil && SecretNameOK(o.APIKeySecret) { // A08: openai.api_key_secret (adapters/openaiagent.go)
			seen[o.APIKeySecret] = true
		}
		if v := rc.Agent.VoicePlatform; v != nil && SecretNameOK(v.APIKeySecret) { // voice_platform.api_key_secret (adapters/voiceplatform.go)
			seen[v.APIKeySecret] = true
		}
	}
	return sortedSet(seen)
}

// SlotSecretNames is RunSecretNames for one slot of a manifest: what a
// coordinator hands the runner that leased the slot, and nothing more.
func SlotSecretNames(m *schemas.Manifest, s *schemas.Suite, slot *schemas.Slot) []string {
	if m == nil || slot == nil {
		return nil
	}
	rc := &schemas.RunContext{Manifest: m, Suite: s}
	if s != nil {
		rc.Task = s.TaskByID(slot.Task)
	}
	if slot.Arm == "compat" {
		if m.Compatibility != nil {
			rc.Agent = &m.Compatibility.Config
		}
	} else if a, ok := m.Arms[slot.Arm]; ok {
		rc.Agent = &a.Config
	}
	return RunSecretNames(rc)
}

// MissingSecrets returns the names of want that have no non-empty value in
// got, in want's order.
func MissingSecrets(want []string, got map[string]string) []string {
	var out []string
	for _, n := range want {
		if got[n] == "" {
			out = append(out, n)
		}
	}
	return out
}

func sortedSet(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
