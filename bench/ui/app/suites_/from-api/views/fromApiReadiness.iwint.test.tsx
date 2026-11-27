import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ReadinessLine } from "./fromApiView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

// D4 9 gap 1: after the tasks from an API are written, the page says the readiness check runs and links its job.
describe("tasks from an API: readiness line", () => {
	it("says the tasks are being proved and links the job", () => {
		const html = renderToStaticMarkup(<ReadinessLine jobId="job_42" />);
		expect(html).toContain("Proving the tasks on a worker. This takes a few minutes.");
		expect(html).toContain("Open the job");
		expect(html).toContain("job_42");
	});
});