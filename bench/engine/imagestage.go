package engine

import "github.com/maximhq/bifrost/bench/schemas"

// StageImage is the live stage of a slot whose lease waits for a container
// image to be prepared (wave 4, F1.1). Display only.
const StageImage schemas.Stage = "image"
