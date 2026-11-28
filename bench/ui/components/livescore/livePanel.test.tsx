import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { LIVE_STATUS_FIXTURE } from "./live.fixtures";
import { CompareSection, LiveBody, WorstSection } from "./livePanel";

// The job button needs the store; its props are what this file checks.
vi.mock("@/components/jobs/jobRunButton", () => ({
	JobRunButton: ({ kind, params, label }: { kind: string; params?: object; label: string }) => (
		<button type="button" data-testid={`job-run-${kind}`} data-params={JSON.stringify(params ?? {})}>
			{label}
		</button>
	),
}));

// The sessions list reads the store; sessionsSection.test.tsx covers it.
vi.mock("./sessionsSection", () => ({ SessionsSection: () => <div data-testid="live-sessions" /> }));

const signalRows = (html: string) => [...html.matchAll(/data-testid="live-signal-([^"]+)"/g)].map((m) => m[1]);

describe("LiveBody", () => {
	it("labels the page live evidence and says what is read", () => {
		const html = renderToStaticMarkup(<LiveBody status={LIVE_STATUS_FIXTURE} />);
		expect(html).toContain('data-testid="live-evidence-note"');
		expect(html).toContain("no ground truth");
		expect(html).toMatch(/data-testid="live-source-url"[^>]*>http:\/\/127\.0\.0\.1:18080/);
		expect(html).toContain("read-only");
		expect(html).toMatch(/data-testid="live-grouping"[^>]*>160 by session id, 6 by pauses/);
		expect(html).toContain("numbers and flags only");
		expect(html).toContain('data-testid="live-judge"');
		expect(html).toContain("never decide");
	});

	it("shows the comparison worse, decided signals first, with pairs and e-values", () => {
		const html = renderToStaticMarkup(<CompareSection compare={LIVE_STATUS_FIXTURE.compare!} />);
		expect(html).toMatch(/data-result="worse" data-testid="live-compare-result"[^>]*>Worse</);
		const rows = signalRows(html);
		expect(rows.slice(0, 2).sort()).toEqual(["abandoned", "tool_error_rate"]);
		expect(rows).toContain("loop");
		expect(html).toMatch(/data-result="worse" data-testid="live-decision-abandoned"/);
		expect(html).toMatch(/data-result="not_yet_known" data-testid="live-decision-loop"/);
		expect(html).toContain("New version v2 compared with current version v1");
		expect(html).toContain("46.5%"); // abandoned share of the candidate
		expect(html).toContain("Ville");
	});

	it("draws every signal over time for each version", () => {
		const html = renderToStaticMarkup(<LiveBody status={LIVE_STATUS_FIXTURE} />);
		for (const s of ["abandoned", "turn_latency_ms", "cost_usd", "judge_score"]) expect(html).toContain(`data-testid="live-series-${s}"`);
		expect(html).toContain("<polyline");
		expect(html).toContain("v1: 80 sessions, 0 flagged (virtual key vk-a)");
	});

	it("links the worst sessions to their failure inbox items", () => {
		const html = renderToStaticMarkup(<WorstSection worst={LIVE_STATUS_FIXTURE.worst} />);
		const first = LIVE_STATUS_FIXTURE.worst[0];
		expect(first.inbox_item).toMatch(/^inc-[0-9a-f]{12}$/);
		expect(html).toContain(`href="/failures?tab=production&amp;item=${first.inbox_item}"`);
		expect(html).toContain("abandoned");
	});

	it("explains how to start when live scoring is not configured", () => {
		const html = renderToStaticMarkup(
			<LiveBody
				status={{
					...LIVE_STATUS_FIXTURE,
					...({ mode: undefined } as object),
					configured: false,
					problem: undefined,
					compare: undefined,
				}}
			/>,
		);
		// M7-64: on a live server the setup hero says it already, so the note is not shown twice.
		const live = renderToStaticMarkup(<LiveBody status={{ ...LIVE_STATUS_FIXTURE, configured: false, problem: undefined, compare: undefined }} />);
		expect(live).not.toContain('data-testid="live-unconfigured"');
		expect(live).toContain('data-testid="live-simulate"');
		expect(html).toContain('data-testid="live-unconfigured"');
		expect(html).toContain("Not set up. Use Set up live scoring.");
		expect(html).toContain("(Terminal: heimdall livescore init)");
		expect(html).not.toContain("bifrost-bench");
		const broken = renderToStaticMarkup(
			<LiveBody
				status={{
					...LIVE_STATUS_FIXTURE,
					problem: "parse livescore/config.yaml: unknown key",
				}}
			/>,
		);
		expect(broken).toContain("Live scoring is off: settings file unreadable. Open Set up live scoring to replace it.");
		// The raw reason only in the title attribute, never in the text.
		expect(broken).toContain('title="parse livescore/config.yaml: unknown key"');
		expect(broken).not.toContain(">parse livescore/config.yaml");
	});

	it("names no command line on the hosted server (N131)", () => {
		const html = renderToStaticMarkup(
			<LiveBody
				hosted
				status={{
					...LIVE_STATUS_FIXTURE,
					...({ mode: undefined } as object),
					configured: false,
					problem: undefined,
					compare: undefined,
				}}
			/>,
		);
		expect(html).toContain("Not set up.");
		expect(html).not.toContain("livescore init");
		expect(html).not.toContain("Terminal");
	});

	it("offers a simulated try of live scoring before it is set up (J15.setup)", () => {
		const html = renderToStaticMarkup(
			<LiveBody status={{ ...LIVE_STATUS_FIXTURE, configured: false, problem: undefined, compare: undefined }} />,
		);
		expect(html).toContain('data-testid="live-simulate"');
		expect(html).toContain('data-testid="job-run-livescore.compare"');
		expect(html).toContain(`data-params="${JSON.stringify({ simulate: 200 }).replace(/"/g, "&quot;")}"`);
		expect(html).toContain("Try simulated traffic (free)");
		expect(html).toContain("Nothing is sent to your agents.");
		const configured = renderToStaticMarkup(<LiveBody status={LIVE_STATUS_FIXTURE} />);
		expect(configured).not.toContain('data-testid="live-simulate"');
	});
});