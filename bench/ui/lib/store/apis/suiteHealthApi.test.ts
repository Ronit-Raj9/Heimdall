import { describe, expect, it } from "vitest";
import { normalizeSuiteHealth } from "./benchApi";
import { resolveFixture } from "./fixtures";

describe("normalizeSuiteHealth", () => {
	it("reads Go nil lists and maps as empty", () => {
		const body = normalizeSuiteHealth({
			mode: "live",
			suite: "s",
			tasks: [
				{
					task: "t1",
					quarantine: undefined,
					flags: null,
					verifier: { flaky: false, checks: 0, evidence: null },
					variance: { per_config: null },
				},
			],
			counts: null,
			notes: null,
		});
		expect(body.counts).toEqual({});
		expect(body.notes).toEqual([]);
		expect(body.tasks[0].flags).toEqual([]);
		expect(body.tasks[0].quarantine).toBeNull();
		expect(body.tasks[0].verifier.evidence).toEqual([]);
		expect(body.tasks[0].variance.per_config).toEqual([]);
		expect(body.mode).toBe("live");
	});

	it("keeps a full report as it is", () => {
		const evidence = [{ kind: "oracle_failed", detail: "d" }];
		const body = normalizeSuiteHealth({
			tasks: [{ task: "t1", flags: ["flaky_verifier"], verifier: { flaky: true, checks: 1, evidence }, variance: { per_config: [] } }],
			counts: { flaky_verifier: 1 },
			notes: ["n"],
		});
		expect(body.tasks[0].verifier.evidence).toEqual(evidence);
		expect(body.tasks[0].flags).toEqual(["flaky_verifier"]);
		expect(body.counts).toEqual({ flaky_verifier: 1 });
	});

	it("survives an empty body", () => {
		expect(normalizeSuiteHealth(null).tasks).toEqual([]);
	});
});

describe("suite health fixture route", () => {
	it("is keyed by suite id and refuses unsafe ids", () => {
		expect(resolveFixture("GET", "/api/bench/suites/acme-payments/health")).toBe("suites.health.acme-payments.json");
		expect(resolveFixture("GET", "/suites/..%2F/health")).toBeNull();
		expect(resolveFixture("POST", "/suites/acme-payments/health")).toBeNull();
	});
});