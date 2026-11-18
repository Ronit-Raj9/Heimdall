import "@/lib/i18n";

import fs from "node:fs";
import path from "node:path";
import type { RunServices } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { runServiceLogPath, runServicesPath } from "@/lib/store/apis/servicesApi";
import { RunServiceLogs, ServiceLogText, ServiceLogsSection, ServiceTable } from "./serviceLogs";

const query = vi.hoisted(() => ({
	services: {} as Record<string, unknown>,
	log: {} as Record<string, unknown>,
	args: [] as unknown[],
}));

vi.mock("@/lib/store/apis/servicesApi", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@/lib/store/apis/servicesApi")>();
	return {
		...actual,
		useGetRunServicesQuery: (arg: unknown) => {
			query.args.push(arg);
			return query.services;
		},
		useGetRunServiceLogQuery: (arg: unknown) => {
			query.args.push(arg);
			return query.log;
		},
	};
});

const REPORT: RunServices = {
	run_id: "r_1",
	sealed: true,
	services: [
		{
			id: "db",
			image: "postgres:18",
			image_id: "sha256:aa",
			ready: true,
			log_bytes: 2048,
		},
		{ id: "cache", image: "redis:7-alpine", ready: true, log_bytes: 120 },
		{
			id: "desk",
			role: "mcp",
			image: "desk:1<b>x</b>",
			ready: false,
			error: "not ready after 60s: <script>alert(1)</script>",
			proxied: true,
		},
	],
};

beforeEach(() => {
	query.services = {};
	query.log = {};
	query.args = [];
});

describe("ServiceTable", () => {
	it("lists every service, its start and its log, as text", () => {
		const html = renderToStaticMarkup(<ServiceTable report={REPORT} arm="A" />);
		expect(html).toContain('data-testid="service-row-db"');
		expect(html).toContain("postgres:18");
		expect(html).toContain('data-testid="service-log-toggle-db"');
		expect(html).toContain("Show log (2");
		expect(html).toContain("not ready");
		expect(html).toContain("no log captured");
		expect(html).not.toContain("<script>");
		expect(html).not.toContain("<b>x</b>");
		expect(html).toContain("&lt;script&gt;");
	});

	it("waits for the seal before offering a log", () => {
		const html = renderToStaticMarkup(<ServiceTable report={{ ...REPORT, sealed: false }} arm="A" />);
		expect(html).not.toContain("service-log-toggle-db");
		expect(html).toContain("served once the run is sealed");
	});

	it("says when no service ran", () => {
		const html = renderToStaticMarkup(<ServiceTable report={{ ...REPORT, services: [] }} arm="B" />);
		expect(html).toContain('data-testid="services-B-none"');
	});
});

describe("RunServiceLogs", () => {
	it("shows loading, then errors, then the table", () => {
		query.services = { isLoading: true };
		expect(renderToStaticMarkup(<RunServiceLogs runId="r_1" arm="A" />)).toContain("services-A-loading");
		query.services = { error: { status: 500 }, refetch: () => undefined };
		expect(renderToStaticMarkup(<RunServiceLogs runId="r_1" arm="A" />)).toContain("services-A-error");
		query.services = { data: { ...REPORT, mode: "live" } };
		expect(renderToStaticMarkup(<RunServiceLogs runId="r_1" arm="A" />)).toContain("services-A");
		expect(query.args).toContain("r_1");
	});
});

describe("ServiceLogText", () => {
	it("renders the log as preformatted text", () => {
		query.log = {
			data: "2026-11-18T08:00:00Z ready\n<img src=x onerror=alert(1)>\n",
		};
		const html = renderToStaticMarkup(<ServiceLogText runId="r_1" service="db" />);
		expect(html).toContain('data-testid="service-log-db"');
		expect(html).not.toContain("<img");
		expect(html).toContain("&lt;img");
		expect(query.args).toContainEqual({ runId: "r_1", service: "db" });
	});
});

describe("ServiceLogsSection", () => {
	it("renders nothing for a pair without services", () => {
		expect(renderToStaticMarkup(<ServiceLogsSection runs={[]} />)).toBe("");
	});
	it("has one tab per arm with services", () => {
		query.services = { isLoading: true };
		const html = renderToStaticMarkup(
			<ServiceLogsSection
				runs={[
					{ arm: "A", runId: "r_1" },
					{ arm: "B", runId: "r_2" },
				]}
			/>,
		);
		expect(html).toContain("runs-services-tab-A");
		expect(html).toContain("runs-services-tab-B");
	});
});

describe("services API paths and contract", () => {
	it("escapes ids as one path segment", () => {
		expect(runServicesPath("r/1")).toBe("/runs/r%2F1/services");
		expect(runServiceLogPath("r_1", "db")).toBe("/runs/r_1/services/db/log");
	});

	it("follows the Go contract key lists", () => {
		const dir = path.resolve(__dirname, "../../../handlers/testdata/contract");
		const keys = (type: string) =>
			fs
				.readFileSync(path.join(dir, `${type}.keys`), "utf8")
				.split("\n")
				.map((l) => l.trim())
				.filter((l) => l && !l.startsWith("#"));
		const top = keys("RunServices").map((k) => k.replace(/\?$/, ""));
		const row = keys("RunService").map((k) => k.replace(/\?$/, ""));
		for (const k of Object.keys(REPORT)) expect(top).toContain(k);
		for (const s of REPORT.services) for (const k of Object.keys(s)) expect(row).toContain(k);
		for (const k of keys("RunService").filter((k) => !k.endsWith("?"))) expect(Object.keys(REPORT.services[0])).toContain(k);
	});
});