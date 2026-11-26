import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import AddSuiteView from "./addSuiteView";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, children, ...rest }: { to: string; children: React.ReactNode }) => (
		<a href={to} {...rest}>
			{children}
		</a>
	),
}));
vi.mock("@/lib/store", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store")>()),
	useGetImportsQuery: () => ({ isLoading: true, isFetching: false, isError: false, refetch: () => undefined }),
}));

describe("AddSuiteView (p10-8)", () => {
	it("links to Add benchmark", () => {
		const html = renderToStaticMarkup(<AddSuiteView />);
		expect(html).toContain('href="/suites/add"');
		expect(html).toContain('data-testid="imports-add-file"');
		expect(html).toContain("Add a benchmark from a file or link");
	});
});