import "@/lib/i18n";

import { liveSessionsUrl } from "@/lib/store/apis/livescoreSessionsApi";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { LIVE_FLAGS, SessionsSection } from "./sessionsSection";

const state = vi.hoisted(() => ({ value: {} as Record<string, unknown> }));
vi.mock("@/lib/store/apis/livescoreSessionsApi", async (orig) => ({
	...(await orig<typeof import("@/lib/store/apis/livescoreSessionsApi")>()),
	useGetLiveSessionsQuery: () => state.value,
}));
vi.mock("@/components/runs/screenStates", async (orig) => ({
	...(await orig<typeof import("@/components/runs/screenStates")>()),
	LoadError: ({ testId }: { testId: string }) => <div data-testid={testId} />,
}));

const versions = [{ name: "v1", selector: "", sessions: 1, flagged: 1, last_24h: [], series: [] }];

describe("live sessions request", () => {
	it("sends the query the handler reads (handlers/livescore_test.go uses the same URL)", () => {
		expect(liveSessionsUrl({ version: "v2", flag: "abandoned", limit: 5 })).toBe("/livescore/sessions?version=v2&flag=abandoned&limit=5");
		expect(liveSessionsUrl({ version: "", flag: "" })).toBe("/livescore/sessions");
		const goTest = readFileSync(resolve(__dirname, "../../../handlers/livescore_test.go"), "utf8");
		expect(goTest).toContain('"/livescore/sessions?version=v2&flag=abandoned&limit=5"');
	});

	it("offers only the flags the server knows", () => {
		const src = readFileSync(resolve(__dirname, "../../../livescore/livescore.go"), "utf8");
		for (const f of LIVE_FLAGS) expect(src).toContain(`"${f}"`);
	});
});

describe("SessionsSection", () => {
	it("loading", () => {
		state.value = { isLoading: true };
		expect(renderToStaticMarkup(<SessionsSection versions={versions} />)).toContain("live-sessions-loading");
	});
	it("error", () => {
		state.value = { error: { status: 400 } };
		expect(renderToStaticMarkup(<SessionsSection versions={versions} />)).toContain("live-sessions-error");
	});
	it("empty", () => {
		state.value = { data: { evidence: "live", total: 0, sessions: [] } };
		expect(renderToStaticMarkup(<SessionsSection versions={versions} />)).toContain("No scored session matches.");
	});
	it("rows and count", () => {
		state.value = {
			data: {
				evidence: "live",
				total: 7,
				sessions: [
					{
						seq: 1,
						id: "ls_ab",
						version: "v1",
						evidence: "live",
						grouping: "gap",
						started: "2026-11-01T00:00:00Z",
						ended: "2026-11-01T00:01:00Z",
						scored_at: "2026-11-01T00:02:00Z",
						requests: 3,
						signals: { abandoned: true },
						flags: ["loop"],
					},
				],
			},
		};
		const html = renderToStaticMarkup(<SessionsSection versions={versions} />);
		expect(html).toContain('data-testid="live-session-ls_ab"');
		expect(html).toContain("loop");
		expect(html).toContain("1 of 7");
	});
});