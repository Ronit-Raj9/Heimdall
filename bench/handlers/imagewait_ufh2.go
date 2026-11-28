package handlers

import (
	"math"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// The wait before the first run while per-task agent images are built
// (lane H2). Benchmarks whose tasks bring their own image (Terminal-Bench 2,
// BFCL and the other Harbor imports) need one agent image per task
// (engine.ResolveTaskAgentImage on utils.TaskImage), the worker builds them
// one at a time, and no run starts before its image exists.

// TaskImageBuildSeconds is the time one task image takes to build on the
// worker. Measured 2026-11-28: 24 images in 29 minutes (about 75 seconds
// each); a busy worker took 4 to 5 minutes per image.
const TaskImageBuildSeconds = 75

// ImageWait is the expected wait. The worker builds each task's image right
// before that task's first run, so the first run waits for one image
// (Minutes) and all images together take TotalMinutes. UpTo is always true here: the images are
// built and kept on the worker, and the control plane cannot ask which of
// them already exist, so Images counts every image the runs may need.
type ImageWait struct {
	Images       int  `json:"images"`
	Minutes      int  `json:"minutes"`       // before the first run: one image
	TotalMinutes int  `json:"total_minutes"` // every image, spread over the runs
	UpTo         bool `json:"up_to"`
}

// imageWait is nil when no image must be built.
func imageWait(images int) *ImageWait {
	if images <= 0 {
		return nil
	}
	return &ImageWait{Images: images, Minutes: int(math.Ceil(float64(TaskImageBuildSeconds) / 60)),
		TotalMinutes: int(math.Ceil(float64(images*TaskImageBuildSeconds) / 60)), UpTo: true}
}

// ownImageTasks is how many of taskIDs bring their own image, when the
// benchmark builds an agent image per task (needsTaskImageAgent).
func ownImageTasks(st *schemas.Suite, taskIDs []string) int {
	if !needsTaskImageAgent(st, taskIDs) {
		return 0
	}
	n := 0
	for _, id := range taskIDs {
		if _, ok, err := utils.TaskImage(st.ID, st.TaskByID(id)); err == nil && ok {
			n++
		}
	}
	return n
}
