import "@/lib/i18n";

import { mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { OwnTasksCard, benchmarkSlug, nameProblem } from "./ownTasks";

const q = vi.hoisted(() => ({ suites: {} as Record<string, unknown> }));
vi.mock("@/lib/store/apis/suitesLightApi", () => ({ useGetSuitesLightQuery: () => q.suites }));
vi.mock("@/lib/store/apis/suiteAuthoringApi", async (orig) => ({
	...(await orig<typeof import("@/lib/store/apis/suiteAuthoringApi")>()),
	useCreateSuiteMutation: () => mutation(),
}));
vi.mock("@/lib/store/apis/suiteBuildApi", async (orig) => ({
	...(await orig<typeof import("@/lib/store/apis/suiteBuildApi")>()),
	useSetSuiteGraderMutation: () => mutation(),
}));
vi.mock("@/lib/store/hooks", () => ({ useAppDispatch: () => () => undefined, useAppSelector: () => undefined }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const suites = [{ id: "coding-core", builtin: true }, { id: "marshmallow-issues" }, { id: "marshmallow-issues-2" }];

beforeEach(() => {
	q.suites = okQuery({ suites });
});

describe("nameProblem (N100)", () => {
	it("accepts a name that makes a free id", () => {
		expect(benchmarkSlug("Bad Name!")).toBe("bad-name");
		expect(nameProblem("bad-name", "Bad Name!", suites)).toBe("");
		expect(nameProblem("", "", suites)).toBe("");
	});
	it("refuses a built-in id, an id the org has, and a name with no letters or digits", () => {
		expect(nameProblem("coding-core", "coding-core", suites)).toBe("coding-core is a built-in benchmark. Pick another name.");
		expect(nameProblem("marshmallow-issues", "marshmallow-issues", suites)).toBe(
			"Your organization already has a benchmark called marshmallow-issues. Pick another name, for example marshmallow-issues-3.",
		);
		expect(nameProblem("", "!!!", suites)).toBe("Use letters or digits in the name.");
	});
	it("skips the existing-id check while the list loads", () => {
		expect(nameProblem("coding-core", "coding-core", [])).toBe("");
	});
});

describe("OwnTasksCard name check (N100)", () => {
	const card = (name: string) => render(<OwnTasksCard onAdd={() => undefined} initialOpen initialName={name} />);
	it("disables Create and says why for a taken or empty id", () => {
		for (const name of ["coding-core", "marshmallow-issues", "!!!"]) {
			const html = card(name);
			expect(tagOf(html, "step3-own-create")).toContain('disabled=""');
			expect(html).toContain('data-testid="step3-own-name-problem"');
		}
	});
	it("keeps Create on for a free id and says the id was made from the name", () => {
		const html = card("Bad Name!");
		expect(tagOf(html, "step3-own-create")).not.toContain('disabled=""');
		expect(html).not.toContain("step3-own-name-problem");
		expect(html).toContain("Benchmark id: bad-name (made from the name)");
	});
	it("lets the server decide while the list loads", () => {
		q.suites = { data: undefined, isLoading: true };
		expect(tagOf(card("coding-core"), "step3-own-create")).not.toContain('disabled=""');
	});
});