package auth

import (
	"context"
	"strings"
)

// SecretPrincipal resolves a raw credential sent outside the Authorization
// bearer header, as a docker client sends its password to the library
// registry's token endpoint (handlers/registrytoken.go, bench/imagepin): an
// API token (hm_...) or a runner token (hmr_...), checked exactly as the
// bearer path checks them. Anything else is ErrInvalidToken.
func (s *Service) SecretPrincipal(ctx context.Context, raw string) (*Principal, error) {
	dctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	switch {
	case strings.HasPrefix(raw, RunnerTokenPrefix):
		return verifyRunner(dctx, raw)
	case strings.HasPrefix(raw, APITokenPrefix):
		return s.tokenPrincipal(dctx, raw)
	}
	return nil, ErrInvalidToken
}
