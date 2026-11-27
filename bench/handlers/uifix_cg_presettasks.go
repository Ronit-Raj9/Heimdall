package handlers

// presetTasks sums the tasks the benchmarks run and reports whether some
// benchmark ran fewer than asked (it has no more ready tasks). Skipped
// benchmarks count nothing.
func presetTasks(per []EvalBenchmarkEstimate, asked int) (int, bool) {
	n, all := 0, false
	for _, b := range per {
		if b.Skipped != "" {
			continue
		}
		n += b.Tasks
		if asked > 0 && b.Tasks < asked {
			all = true
		}
	}
	return n, all
}
