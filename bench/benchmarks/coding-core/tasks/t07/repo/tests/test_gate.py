from gatekeeper import Decision, Gate, ManualClock, default_config


def config(**limiter):
    cfg = default_config()
    cfg["limiter"].update(limiter)
    return cfg


def test_gate_applies_limit():
    gate = Gate(config(limit=2, window=30.0), ManualClock())
    assert gate.check("k").allowed
    second = gate.check("k")
    assert second == Decision(allowed=True, remaining=0, retry_after=second.retry_after)
    assert not gate.check("k").allowed


def test_exempt_keys_always_admitted():
    gate = Gate(config(limit=1, exempt_keys=["health"]), ManualClock())
    for _ in range(5):
        assert gate.check("health").allowed
    assert gate.check("other").allowed
    assert not gate.check("other").allowed


def test_disabled_gate_admits_everything():
    gate = Gate(config(limit=1, enabled=False), ManualClock())
    assert all(gate.check("k").allowed for _ in range(10))


def test_defaults_when_no_config():
    gate = Gate(clock=ManualClock())
    assert gate.limiter.limit == 60
    assert gate.limiter.window == 60.0
