package sharelinks

import (
	"encoding/json"
	"strings"

	"github.com/maximhq/bifrost/bench/secrets"
)

// Hidden replaces a value the share page leaves out.
const Hidden = "[hidden]"

// secretKeys are field names whose value is always removed from a shared
// page, whatever it holds. Matched on the lower-cased name.
var secretKeys = map[string]bool{
	"secret": true, "secrets": true, "password": true, "passwd": true, "token": true,
	"api_key": true, "apikey": true, "authorization": true, "auth": true, "headers": true,
	"env": true, "environment": true, "credentials": true, "credential": true,
	"access_key": true, "secret_key": true, "private_key": true, "bearer": true,
	"cookie": true, "cookies": true, "created_by": true, "revoked_by": true, "actor": true,
	"key_value": true, "value_hash": true, "webhook_url": true,
	// Fix wave 3 (N293): gateway keys, people, worker names and hosts. A
	// person's display name (*_name) stays.
	"virtual_key_id": true, "uploaded_by": true, "requested_by": true, "updated_by": true,
	"started_by": true, "approved_by": true, "reviewed_by": true, "labeller": true, "author": true,
	"email": true, "worker": true, "worker_id": true, "host": true, "by": true,
}

// secretSuffixes catch prefixed variants (OPENAI_API_KEY, x_secret).
var secretSuffixes = []string{"_secret", "_password", "_token", "_api_key", "_apikey", "_private_key", "_access_key", "_secret_key", "_credentials",
	// Fix wave 3 (N293): every *_path field (stdout_path and the like) holds
	// a file path on a worker or the server.
	"_path"}

// promptKeys are fields that hold prompts, instructions, agent text or tool
// arguments, blanked when a link hides prompts.
var promptKeys = map[string]bool{
	"prompt": true, "prompts": true, "system_prompt": true, "instruction": true, "instructions": true,
	"messages": true, "message": true, "input": true, "inputs": true, "output": true, "outputs": true,
	"content": true, "text": true, "transcript": true, "summary": true, "target": true,
	"args": true, "arguments": true, "query": true, "response": true, "completion": true,
	"stdout": true, "stderr": true, "diff": true, "patch": true, "answer": true, "question": true,
}

func secretKey(k string) bool {
	k = strings.ToLower(k)
	if secretKeys[k] {
		return true
	}
	for _, s := range secretSuffixes {
		if strings.HasSuffix(k, s) {
			return true
		}
	}
	return false
}

// Redact returns v (decoded JSON) with every secret removed: fields whose
// name says they hold one are replaced by secrets.Mask, and every string is
// passed through secrets.Redact, which masks key shapes (sk-..., ghp_...,
// JWTs, user:password@ in URLs, NAME_KEY=value). With hidePrompts, fields
// that hold prompts, instructions, agent text or tool arguments are
// replaced by Hidden as well.
func Redact(v any, hidePrompts bool) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			switch {
			case secretKey(k):
				if val == nil {
					out[k] = nil
				} else {
					out[k] = secrets.Mask
				}
			case hidePrompts && promptKeys[strings.ToLower(k)]:
				if val == nil {
					out[k] = nil
				} else if _, ok := val.(string); ok {
					if val == "" {
						out[k] = ""
					} else {
						out[k] = Hidden
					}
				} else {
					out[k] = Hidden
				}
			default:
				out[k] = Redact(val, hidePrompts)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = Redact(val, hidePrompts)
		}
		return out
	case string:
		return secrets.Redact(t)
	}
	return v
}

// RedactJSON decodes body, redacts it and returns the value. A body that is
// not JSON becomes nil.
func RedactJSON(body []byte, hidePrompts bool) any {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil
	}
	return Redact(v, hidePrompts)
}
