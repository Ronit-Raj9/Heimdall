package main

import (
	improversuite "github.com/maximhq/bifrost/bench/benchmarks/heimdall-improver"

	// The improver's evidence builder registers itself as the default
	// evidence builder of improvement runs when its package is linked in.
	_ "github.com/maximhq/bifrost/bench/improve/evidence"
)

func init() { improverSuiteFS = improversuite.FS }
