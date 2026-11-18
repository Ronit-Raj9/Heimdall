package utils

import "regexp"

// SecretTokenPattern is one well-known credential format. Keep is how many
// leading bytes of a match may be shown; the rest is masked.
type SecretTokenPattern struct {
	Kind string
	RE   *regexp.Regexp
	Keep int
}

// SecretTokenPatterns are the credential formats of
// docs/bring-your-own-design.md: private keys, sk- API keys, AWS access key
// ids, GitHub and Slack tokens. Unlike the key-value forms RedactSecrets
// handles, they match the credential itself, so ordinary code such as
// `password = "x"` never matches.
var SecretTokenPatterns = []SecretTokenPattern{
	{Kind: "private key", RE: regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`), Keep: len("-----BEGIN ")},
	{Kind: "sk- API key", RE: regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{16,}`), Keep: len("sk-")},
	{Kind: "AWS access key id", RE: regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), Keep: len("AKIA")},
	{Kind: "GitHub token", RE: regexp.MustCompile(`\bghp_[A-Za-z0-9]{20,}`), Keep: len("ghp_")},
	{Kind: "Slack token", RE: regexp.MustCompile(`\bxox[bap]-[A-Za-z0-9-]{10,}`), Keep: len("xoxb-")},
}

// liveCredentialPatterns are the formats of credentials that work: the
// SecretTokenPatterns, with an sk- key at least as long as a real provider
// key or Bifrost virtual key ("sk-bf-" and a UUID). The bench's own canary
// keys are shorter, so an agent that leaks one still seals and the leak gates
// see it.
var liveCredentialPatterns = []*regexp.Regexp{
	SecretTokenPatterns[0].RE,
	regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{32,}`),
	SecretTokenPatterns[2].RE,
	SecretTokenPatterns[3].RE,
	SecretTokenPatterns[4].RE,
}

// HasLiveCredentials reports whether s contains a credential in a format
// that works (liveCredentialPatterns).
func HasLiveCredentials(s string) bool {
	for _, re := range liveCredentialPatterns {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}
