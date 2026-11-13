# t05 hidden tests: requirement map

All tests are in `verifier/tests/test_window_boundary.py`. Sentence numbers refer to the numbered list in `instruction.md`.

| Test | Instruction sentence it checks |
|---|---|
| `test_request_exactly_window_old_no_longer_counts` | 1: "A request made exactly `window` seconds ago is outside the window and no longer counts"; 2: admits while fewer than `limit` are inside, refuses at `limit`. |
| `test_request_just_inside_window_still_counts` | 1: "`now - window < t <= now`" and "a request made less than `window` seconds ago still counts" (times 9.75, 10, 10.25, 10.5 against requests at 0 and 0.5). |
| `test_remaining_uses_the_same_boundary` | 3: "`remaining(key)` returns `limit` minus the number of requests for `key` inside the window at the current time. Calling it does not change anything." Also "Different keys never affect each other." |
| `test_retry_after_is_exact_and_sufficient` | 4: "returns `0.0` when a request for `key` would be admitted now. Otherwise it returns the exact number of seconds until the oldest request that still counts leaves the window, so that moving the clock forward by exactly that amount makes the next request admissible." |
| `test_refused_requests_are_not_recorded` | 2: "Otherwise it returns `False` and records nothing: a refused request never counts against later requests." |
| `test_simulate_cli_follows_the_rule` | 5: CLI follows the same rule; output line format `KEY@SECONDS allow|deny remaining=R`. Also "Different keys never affect each other." |

Values are chosen so every subtraction is exact in binary floating point (0, 0.5, 4, 9.75, 10, 10.25, 29.5, 30, 59.75).
