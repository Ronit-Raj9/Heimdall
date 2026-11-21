package auth

import (
	"os"
	"strings"
	"sync"
)

// ServerAdminsEnv lists the server admins' emails, comma-separated.
const ServerAdminsEnv = "HEIMDALL_SERVER_ADMINS"

var (
	serverAdminsOnce sync.Once
	serverAdmins     map[string]bool
)

func loadServerAdmins() {
	serverAdmins = map[string]bool{}
	for _, e := range strings.Split(os.Getenv(ServerAdminsEnv), ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			serverAdmins[e] = true
		}
	}
}

// IsServerAdmin reports whether p is a signed-in user whose email is listed
// in HEIMDALL_SERVER_ADMINS (read once). Tokens, runners, anonymous and local
// principals are never server admins.
func IsServerAdmin(p *Principal) bool {
	if p == nil || p.Kind != KindUser {
		return false
	}
	return isServerAdminEmail(p.Email)
}

func isServerAdminEmail(email string) bool {
	serverAdminsOnce.Do(loadServerAdmins)
	email = strings.ToLower(strings.TrimSpace(email))
	return email != "" && serverAdmins[email]
}
