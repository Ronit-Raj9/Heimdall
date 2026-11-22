import "@/lib/i18n";

import { agentFailurePatternsRequest, normalizeFailurePatterns } from "@/lib/store/apis/failurePatternsApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { groupName, WhyFilters, whyFilterArgs, withSelected } from "./whyAgentsFailView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const noop = () => undefined;

describe("why agents fail filters", () => {
	it("shows version and benchmark selects and the practice box, practice off by default", () => {
		const html = renderToStaticMarkup(
			<WhyFilters
				version={null}
				versions={[1, 2]}
				benchmark={null}
				benchmarks={["hd-swe-verified"]}
				practice={false}
				onVersion={noop}
				onBenchmark={noop}
				onPractice={noop}
			/>,
		);
		expect(html).toContain('data-testid="why-version"');
		expect(html).toContain('data-testid="why-benchmark"');
		expect(html).toContain(">Version<");
		expect(html).toContain(">Benchmark<");
		expect(html).toContain("Include practice runs");
		expect(html).toMatch(/<input type="checkbox"(?![^>]*checked)[^>]*data-testid="why-include-practice"/);
	});

	it("builds the request from the URL state: filters only when set, practice left out by default", () => {
		expect(whyFilterArgs("codex", null, null, null)).toEqual({ agent: "codex" });
		expect(whyFilterArgs("codex", 1, "hd-swe-verified", true)).toEqual({
			agent: "codex",
			version: 1,
			benchmark: "hd-swe-verified",
			includePractice: true,
		});
		expect(agentFailurePatternsRequest("codex")).toBe("/agents/codex/failure-patterns");
		expect(agentFailurePatternsRequest(whyFilterArgs("codex", null, null, false))).toBe("/agents/codex/failure-patterns");
		expect(agentFailurePatternsRequest(whyFilterArgs("codex", 2, "swe lite", true))).toEqual({
			url: "/agents/codex/failure-patterns",
			params: { version: "2", benchmark: "swe lite", include_practice: "true" },
		});
	});

	it("keeps a URL value the server's choices lack, and the choices through normalizing", () => {
		expect(withSelected([1, 2], 3)).toEqual([3, 1, 2]);
		expect(withSelected([1, 2], 2)).toEqual([1, 2]);
		expect(withSelected(undefined, null)).toEqual([]);
		const body = normalizeFailurePatterns({ agent: "codex", groups: [], versions: [1], benchmarks: ["hd-swe-verified"] });
		expect(body.versions).toEqual([1]);
		expect(body.benchmarks).toEqual(["hd-swe-verified"]);
	});

	it("names a version group v<N>", () => {
		expect(groupName({ version: 1, label: "v1" }, "Unsaved config")).toBe("v1");
	});
});
