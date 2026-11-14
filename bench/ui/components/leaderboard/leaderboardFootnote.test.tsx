import "@/lib/i18n";

import type { LeaderboardResponse } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { CopyMarkdownButton } from "./copyMarkdownButton";
import { LeaderboardSyntheticBanner } from "./leaderboardBanner";
import { LeaderboardFootnote } from "./leaderboardFootnote";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, search, children, ...rest }: { to: string; search: { exp: string }; children: React.ReactNode }) => (
		<a href={`${to}?exp=${search.exp}`} {...rest}>
			{children}
		</a>
	),
}));

const board = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/leaderboard.json"), "utf8")) as LeaderboardResponse;

describe("LeaderboardFootnote", () => {
	it("lists the common tasks, the runs left out by reason and the scope", () => {
		const html = renderToStaticMarkup(<LeaderboardFootnote board={board} hasResilience />);
		expect(html).toContain("Common tasks (3)");
		for (const task of board.common_tasks) expect(html).toContain(`>${task}<`);
		expect(html).toContain('data-testid="leaderboard-excluded-infra_error"');
		expect(html).toContain(">24 runs<");
		expect(html).toContain(">1 run<");
		expect(html).toContain('href="/verdict?exp=e_synthetic-flaky"');
		expect(html).toContain("Scope all: each entry is measured over its own tasks");
		expect(html).toContain("Rank alone is never a claim that one agent beats another.");
		expect(html).toContain("Resilience columns appear on the clean board only");
		expect(html).toContain("Sorted by verified_success; tiers at alpha 0.05.");
	});

	it("explains a common-scope board and says when nothing was left out", () => {
		const html = renderToStaticMarkup(
			<LeaderboardFootnote board={{ ...board, scope: "common", excluded: [], common_tasks: ["t01"] }} hasResilience={false} />,
		);
		expect(html).toContain("over the same 1 common task only.");
		expect(html).toContain("No runs were left out.");
		expect(html).not.toContain("Resilience columns");
	});

	it("says when no task is common to every entry", () => {
		const html = renderToStaticMarkup(<LeaderboardFootnote board={{ ...board, common_tasks: [] }} hasResilience={false} />);
		expect(html).toContain("No task was run by every listed entry.");
	});
});

describe("LeaderboardSyntheticBanner", () => {
	it("says the numbers are not findings", () => {
		const html = renderToStaticMarkup(<LeaderboardSyntheticBanner />);
		expect(html).toContain('data-testid="leaderboard-synthetic-banner"');
		expect(html).toContain("they are not findings.");
	});
});

describe("CopyMarkdownButton", () => {
	it("renders the copy action without building the markdown", () => {
		const build = vi.fn(() => "");
		const html = renderToStaticMarkup(<CopyMarkdownButton build={build} />);
		expect(html).toContain('data-testid="leaderboard-copy-markdown"');
		expect(html).toContain("Copy as markdown");
		expect(build).not.toHaveBeenCalled();
	});
});