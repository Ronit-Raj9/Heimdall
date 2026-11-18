# judgescript

A scripted judge model for $0 tests of judged grading and judge calibration: an Anthropic Messages and OpenAI Chat Completions endpoint whose verdict comes from a JSON script (by prompt sha256, by graded answer, by answer substring, or "reference": correct when the answer contains the reference). Point a throwaway fault proxy's upstream at it (`BENCH_FAULTPROXY_UPSTREAM`); never a live stack. Build with `CGO_ENABLED=0 GOWORK=off go build -o judgescript ./calibration/testdata/judgescript` from `bench/`. The script format is in `main.go`; docs/judge-calibration.md shows a full run.

X1 vision grading: `by_image_sha256` maps the sha256 of the first image block of a request (Anthropic `image` blocks and OpenAI `image_url` data URLs, decoded) to a reply, ahead of every other rule, and every request's image hashes are logged; `images.go`, docs/visual-grading.md.
