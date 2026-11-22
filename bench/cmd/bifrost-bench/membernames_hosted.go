package main

import (
	"context"
	"strings"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/server"
)

// The run controls name the users in their change log and pause record
// (J6.log-user): the hosted server hands the handlers a resolver over the
// organization's members. The area adds no routes.
func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		if env.db != nil {
			handlers.SetMemberNames(hostedMemberNames(&orgService{env: env}))
		}
		return nil, nil
	})
}

// hostedMemberNames maps each member's user id, and the member's lower-case
// email, to the display text: the name, else the part of the email before
// "@", else the id. The full email is never the display text (fix wave 2,
// D4): members see each other by name only.
func hostedMemberNames(s *orgService) func(ctx context.Context, orgID string) (map[string]string, error) {
	return func(ctx context.Context, orgID string) (map[string]string, error) {
		ms, err := s.Members(ctx, orgID)
		if err != nil {
			return nil, err
		}
		out := make(map[string]string, 2*len(ms))
		for _, m := range ms {
			name := m.Name
			if name == "" {
				name, _, _ = strings.Cut(m.Email, "@")
			}
			if name == "" {
				name = m.UserID
			}
			out[m.UserID] = name
			if e := strings.ToLower(strings.TrimSpace(m.Email)); e != "" {
				out[e] = name
			}
		}
		return out, nil
	}
}
