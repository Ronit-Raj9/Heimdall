# Benchmarks

One folder per suite. Create one with

    bifrost-bench suite init <suite> -template python-pytest|node-test|go-test|generic-reward

and add tasks cut from your own repositories with

    bifrost-bench task init -suite <suite> -id <task> -repo <git repo> -base <commit> -reference <commit>

Each suite holds `suite.yaml`, `base/Dockerfile` (its toolchain image) and
`tasks/<task>/`. `bifrost-bench task admit <suite>/<task>` proves a task is
fit to measure with and records the result in `suite.lock`.
