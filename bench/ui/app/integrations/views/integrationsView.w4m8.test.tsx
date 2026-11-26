import "@/lib/i18n";

import { errorQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { PanelLoadError } from "./integrationsView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("integrations load errors", () => {
	it("names the problem and offers Retry for a server error", () => {
		const html = render(
			<PanelLoadError
				title="Could not load webhooks"
				error={errorQuery(500, "boom").error}
				onRetry={() => undefined}
				testId="webhooks-load-error"
			/>,
		);
		expect(html).toContain("Could not load webhooks");
		expect(html).toContain('data-testid="webhooks-load-error-retry"');
	});

	it("offers no Retry where a retry cannot help", () => {
		const html = render(
			<PanelLoadError
				title="Could not load exports"
				error={errorQuery(404, "gone").error}
				onRetry={() => undefined}
				testId="exports-load-error"
			/>,
		);
		expect(html).not.toContain("exports-load-error-retry");
	});
});