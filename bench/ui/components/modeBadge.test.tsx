import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ModeBadge } from "./modeBadge";

describe("ModeBadge", () => {
	const cases = [
		{ mode: "live", label: "LIVE" },
		{ mode: "recorded", label: "RECORDED" },
		{ mode: "synthetic", label: "SYNTHETIC" },
	] as const;
	for (const { mode, label } of cases) {
		it(mode, () => {
			const html = renderToStaticMarkup(<ModeBadge mode={mode} />);
			expect(html).toContain(`data-testid="mode-badge-${mode}"`);
			expect(html).toContain(`>${label}<`);
		});
	}

	it("offline when the server did not answer", () => {
		const html = renderToStaticMarkup(<ModeBadge mode={undefined} isError />);
		expect(html).toContain('data-testid="mode-badge-offline"');
	});

	it("loading", () => {
		expect(renderToStaticMarkup(<ModeBadge mode={undefined} isLoading />)).toContain('data-testid="mode-badge-loading"');
	});
});