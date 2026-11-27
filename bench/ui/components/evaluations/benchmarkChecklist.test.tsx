import "@/lib/i18n";

import type { AgentCompatibility } from "@/lib/store/apis/evaluationsApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { BenchmarkChecklist, type ChecklistSuite } from "./benchmarkChecklist";

const suites: ChecklistSuite[] = [
	{ id: "hd-harbor-bfcl", description: "Function calling", admitted_count: 12, builtin: true, source: "library", track: "terminal" },
	{ id: "longmemeval-s", description: "Long memory", admitted_count: 30, builtin: true, source: "library", track: "memory" },
	{ id: "not-ready", description: "Nothing admitted", admitted_count: 0, builtin: true, source: "library", track: "qa" },
].map((s) => ({ ...s, environment: undefined }) as unknown as ChecklistSuite);

const compatibility: AgentCompatibility = {
	agent: "zz-test-loop",
	benchmarks: [
		{ id: "hd-harbor-bfcl", ok: true, requires: [], missing: [] },
		{ id: "longmemeval-s", ok: false, requires: ["sessions"], missing: ["sessions"], reason: "needs sessions" },
	],
} as unknown as AgentCompatibility;

const render = (props: Partial<Parameters<typeof BenchmarkChecklist>[0]> = {}) =>
	renderToStaticMarkup(<BenchmarkChecklist suites={suites} selected={[]} onToggle={() => undefined} testIdPrefix="t" {...props} />);

describe("BenchmarkChecklist (N124)", () => {
	it("lists only benchmarks with ready tasks, by display name, with their ready task count", () => {
		const html = render();
		expect(html).toContain('data-testid="t-benchmark-hd-harbor-bfcl"');
		expect(html).not.toContain('data-testid="t-benchmark-not-ready"');
		expect(html).toContain("12 ready tasks");
		expect(html).not.toMatch(/>hd-harbor-bfcl</);
		expect(html).toContain('data-testid="t-group-support"');
		expect(html).toContain('data-testid="t-group-memory"');
	});
	it("greys out a benchmark the agent cannot run, with the reason", () => {
		const html = render({ compatibility, selected: ["longmemeval-s"] });
		expect(html).toMatch(/data-testid="t-benchmark-longmemeval-s" data-disabled="true"/);
		expect(html).toContain("Needs an agent that can use sessions");
		expect(html).not.toMatch(/data-testid="t-benchmark-hd-harbor-bfcl" data-disabled/);
	});
	it("waits while compatibility is checked", () => {
		const html = render({ checking: true });
		expect(html).toContain("Checking whether your agent can run it...");
	});
	it("says so when no benchmark is ready", () => {
		expect(render({ suites: [suites[2]] })).toContain("No benchmark has ready tasks yet.");
		expect(render({ suites: [], loading: true })).toContain("Loading benchmarks...");
	});
});