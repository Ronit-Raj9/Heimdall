import "@/lib/i18n";

import { errorQuery, mutation, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { EmailPanel } from "./emailPanel";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

vi.mock("@/lib/store/apis/integrationsApi", async (orig) => ({
	...(await orig<typeof import("@/lib/store/apis/integrationsApi")>()),
	useGetEmailSettingsQuery: () => errorQuery(500, "boom"),
	usePutEmailSettingsMutation: () => mutation(),
	useTestEmailMutation: () => mutation(),
}));

describe("email panel load error", () => {
	it("names the problem and offers Retry", () => {
		const html = render(<EmailPanel />);
		expect(html).toContain("Could not load email settings");
		expect(html).toContain('data-testid="email-load-error-retry"');
	});
});