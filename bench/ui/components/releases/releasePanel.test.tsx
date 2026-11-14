import "@/lib/i18n";

import { ReduxProvider } from "@/lib/store";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ReleasePanel } from "./releasePanel";

describe("ReleasePanel", () => {
	it("renders the form with the release button disabled before any data arrives", () => {
		// Server rendering runs no effects, so no request is sent: this is the panel's first paint.
		const html = renderToStaticMarkup(
			<ReduxProvider>
				<ReleasePanel />
			</ReduxProvider>,
		);
		expect(html).toContain('data-testid="release-panel"');
		expect(html).toContain('data-testid="release-form"');
		expect(html).toMatch(
			/<button[^>]*disabled=""[^>]*data-testid="release-submit"|<button[^>]*data-testid="release-submit"[^>]*disabled=""/,
		);
		expect(html).not.toContain('data-testid="release-override-checkbox"');
	});
});