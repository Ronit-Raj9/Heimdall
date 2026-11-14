import "@/lib/i18n";

import type { Value } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { AgentCell, ExperimentLinks, ReliabilityCell, ResilienceCell, SuccessCell, TierBadge } from "./leaderboardCells";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, search, children, ...rest }: { to: string; search: { exp: string }; children: React.ReactNode }) => (
		<a href={`${to}?exp=${search.exp}`} {...rest}>
			{children}
		</a>
	),
}));

const HASH = "sha256:52b5454d0000000000000000000000000000000000000000000000000000933a";
const unknown: Value = { state: "unknown", v: null, reason: "no graded run" };

describe("TierBadge", () => {
	it("shows tier 1 as tied with the leader and the leader's own hint", () => {
		const html = renderToStaticMarkup(<TierBadge entry={{ rank: 1, tier: 1, tier_reason: "leader", p_vs_leader: null }} />);
		expect(html).toContain('data-tier="1"');
		expect(html).toContain('tabindex="0"');
		expect(html).toContain("Leader: every other entry is compared with this one.");
	});

	it("never shows an unexpected tier as tied", () => {
		const html = renderToStaticMarkup(
			<TierBadge entry={{ rank: 3, tier: 0, tier_reason: "p 0.04 on 9 common tasks", p_vs_leader: 0.04 }} />,
		);
		expect(html).toContain('data-tier="2"');
		expect(html).toContain(">behind<");
	});
});

describe("AgentCell", () => {
	const base = { rank: 2, agent: "codex", model: "gpt-5.4-mini", label: "A: baseline", config_hash: HASH };

	it("shows agent, model, label and the short hash with the full hash in the title", () => {
		const html = renderToStaticMarkup(<AgentCell entry={{ ...base, synthetic: false }} />);
		expect(html).toContain(">codex<");
		expect(html).toContain(">A: baseline<");
		expect(html).toContain(">52b5454d…933a<");
		expect(html).toContain(`title="Config hash ${HASH}"`);
		expect(html).not.toContain("leaderboard-synthetic-2");
	});

	it("badges a synthetic configuration", () => {
		const html = renderToStaticMarkup(<AgentCell entry={{ ...base, synthetic: true }} />);
		expect(html).toContain('data-testid="leaderboard-synthetic-2"');
		expect(html).toContain(">synthetic<");
	});
});

describe("SuccessCell", () => {
	it("draws no whiskers without an interval and says why", () => {
		const html = renderToStaticMarkup(
			<SuccessCell entry={{ rank: 1, success: { state: "observed", v: 0.5 }, passes: { num: 1, den: 2, unknown: 0 } }} />,
		);
		expect(html).toContain('aria-label="Verified success 50%, no interval with fewer than 2 tasks"');
		expect(html).not.toContain("leaderboard-interval-1");
	});

	it("shows an unknown success hatched with no bar, and unknown passes apart", () => {
		const html = renderToStaticMarkup(<SuccessCell entry={{ rank: 1, success: unknown, passes: { num: 0, den: 0, unknown: 3 } }} />);
		expect(html).toContain('data-state="unknown"');
		expect(html).not.toContain('role="img"');
		expect(html).toContain("0/0 passes");
		expect(html).toContain("+3 unknown");
		expect(html).not.toContain(">0%<");
	});
});

describe("ReliabilityCell", () => {
	it("names k", () => {
		const html = renderToStaticMarkup(<ReliabilityCell entry={{ rank: 1, reliability: { state: "observed", v: 0.25 }, trials: 6 }} />);
		expect(html).toContain(">25%<");
		expect(html).toContain(">pass^6<");
	});
});

describe("ResilienceCell", () => {
	it("tells not run apart from unknown", () => {
		const notRun = renderToStaticMarkup(<ResilienceCell value={undefined} condition="test-crash" testId="r" />);
		expect(notRun).toContain(">not run<");
		expect(notRun).toContain('title="This configuration did not run test-crash."');
		const unk = renderToStaticMarkup(<ResilienceCell value={unknown} condition="test-crash" testId="r" />);
		expect(unk).toContain('data-state="unknown"');
		expect(unk).toContain("bench-hatched");
	});
});

describe("ExperimentLinks", () => {
	it("links the newest experiments and counts the rest", () => {
		const html = renderToStaticMarkup(<ExperimentLinks entry={{ rank: 1, experiments: ["e_c", "e_b", "e_a"] }} />);
		expect(html).toContain('href="/verdict?exp=e_c"');
		expect(html).toContain('href="/verdict?exp=e_b"');
		expect(html).not.toContain('href="/verdict?exp=e_a"');
		expect(html).toContain(">+1 more<");
		expect(html).toContain('aria-label="Open the verdict of e_c"');
	});
});