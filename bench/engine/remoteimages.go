package engine

// Deploy area: the image ids of a run of a deferred (hosted) manifest.
//
// A hosted server plans without Docker, so its manifest freezes no image
// ids (Manifest.SuiteImages, TaskImages and the arms' ImageID stay empty)
// and nothing pins the images a runner starts. The runner therefore adopts
// those empty values for the hash comparison (adoptUnrepeatable), checks the
// suite lock's image fingerprints again before every slot (a lock that pins
// a local id or a registry digest still refuses another image), resolves the
// ids it is about to run, and sends them with the upload; the coordinator
// seals them in remote/worker.json "images", the index loads them as
// SealedRun.RunnerImages, and instrument runner_images warns when counted
// runs used different ids for the same role.

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Role prefixes of RunnerImages beside the Manifest.SuiteImages roles.
const (
	runnerImageTask  = "task/"
	runnerImageAgent = "agent/"
)

// hostedImagesAdopted reports whether a leased manifest is a hosted plan
// that froze no suite or task image ids: deferred gateway and both maps
// empty. Only then does the runner adopt the empty maps; a manifest that
// has them keeps the exact comparison.
func hostedImagesAdopted(remote *schemas.Manifest) bool {
	return remote != nil && remote.Gateway.Deferred() && len(remote.SuiteImages) == 0 && len(remote.TaskImages) == 0
}

// checkSlotImages re-checks the suite lock's fingerprints, image pins
// included, before a slot of a deferred manifest runs on this worker: the
// experiment's admission checked them once, and an image rebuilt or pulled
// since must not run under a lock that pinned another one. A smoke
// experiment only warns, as everywhere else. Other manifests froze their ids
// at plan time and the environment enforces those.
func (w *Worker) checkSlotImages(ctx context.Context, exp *workerExp) string {
	if exp == nil || exp.m == nil || !exp.m.Gateway.Deferred() {
		return ""
	}
	if err := checkFingerprints(ctx, w.reg, exp.suite, w.logger, exp.m.Experiment.Smoke, "run", exp.m.ExpID); err != nil {
		return err.Error()
	}
	return ""
}

// runnerImages resolves the local image ids one slot of a deferred manifest
// uses on this worker: the suite's image roles (as a local plan records
// them), the task's own image and the arm's agent image. Nil for any other
// manifest. A role whose image cannot be inspected is left out, as a local
// plan leaves it out; Create builds or pulls what is missing.
func (w *Worker) runnerImages(ctx context.Context, exp *workerExp, slot *schemas.Slot) *schemas.RunnerImages {
	if exp == nil || exp.m == nil || !exp.m.Gateway.Deferred() || slot == nil {
		return nil
	}
	ids := map[string]string{}
	for role, id := range NewPlanner(w.reg, "", w.logger).suiteImages(ctx, exp.suite) {
		ids[role] = id
	}
	ins := w.imageInspector(exp.suite)
	if ins != nil && exp.suite != nil {
		if task := exp.suite.TaskByID(slot.Task); task != nil {
			if img, ok, err := utils.TaskImage(exp.suite.ID, task); err == nil && ok && img.Tag != "" {
				if id, err := ins.ImageID(ctx, img.Tag); err == nil && id != "" {
					ids[runnerImageTask+task.ID] = id
				}
			}
		}
		if arm, ok := exp.m.Arms[slot.Arm]; ok && arm.Config.Image != "" {
			image := arm.Config.Image
			if tag, _ := taskAgentImage(&arm, slot.Task); tag != "" { // the per-task agent image the slot starts (audit 8-B-06)
				image = tag
			}
			if id, err := ins.ImageID(ctx, image); err == nil && id != "" {
				ids[runnerImageAgent+slot.Arm] = id
			}
		}
	}
	return &schemas.RunnerImages{IDs: ids}
}

// imageInspector is the suite environment's image inspector, nil when the
// environment has none (no Docker) or is not registered.
func (w *Worker) imageInspector(s *schemas.Suite) ImageInspector {
	if w.reg == nil || s == nil {
		return nil
	}
	env, ok := findPart(w.reg.Environments, s.Environment)
	if !ok {
		return nil
	}
	ins, _ := any(env).(ImageInspector)
	return ins
}
