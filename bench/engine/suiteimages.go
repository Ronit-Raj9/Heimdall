package engine

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// suiteImages records the local image id of every image a run of suite
// starts beside the agent and the task (docs/lock-fingerprints.md): the
// suite's base image, the environment's helper image, its tool proxy image
// when a service or an external API needs the proxy, each suite service
// image, and each task's own service image (compose services included), as
// task/<task id>/service/<service id>. The environment checks them again before each run, so a rebuilt tag
// never changes what a frozen experiment measures. A tag that cannot be
// inspected (no docker, image not built yet) gets no entry and one warning,
// never an error. Without any id the result is nil, so the manifest of a
// plan without docker hashes as it did before the field existed.
func (p *Planner) suiteImages(ctx context.Context, s *schemas.Suite) map[string]string {
	if p.reg == nil || s == nil {
		return nil
	}
	env, ok := findPart(p.reg.Environments, s.Environment)
	if !ok {
		return nil
	}
	ins, ok := any(env).(ImageInspector)
	if !ok {
		return nil
	}
	tags := suiteImageTags(s, env)
	out := map[string]string{}
	for _, role := range sortedKeys(tags) {
		tag := tags[role]
		id, err := ins.ImageID(ctx, tag)
		if err != nil || id == "" {
			msg := "no id reported"
			if err != nil {
				msg = err.Error()
			}
			p.logger.Warn("image not inspected; the manifest records no id for it", "role", role, "image", tag, "error", msg)
			continue
		}
		out[role] = id
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// suiteImageTags maps each image role of s to its tag: base, the helper and
// tool proxy roles the environment reports (ImageRoler; the tool proxy only
// when the suite uses it) and service/<id> for each suite service. Roles
// without a tag are left out. A task's own services use
// task/<task id>/service/<id>, so two tasks may reuse a service id.
func suiteImageTags(s *schemas.Suite, env schemas.Environment) map[string]string {
	tags := map[string]string{}
	if s.BaseImage.Tag != "" {
		tags[suite.ImageBase] = s.BaseImage.Tag
	}
	if r, ok := any(env).(ImageRoler); ok {
		for role, tag := range r.ImageRoles() {
			if tag == "" || (role == suite.ImageToolProxy && !suiteUsesToolProxy(s)) {
				continue
			}
			tags[role] = tag
		}
	}
	for _, sv := range s.Services {
		tag := sv.Image
		if sv.Build != nil && sv.Build.Tag != "" {
			tag = sv.Build.Tag
		}
		if sv.ID != "" && tag != "" {
			tags["service/"+sv.ID] = tag
		}
	}
	for _, t := range s.Tasks {
		if t == nil {
			continue
		}
		for _, sv := range t.Services {
			tag := sv.Image
			if sv.Build != nil && sv.Build.Tag != "" {
				tag = sv.Build.Tag
			}
			if t.ID != "" && sv.ID != "" && tag != "" {
				tags[TaskServiceRole(t.ID, sv.ID)] = tag
			}
		}
	}
	return tags
}

// suiteUsesToolProxy reports whether any run of s starts the tool proxy: a
// suite or task service, or an external API.
func suiteUsesToolProxy(s *schemas.Suite) bool {
	if len(s.Services) > 0 || len(s.External) > 0 {
		return true
	}
	for _, t := range s.Tasks {
		if len(t.Services) > 0 || len(t.External) > 0 {
			return true
		}
	}
	return false
}

// TaskServiceRole is the Manifest.SuiteImages role of a task's own service
// image. environments/image.go reads the same shape.
func TaskServiceRole(taskID, serviceID string) string {
	return "task/" + taskID + "/service/" + serviceID
}
