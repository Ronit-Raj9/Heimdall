import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/components/jobs/jobLogPanel", () => ({ JobLogPanel: () => null }));

import { JobLogDrawer } from "./jobsView";

// Wave 4, p14-7: the log drawer's panel can take focus.
describe("JobLogDrawer focus", () => {
	it("gives the panel tabindex -1", () => {
		const html = render(<JobLogDrawer jobId="job_x" onClose={() => undefined} />);
		expect(html).toMatch(/role="dialog"[^>]*tabindex="-1"|tabindex="-1"[^>]*role="dialog"/);
	});
});