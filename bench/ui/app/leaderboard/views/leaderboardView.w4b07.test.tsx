import "@/lib/i18n";

import fs from "node:fs";
import path from "node:path";
import { okQuery, render } from "@/lib/testing/render";
import type { LeaderboardResponse } from "@/lib/types/bench";
import { describe, expect, it, vi } from "vitest";
import { LeaderboardTab } from "./leaderboardView";

// Wave 4, N76: a link naming a benchmark that does not exist says so instead of silently
// showing another benchmark's board.

const url = vi.hoisted(() => ({ suite: null as string | null }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	useQueryState: (key: string) => [key === "suite" ? url.suite : null, () => undefined],
}));
const board = JSON.parse(
	fs.readFileSync(path.resolve(__dirname, "../../../public/fixtures/leaderboard.json"), "utf8"),
) as LeaderboardResponse;
vi.mock("@/lib/store", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store")>()),
	useGetLeaderboardQuery: () => okQuery(board),
	useGetSuitesQuery: () => okQuery({ mode: "live", suites: [] }),
}));

// M4-34: the leaderboard reads the light suites list.
vi.mock("@/lib/store/apis/suitesLightApi", () => ({
	useGetSuitesLightQuery: () => okQuery({ mode: "live", suites: [] }),
}));

vi.mock("@/components/migration/migrationReport", () => ({ LeaderboardParetoPanel: () => null }));

describe("LeaderboardTab unknown benchmark", () => {
	it("an unknown suite says no benchmark named bogus-zz@9", () => {
		url.suite = "bogus-zz@9";
		const html = render(<LeaderboardTab />);
		expect(html).toContain('data-testid="unknown-link-benchmark"');
		expect(html).toContain("No benchmark named bogus-zz@9 was found. Showing all benchmarks instead.");
		expect(html).toContain('data-testid="unknown-link-benchmark-show-all"');
	});

	it("a known suite shows no note", () => {
		url.suite = "coding-core@1.0.0";
		expect(render(<LeaderboardTab />)).not.toContain("unknown-link-benchmark");
	});
});