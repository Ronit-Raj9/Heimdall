package suite

import "github.com/maximhq/bifrost/bench/schemas"

// Builtin lists every suite loader shipped with the bench. benchmarksRoot is
// the folder holding one sub-folder per pack, used to resolve id refs such as
// "coding-core@1.0.0". Removing a line removes the loader.
func Builtin(benchmarksRoot string) []schemas.SuiteLoader {
	return []schemas.SuiteLoader{
		newLocalLoader(benchmarksRoot),
	}
}
