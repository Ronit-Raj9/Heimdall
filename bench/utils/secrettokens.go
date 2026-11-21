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
// key or Bifrost virtual key ("sk-bf-" and a UUID; liveSKKey).
var liveCredentialPatterns = []*regexp.Regexp{
	SecretTokenPatterns[0].RE,
	SecretTokenPatterns[2].RE,
	SecretTokenPatterns[3].RE,
	SecretTokenPatterns[4].RE,
}

// liveSKKey is an sk- key at least as long as a real provider key or Bifrost
// virtual key.
var liveSKKey = regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{32,}`)

// benchCanaryKey is a canary key the bench plants itself, such as
// "sk-bench-canary-{{nonce}}" or "sk-team-canary-{{nonce}}": an sk- key whose
// label ends in "canary-", then a probe nonce (NewNonce). With the nonce it is
// as long as a real key, but it opens nothing, and a run whose agent leaks it
// must still seal so the leak gates can see it. (S1-fix: these canaries are
// 43 and 42 characters, so liveSKKey alone refused to seal every such run.)
var benchCanaryKey = regexp.MustCompile(`^sk-[a-z0-9-]*canary-nonce_[0-9a-z]{24}$`)

// HasLiveAPICredentials is HasLiveCredentials without private keys: the
// formats that open an account outside the sandbox by themselves (provider
// and Bifrost keys, AWS key ids, GitHub and Slack tokens). A PEM private key
// opens nothing on its own, and an agent writes one whenever the task asks
// for a key pair or a TLS certificate (terminal-bench's
// openssl-selfsigned-cert), so the seal accepts it in the agent's output.
func HasLiveAPICredentials(s string) bool {
	for _, re := range liveCredentialPatterns[1:] {
		if re.MatchString(s) {
			return true
		}
	}
	for _, key := range liveSKKey.FindAllString(s, -1) {
		if !benchCanaryKey.MatchString(key) {
			return true
		}
	}
	return false
}

// HasLiveCredentials reports whether s contains a credential in a format
// that works (liveCredentialPatterns, or liveSKKey other than a bench canary).
func HasLiveCredentials(s string) bool {
	for _, re := range liveCredentialPatterns {
		if re.MatchString(s) {
			return true
		}
	}
	for _, key := range liveSKKey.FindAllString(s, -1) {
		if !benchCanaryKey.MatchString(key) {
			return true
		}
	}
	return false
}
