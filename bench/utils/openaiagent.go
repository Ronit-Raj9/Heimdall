package utils

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// OpenAI-compatible agents (adapter openai_agent, docs/openai-agents.md).

// AdapterOpenAIAgent is the adapter name of an OpenAI-compatible agent.
const AdapterOpenAIAgent = "openai_agent"

// IsOpenAIAgent reports whether cfg is an OpenAI-compatible endpoint agent.
func IsOpenAIAgent(cfg *schemas.AgentConfig) bool {
	return cfg != nil && (cfg.Adapter == AdapterOpenAIAgent || cfg.OpenAI != nil)
}

// CheckOpenAIAgent refuses an openai block the launcher could not use. It
// is pure.
func CheckOpenAIAgent(o *schemas.OpenAIAgent) error {
	if o == nil {
		return fmt.Errorf("an openai_agent needs an openai block with url")
	}
	if _, err := OpenAIEndpoint(o); err != nil {
		return err
	}
	switch o.Tools {
	case "", schemas.OpenAIToolsAuto, schemas.OpenAIToolsOff:
	default:
		return fmt.Errorf("openai.tools %q: want auto or off", o.Tools)
	}
	if o.APIKeySecret != "" && !SecretNameOK(o.APIKeySecret) {
		return fmt.Errorf("openai.api_key_secret %q is not a secret name (letters, digits and _)", o.APIKeySecret)
	}
	if o.MaxTurns < 0 || o.TimeoutS < 0 {
		return fmt.Errorf("openai.max_turns and openai.timeout_s cannot be negative")
	}
	return nil
}

// OpenAIEndpoint is the URL the launcher posts to: the configured URL when
// it already ends in /chat/completions or /responses, else the base plus
// the API's path. It is pure.
func OpenAIEndpoint(o *schemas.OpenAIAgent) (string, error) {
	api := o.API
	switch api {
	case "":
		api = schemas.OpenAIAPIChat
	case schemas.OpenAIAPIChat, schemas.OpenAIAPIResponses:
	default:
		return "", fmt.Errorf("openai.api %q: want chat or responses", o.API)
	}
	u, err := url.Parse(strings.TrimSpace(o.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("openai.url %q: want http(s)://host[:port]/path with no user info", o.URL)
	}
	p := strings.TrimRight(u.Path, "/")
	switch {
	case strings.HasSuffix(p, "/chat/completions"):
		if api != schemas.OpenAIAPIChat {
			return "", fmt.Errorf("openai.url ends in /chat/completions but openai.api is %s", api)
		}
	case strings.HasSuffix(p, "/responses"):
		if api != schemas.OpenAIAPIResponses {
			return "", fmt.Errorf("openai.url ends in /responses but openai.api is %s", api)
		}
	case api == schemas.OpenAIAPIResponses:
		p += "/responses"
	default:
		p += "/chat/completions"
	}
	u.Path = p
	return u.String(), nil
}

// OpenAIOutputAllowed says whether an OpenAI-compatible agent can run a
// task with output kind kind, and why not. Answer always; state only with
// tools offered (it changes the services through the tool proxy, which
// the launcher calls for it); never diff or artifact, since the agent is
// not beside the workspace. It is pure.
func OpenAIOutputAllowed(o *schemas.OpenAIAgent, kind string) (bool, string) {
	switch kind {
	case schemas.OutputAnswer:
		return true, ""
	case schemas.OutputState:
		if o != nil && o.Tools != schemas.OpenAIToolsOff {
			return true, ""
		}
		return false, "a state task needs openai.tools auto: the endpoint changes the run's services only through the tool calls the bench runs for it"
	default:
		return false, "an OpenAI-compatible endpoint is not beside the workspace, so it cannot produce a " + kind + " output"
	}
}
