import { describe, expect, it } from "vitest";
import {
	adminRedirect,
	canariesRedirect,
	catalogRedirect,
	importTabRedirect,
	addBenchmarkTarget,
	policiesRedirect,
	reviewRedirect,
	schedulesRedirect,
	settingsRedirect,
	uploadRedirect,
	welcomeRedirect,
} from "./navRedirects";

describe("navRedirects", () => {
	it("sends the old plumbing pages to their Settings tab with their query", () => {
		expect(settingsRedirect("keys", { add: "openai" })).toEqual({ to: "/settings", search: { tab: "keys", add: "openai" } });
		expect(settingsRedirect("files", { area: "agents", path: "a.yaml" })).toEqual({ to: "/settings", search: { tab: "files", area: "agents", path: "a.yaml" } });
		expect(settingsRedirect("jobs", { status: "failed", tab: "x" })).toEqual({ to: "/settings", search: { tab: "jobs", status: "failed" } });
		expect(uploadRedirect()).toEqual({ to: "/settings", search: { tab: "files", files: "upload" } });
	});

	it("keeps the admin panel's own tab as section", () => {
		expect(adminRedirect({ tab: "users" })).toEqual({ to: "/settings", search: { tab: "admin", section: "users" } });
		expect(adminRedirect()).toEqual({ to: "/settings", search: { tab: "admin" } });
	});

	it("folds catalog and import into Benchmarks", () => {
		expect(catalogRedirect()).toEqual({ to: "/suites", search: { tab: "catalog" } });
		expect(importTabRedirect()).toEqual({ to: "/suites", search: { tab: "import" } });
	});

	it("keeps /suites/add as the Add benchmark page, not the Import tab", () => {
		expect(addBenchmarkTarget()).toEqual({ to: "/suites/add", search: {} });
	});

	it("folds drift monitors and schedules into Scheduled checks", () => {
		expect(canariesRedirect({ name: "nightly" })).toEqual({ to: "/scheduled-checks", search: { tab: "drift", name: "nightly" } });
		expect(schedulesRedirect()).toEqual({ to: "/scheduled-checks", search: { tab: "schedules" } });
	});

	it("folds review and rules into Releases", () => {
		expect(reviewRedirect({ exp: "e1", run: "r1" })).toEqual({ to: "/releases", search: { tab: "review", exp: "e1", run: "r1" } });
		expect(policiesRedirect({ file: "default.yaml" })).toEqual({ to: "/releases", search: { tab: "rules", file: "default.yaml" } });
	});

	it("sends Getting started to the one start path", () => {
		expect(welcomeRedirect()).toEqual({ to: "/evaluate", search: {} });
	});
});
