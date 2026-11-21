package utils

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X23: Heimdall's own model calls through Bifrost (the judge, the visual
// judge, the model-played user) carry x-bf-maxim-* headers, so a gateway
// with the Maxim plugin logs them as Heimdall's, grouped by experiment and
// tagged with the run (https://docs.getbifrost.ai/features/observability/maxim:
// session-id, trace-name and generation-name are reserved; every other
// x-bf-maxim-* header becomes a tag). A gateway without the plugin ignores
// them.

// MaximHeaderPrefix starts every Bifrost Maxim plugin header.
const MaximHeaderPrefix = "x-bf-maxim-"

// Roles of Heimdall's own calls.
const (
	MaximRoleJudge       = "judge"
	MaximRoleVisualJudge = "visual_judge"
	MaximRoleSimUser     = "simulated_user"
)

var maximValueRe = regexp.MustCompile(`[^A-Za-z0-9_.:@/-]`)

// MaximHeaders are the headers of one of Heimdall's own calls for a run:
// the experiment as the Maxim session, a readable generation name, and the
// run, task, arm and role as tags. Values are cut to a safe charset. A run
// context without a run gives none.
func MaximHeaders(rc *schemas.RunContext, role string) map[string]string {
	if rc == nil || rc.Run == nil || rc.Run.RunID == "" {
		return nil
	}
	h := map[string]string{
		MaximHeaderPrefix + "heimdall-run":  rc.Run.RunID,
		MaximHeaderPrefix + "heimdall-role": role,
		MaximHeaderPrefix + "source":        "heimdall",
	}
	if rc.Run.ExpID != "" {
		h[MaximHeaderPrefix+"session-id"] = "heimdall-" + rc.Run.ExpID
		h[MaximHeaderPrefix+"heimdall-experiment"] = rc.Run.ExpID
	}
	if rc.Slot != nil {
		if rc.Slot.Task != "" {
			h[MaximHeaderPrefix+"heimdall-task"] = rc.Slot.Task
		}
		if rc.Slot.Arm != "" {
			h[MaximHeaderPrefix+"heimdall-arm"] = rc.Slot.Arm
		}
	}
	for k, v := range h {
		h[k] = cleanMaximValue(v)
	}
	h[MaximHeaderPrefix+"generation-name"] = "heimdall " + cleanMaximValue(role)
	return h
}

type maximCtxKey struct{}

// WithMaximHeaders returns ctx carrying headers for the gateway calls made
// under it.
func WithMaximHeaders(ctx context.Context, h map[string]string) context.Context {
	if len(h) == 0 {
		return ctx
	}
	return context.WithValue(ctx, maximCtxKey{}, h)
}

// MaximHeadersFrom returns the headers WithMaximHeaders put on ctx, sorted
// by name, keeping only x-bf-maxim-* names.
func MaximHeadersFrom(ctx context.Context) [][2]string {
	h, _ := ctx.Value(maximCtxKey{}).(map[string]string)
	out := make([][2]string, 0, len(h))
	for k, v := range h {
		if strings.HasPrefix(strings.ToLower(k), MaximHeaderPrefix) && v != "" {
			out = append(out, [2]string{k, v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

func cleanMaximValue(v string) string {
	v = maximValueRe.ReplaceAllString(strings.ReplaceAll(v, " ", "_"), "")
	if len(v) > 128 {
		v = v[:128]
	}
	return v
}
