# Fix the window boundary in the rate limiter

`gatekeeper.SlidingWindowLimiter` gets the edge of its window wrong. A request made exactly `window` seconds ago is still counted, so clients are refused, and told to wait longer, at the moment they should be admitted again.

Make the limiter follow this rule everywhere it decides or reports anything:

1. Time is whatever the limiter's clock returns from `now()`, in seconds. At time `now`, a recorded request made at time `t` is inside the window when `now - window < t <= now`. A request made exactly `window` seconds ago is outside the window and no longer counts; a request made less than `window` seconds ago still counts.
2. `allow(key)` admits the request, records it and returns `True` when fewer than `limit` requests for `key` are inside the window. Otherwise it returns `False` and records nothing: a refused request never counts against later requests.
3. `remaining(key)` returns `limit` minus the number of requests for `key` inside the window at the current time. Calling it does not change anything.
4. `retry_after(key)` returns `0.0` when a request for `key` would be admitted now. Otherwise it returns the exact number of seconds until the oldest request that still counts leaves the window, so that moving the clock forward by exactly that amount makes the next request for `key` admissible.
5. `python -m gatekeeper simulate --limit N --window S EVENT...` follows the same rule. Each event is written `KEY@SECONDS`, events are given in time order, and the command prints one line per event: the event exactly as given, then `allow` or `deny`, then `remaining=R`, where R is `remaining(key)` right after the decision, separated by single spaces.

Different keys never affect each other. `gatekeeper.ManualClock` gives you a clock you can move by hand (`set(t)`, `advance(seconds)`). The existing tests must keep passing.
