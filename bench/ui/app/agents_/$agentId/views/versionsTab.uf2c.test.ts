import type { AgentVersion } from "@/lib/store/apis/evaluationsApi";
import { describe, expect, it } from "vitest";
import { sameAsLatest, versionColumns } from "./versionsTab";

const v = (version: number, extra: Partial<AgentVersion> = {}): AgentVersion => ({
	version,
	hash: `h${version}`,
	created_at: "2026-11-20T10:00:00Z",
	created_by: "",
	note: "",
	...extra,
});

describe("versions tab (M2-62, M2-63)", () => {
	it("knows when the saved config is already the newest version", () => {
		expect(sameAsLatest({ live: 2, versions: [v(1), v(2)] })).toBe(2);
		expect(sameAsLatest({ live: 1, versions: [v(1), v(2)] })).toBeNull();
		expect(sameAsLatest({ versions: [v(1)] })).toBeNull();
		expect(sameAsLatest(undefined)).toBeNull();
	});

	it("hides Author and Note when every row is empty", () => {
		expect(versionColumns([v(1), v(2)])).toEqual({ author: false, note: false });
		expect(versionColumns([v(1, { created_by: "raj" }), v(2, { note: "x" })])).toEqual({ author: true, note: true });
	});
});
