import "@/lib/i18n";

import type { Job } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { NO_FILTERS } from "./catalogModel";
import { CatalogBody, importFailureOf } from "./catalogView";

const failed = {
	id: "job_01m82j8xbwyma4vyqkh3qtmtdg",
	org_id: "org",
	kind: "catalog.import",
	status: "failed",
	params: null,
	error:
		"harbor: no task was imported. zz-task-1: no environment/Dockerfile: a Harbor task needs the Dockerfile its container is built from",
	result: {
		suite: "zz-test-harbor",
		tasks: [],
		skipped: [{ id: "zz-task-1", reason: "no environment/Dockerfile: a Harbor task needs the Dockerfile its container is built from" }],
	},
	created_at: "2026-11-22T21:23:00Z",
	cancel_requested: false,
	attempt: 1,
} as unknown as Job;

// A failed catalog import lists the tasks it skipped and why (fix wave 3, N263).
describe("catalog import failure", () => {
	it("reads the skipped tasks of a failed import job", () => {
		expect(importFailureOf(failed)).toEqual({
			error: failed.error,
			skipped: [{ id: "zz-task-1", reason: "no environment/Dockerfile: a Harbor task needs the Dockerfile its container is built from" }],
		});
		expect(importFailureOf({ ...failed, status: "succeeded" } as Job)).toBeNull();
		expect(importFailureOf({ ...failed, kind: "suite.lock" } as Job)).toBeNull();
	});

	it("shows the error and a Skipped tasks list", () => {
		const html = renderToStaticMarkup(
			<CatalogBody
				entries={[]}
				sourceAvailable
				filters={NO_FILTERS}
				onFilters={() => {}}
				onAdd={() => {}}
				importFailure={importFailureOf(failed)}
			/>,
		);
		expect(html).toContain('data-testid="catalog-import-failed"');
		expect(html).toContain("Skipped tasks");
		expect(html).toContain("zz-task-1");
		expect(html).toContain("a Harbor task needs the Dockerfile its container is built from");
	});
});