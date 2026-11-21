import "@/lib/i18n";

import { REGRADE_JOB, hostedSession } from "@/lib/testing/platformFixtures";
import { render } from "@/lib/testing/render";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { JOB_KIND_LABELS, jobKindLabel } from "./jobKindLabels";
import { JobsDrawer } from "@/components/platform/jobsDrawer";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

/** Every kind the Go server registers: string literals passed as Kind or to cliJob, plus the kind constants. */
function goKinds(): string[] {
	const dir = path.resolve(__dirname, "../../../cmd/bifrost-bench");
	const kinds = new Set<string>();
	for (const f of fs.readdirSync(dir)) {
		if (!f.endsWith(".go") || f.endsWith("_test.go")) continue;
		const src = fs.readFileSync(path.join(dir, f), "utf8");
		for (const m of src.matchAll(/(?:Kind:\s*|cliJob\()"([a-z0-9]+\.[a-z0-9-]+)"/g)) kinds.add(m[1]);
		for (const m of src.matchAll(/\b(?:kind|job)[A-Z]\w*\s*=\s*"([a-z0-9]+\.[a-z0-9-]+)"/g)) kinds.add(m[1]);
	}
	return [...kinds].sort();
}

describe("job kind labels", () => {
	it("label every kind the server registers", () => {
		const kinds = goKinds();
		expect(kinds.length).toBeGreaterThan(30);
		expect(kinds.filter((k) => !(k in JOB_KIND_LABELS))).toEqual([]);
	});

	it("fall back to the raw kind", () => {
		expect(jobKindLabel("release.compliance")).toBe("Compliance pack");
		expect(jobKindLabel("brand.new")).toBe("brand.new");
		expect(jobKindLabel("constructor")).toBe("constructor");
	});

	it("show in the jobs drawer for a kind no locale names", () => {
		const job = { ...REGRADE_JOB, id: "job_cp", kind: "release.compliance" };
		const html = render(<JobsDrawer jobs={[job]} isLoading={false} onRetry={() => undefined} onClose={() => undefined} />, hostedSession());
		expect(html).toContain("Compliance pack");
	});
});
