import "@/lib/i18n";

import type { WorkerList } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import WorkersView from "./workersView";

const query = vi.hoisted(() => ({ result: {} as Record<string, unknown> }));

// The real isWorkersOff and error formatting; only the request is faked.
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetWorkersQuery: () => query.result,
}));

vi.mock("@/lib/store/apis/sharedpoolApi", () => ({
	useGetPoolStatusQuery: () => ({ isLoading: false, data: undefined }),
	useListImageRequestsQuery: () => ({ data: undefined }),
	useRequestImageMutation: () => [vi.fn(), { isLoading: false }],
}));

vi.mock("@/components/workers/connectedWorkers", () => ({ ConnectedWorkers: () => null }));

const fixture = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../../public/fixtures/workers.json"), "utf8")) as WorkerList;
const idle = { isLoading: false, isFetching: false, isError: false, data: undefined, error: undefined, refetch: () => undefined };

describe("WorkersView", () => {
	beforeEach(() => {
		query.result = { ...idle };
	});

	it("shows the disabled state when worker mode is off, not an empty table", () => {
		query.result = { ...idle, data: { mode: "live", enabled: false, workers: [] } };
		const html = renderToStaticMarkup(<WorkersView />);
		expect(html).toContain('data-testid="workers-off"');
		expect(html).not.toContain('data-testid="workers-empty"');
		expect(html).not.toContain('data-testid="workers-table"');
	});

	it("shows the disabled state when a server without the route answers 404, not an error", () => {
		query.result = { ...idle, isError: true, error: { status: 404, data: { error: { message: "route not found: /api/bench/workers" } } } };
		const html = renderToStaticMarkup(<WorkersView />);
		expect(html).toContain('data-testid="workers-off"');
		expect(html).toContain("Extra workers off");
		expect(html).not.toContain('data-testid="workers-error"');
		expect(html).not.toContain("mode-badge-offline");
	});

	it("shows any other failure as an error with the server's message", () => {
		query.result = { ...idle, isError: true, error: { status: 500, data: { error: { message: "lease table unreadable" } } } };
		const html = renderToStaticMarkup(<WorkersView />);
		expect(html).toContain('data-testid="workers-error"');
		expect(html).toContain("lease table unreadable");
		expect(html).not.toContain('data-testid="workers-off"');
	});

	it("says when the API is on but no worker registered", () => {
		query.result = { ...idle, data: { mode: "live", enabled: true, workers: [] } };
		const html = renderToStaticMarkup(<WorkersView />);
		expect(html).toContain('data-testid="workers-empty"');
		expect(html).toContain('data-testid="mode-badge-live"');
	});

	it("lists workers, with the synthetic banner on fixture data", () => {
		query.result = { ...idle, data: fixture };
		const html = renderToStaticMarkup(<WorkersView />);
		expect(html).toContain('data-testid="workers-table"');
		expect(html).toContain('data-testid="workers-synthetic-banner"');
		expect(html).toContain('data-testid="workers-row-w-gpu-1"');
	});

	it("titles a shared worker plainly and keeps its raw id to the hover title", () => {
		const shared = { ...fixture.workers[0], id: "shared-runner-org_abc", host: "runner" };
		query.result = { ...idle, data: { mode: "live", enabled: true, workers: [shared] } };
		const html = renderToStaticMarkup(<WorkersView />);
		expect(html).toContain(">Shared worker</p>");
		expect(html).toContain('title="shared-runner-org_abc"');
		expect(html).not.toContain(">shared-runner-org_abc<");
		expect(html).toContain("Open worker tokens");
		expect(html).toContain('data-testid="workers-row-shared-runner-org_abc-shared-note"');
		expect(html).toContain("Isolated containers.");
		expect(html).toContain(`Up to ${shared.slots} ${shared.slots === 1 ? "run" : "runs"} at once.`);
		expect(html).toContain('href="/settings?tab=runners#runner-token-setup"');
		expect(html).toContain("Use your own machine");
	});

	it("shows no shared-worker note on a worker of your own", () => {
		query.result = { ...idle, data: fixture };
		expect(renderToStaticMarkup(<WorkersView />)).not.toContain("shared-note");
	});

	it("shows a skeleton while loading", () => {
		query.result = { ...idle, isLoading: true };
		expect(renderToStaticMarkup(<WorkersView />)).toContain('data-testid="workers-loading"');
	});
});