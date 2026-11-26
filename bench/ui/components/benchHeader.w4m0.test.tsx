import "@/lib/i18n";

import { describe, expect, it, vi } from "vitest";
import { render } from "@/lib/testing/render";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

import { HeaderCrumbs } from "./benchHeader";

// Wave 4, p16-1: a page under another sidebar entry ends the crumbs with its own title.
describe("HeaderCrumbs", () => {
	it("ends /connect with Connect an agent", () => {
		const html = render(<HeaderCrumbs pathname="/connect" />);
		expect(html).toContain("Connect an agent");
		expect(html).toContain('aria-current="page"');
	});
	it("ends a job page with Job", () => {
		expect(render(<HeaderCrumbs pathname="/jobs/job_x" />)).toContain(">Job<");
	});
	it("adds nothing on the entry's own page", () => {
		const html = render(<HeaderCrumbs pathname="/agents" />);
		expect(html).not.toContain("Connect an agent");
	});
});