import "@/lib/i18n";

import type { RunDetail } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { RunOutput, runOutputKind } from "./runOutput";

// The diff request is a spy: an answer, state or artifact run must never ask for it.
const diffQuery = vi.hoisted(() =>
	vi.fn(() => ({ isLoading: false, error: { status: 404, data: {} }, data: undefined, refetch: vi.fn() })),
);
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetRunDiffQuery: diffQuery,
}));
vi.mock("@/lib/session/sessionContext", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/session/sessionContext")>()),
	useSession: () => ({ status: "ready", authMode: "local", signedIn: false }),
}));

const detail = (extra: Partial<RunDetail>): RunDetail =>
	({
		run: { run_id: "r_1" },
		slot: { arm: "A" },
		sealed: true,
		seal_hash: "",
		grade: null,
		grade_source: "original",
		metrics: {},
		gates: [],
		receipts: [],
		...extra,
	}) as unknown as RunDetail;

describe("runOutputKind", () => {
	it("trusts a known hint, else reads what the run recorded", () => {
		expect(runOutputKind(detail({}), "state")).toBe("state");
		expect(runOutputKind(detail({}), "bogus")).toBe("diff");
		expect(runOutputKind(detail({ answer: { source: "file", text: "42" } as never }))).toBe("answer");
		expect(runOutputKind(detail({ artifacts: [{ id: "report" } as never] }))).toBe("artifact");
		expect(runOutputKind(detail({ state: [{ path: "db.json" } as never] }))).toBe("state");
		expect(runOutputKind(detail({}))).toBe("diff");
		expect(runOutputKind(undefined)).toBe("diff");
	});
});

describe("RunOutput", () => {
	it("shows an answer run's answer and never asks for a diff", () => {
		diffQuery.mockClear();
		const html = renderToStaticMarkup(
			<RunOutput detail={detail({ answer: { source: "final_message", text: "Paris" } as never })} arm="A" />,
		);
		expect(html).toContain('data-output="answer"');
		expect(html).toContain("Paris");
		expect(html).not.toContain("Could not load the diff");
		expect(diffQuery).not.toHaveBeenCalled();
	});
	it("says plainly when a sealed diff run changed no files", () => {
		const html = renderToStaticMarkup(<RunOutput detail={detail({})} arm="B" />);
		expect(diffQuery).toHaveBeenCalledWith("r_1");
		expect(html).toContain('data-testid="diff-B-none"');
		expect(html).toContain("The agent changed no files, so there is no diff.");
		expect(html).not.toContain("Could not load the diff");
	});
});