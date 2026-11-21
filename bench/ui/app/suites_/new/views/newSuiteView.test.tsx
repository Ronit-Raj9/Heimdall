import "@/lib/i18n";

import { mutation, okQuery, render } from "@/lib/testing/render";
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

describe("NewSuiteView", () => {
	it("offers the templates with the default checked", () => {
		const html = render(<NewSuiteView />);
		expect(html).toContain('data-testid="suites-new-form"');
		expect(html).toContain("python-pytest");
		expect(html).toContain(">qa<");
		expect(html).toMatch(/checked="" value="python-pytest"/);
	});
});
