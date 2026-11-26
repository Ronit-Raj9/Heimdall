import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { EstimateLine } from "./compareModelsForm";

const old = { runs: 4, est_cost_usd: { low: 0.01, high: 0.02, known: true }, est_minutes: { low: 0.6, high: 1.4, known: true } };

describe("compare models estimate line (wave 4, p02-5)", () => {
	it("shows the waiting text while a new estimate loads, never the old sentence", () => {
		const html = renderToStaticMarkup(<EstimateLine state={{ currentData: undefined, isFetching: true, isError: false }} />);
		expect(html).toContain("Working out the cost...");
		expect(html).not.toContain("4 runs");
	});
	it("shows the current estimate as one sentence", () => {
		const html = renderToStaticMarkup(<EstimateLine state={{ currentData: old as never, isFetching: false, isError: false }} />);
		expect(html).toContain("4 runs. $0.01 to $0.02. About 1 min.");
	});
	it("says the estimate failed", () => {
		const html = renderToStaticMarkup(<EstimateLine state={{ currentData: undefined, isFetching: false, isError: true }} />);
		expect(html).toContain("Could not estimate the cost.");
	});
});