import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { ownPageTitle, pageTitle } from "./navigation";

// Wave 4, p16-1 and p02-8: pages under another sidebar entry have a title of their own.
describe("ownPageTitle", () => {
	it("names the job, jobs and connect pages", () => {
		expect(ownPageTitle("/jobs/job_x")).toBe("Job");
		expect(ownPageTitle("/jobs")).toBe("Jobs");
		expect(ownPageTitle("/connect")).toBe("Connect an agent");
		expect(ownPageTitle("/agents/new")).toBe("New agent");
		expect(ownPageTitle("/agents/from-traces/")).toBe("Agent from traces");
	});
	it("is null for a page that is its nav entry", () => {
		expect(ownPageTitle("/agents")).toBeNull();
	});
});

describe("pageTitle with the wave 4 titles", () => {
	it("reads New agent on /agents/new", () => {
		expect(pageTitle("/agents/new")).toBe("New agent · Heimdall");
		expect(pageTitle("/connect")).toBe("Connect an agent · Heimdall");
	});
});