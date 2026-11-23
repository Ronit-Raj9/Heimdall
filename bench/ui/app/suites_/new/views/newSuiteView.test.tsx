import "@/lib/i18n";

import { mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import NewSuiteView from "./newSuiteView";

vi.mock("@tanstack/react-router", async () => ({
	...(await import("@/lib/testing/routerMock")).routerMock,
	useNavigate: () => () => undefined,
}));
vi.mock("@/lib/store/apis/suiteAuthoringApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/suiteAuthoringApi")>()),
	useGetSuiteTemplatesQuery: () => okQuery({ default: "python-pytest", templates: [{ id: "python-pytest", scenario: false }, { id: "qa", scenario: true, sample: "hotel-cap" }] }),
	useCreateSuiteMutation: () => mutation(),
}));

// N265: the id check reads the light suite list.
vi.mock("@/lib/store/apis/suitesLightApi", () => ({
	useGetSuitesLightQuery: () =>
		okQuery({
			mode: "live",
			suites: [
				{ id: "coding-core", version: "1.0.0", builtin: true, source: "library", task_count: 40 },
				{ id: "faq", version: "1.0.0", task_count: 4 },
			],
		}),
}));
describe("NewSuiteView", () => {
	it("starts from the team's own questions: no template list, no sample task", () => {
		const html = render(<NewSuiteView />);
		expect(html).toContain('data-testid="suites-new-form"');
		expect(html).toMatch(/checked="" value="questions"/);
		expect(html).toContain('data-testid="suites-new-start-code"');
		expect(html).not.toContain('data-testid="suites-new-templates"');
	});
});

describe("NewSuiteView id check (N265)", () => {
	it("disables Create for an id an organization benchmark has, with a link to it", () => {
		const html = render(<NewSuiteView initialId="faq" />);
		expect(html).toContain("A benchmark with this id already exists. Pick another id, or");
		expect(html).toMatch(/<a [^>]*href="\/suites\/faq"[^>]*>open faq<\/a>\./);
		expect(tagOf(html, "suites-new-create")).toContain('disabled=""');
	});

	it("disables Create for a built-in benchmark id", () => {
		const html = render(<NewSuiteView initialId="coding-core" />);
		expect(html).toContain("coding-core is a built-in benchmark. Pick another id.");
		expect(tagOf(html, "suites-new-create")).toContain('disabled=""');
	});

	it("keeps Create on for a free id", () => {
		const html = render(<NewSuiteView initialId="my-new-one" />);
		expect(html).not.toContain('data-testid="suites-new-id-taken"');
		expect(tagOf(html, "suites-new-create")).not.toContain('disabled=""');
	});
});
