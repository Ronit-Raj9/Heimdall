package main

import (
	"context"

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

// hostedMemberNames maps each member's user id to the name, else the email,
// else the id.
func hostedMemberNames(s *orgService) func(ctx context.Context, orgID string) (map[string]string, error) {
	return func(ctx context.Context, orgID string) (map[string]string, error) {
		ms, err := s.Members(ctx, orgID)
		if err != nil {
			return nil, err
		}
		out := make(map[string]string, len(ms))
		for _, m := range ms {
			switch {
			case m.Name != "":
				out[m.UserID] = m.Name
			case m.Email != "":
				out[m.UserID] = m.Email
			default:
				out[m.UserID] = m.UserID
			}
		}
		return out, nil
	}
}
