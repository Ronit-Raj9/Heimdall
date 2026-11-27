import { describe, expect, it } from "vitest";
import { auditParams } from "./auditTab";
import { previewOf } from "./retentionPreviewApi";

const policy = { run_days: 90, min_days: 1, max_days: 3650 };

describe("retention dry run preview", () => {
	it("reads the count for the asked days", () => {
		expect(previewOf({ ...policy, preview: { days: 30, experiments: 12, runs: 340 } } as never, 30)).toEqual({ days: 30, experiments: 12, runs: 340 });
	});
	it("is null when the server sends no preview or one for other days", () => {
		expect(previewOf(policy as never, 30)).toBeNull();
		expect(previewOf(undefined, 30)).toBeNull();
		expect(previewOf({ ...policy, preview: { days: 60, experiments: 1, runs: 2 } } as never, 30)).toBeNull();
	});
});

describe("audit list request", () => {
	it("sends the trimmed text as q", () => {
		expect(auditParams("org_a", "c1", "  member.add ")).toMatchObject({ orgId: "org_a", cursor: "c1", q: "member.add" });
	});
	it("leaves q out when there is no text", () => {
		expect(auditParams("org_a", undefined, "  ")).not.toHaveProperty("q");
	});
});
