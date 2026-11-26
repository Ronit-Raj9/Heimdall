import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { GwWatchState } from "@/lib/types/gateway";
import { describe, expect, it, vi } from "vitest";
import { ModelWatchView, cronWords, type WatchScheduleRow } from "./modelWatchCard";
import { benchmarkRegisteredAgent } from "./registeredAgents";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ children }: { children: React.ReactNode }) => <a>{children}</a>,
	useNavigate: () => () => undefined,
}));

const row = {
	name: "watch-anthropic",
	cron: "0 6 * * *",
	evaluation: "ev_core",
	kind: "evaluation",
	source: "web",
	spent_today_usd: 0,
	new_models: { providers: ["anthropic"], current: "sonnet", usd_per_model: 5 },
} as WatchScheduleRow;

describe("Watch for new models", () => {
	it("says when it checks in plain words", () => {
		expect(cronWords("0 6 * * *")).toBe("every day at 06:00");
		expect(cronWords("15 * * * *")).toBe("every hour");
		expect(cronWords("0 6 * * 1")).toBe("on the schedule 0 6 * * 1");
	});

	it("shows the sentence, the first-check line and what happened to each model", () => {
		const empty = render(<ModelWatchView row={row} onRetry={() => undefined} />);
		expect(empty).toContain("Heimdall checks Bifrost&#x27;s model catalog every day at 06:00.");
		expect(empty).toContain("runs ev_core on it next to sonnet, once, with a budget of 5.00 USD");
		expect(empty).toContain("The first check only records the models that exist today. Nothing runs until a new one appears.");
		const st: GwWatchState = {
			schedule: row.name,
			baseline: {},
			checked_at: "2026-11-27T06:00:00Z",
			seen: {
				"anthropic/claude-x": {
					provider: "anthropic",
					model: "claude-x",
					first_seen: "2026-11-26T06:00:00Z",
					state: "started",
					evaluation_id: "ev_1",
					claimed_at: "",
				},
				"anthropic/claude-y": {
					provider: "anthropic",
					model: "claude-y",
					first_seen: "2026-11-27T06:00:00Z",
					state: "failed",
					reason: "budget refused",
					claimed_at: "",
				},
			},
		};
		const html = render(<ModelWatchView row={row} state={st} onRetry={() => undefined} />);
		expect(html).toContain("Open evaluation");
		expect(html).toContain("Failed: budget refused");
		expect(html).toContain("Try again");
		expect(html.indexOf("claude-y")).toBeLessThan(html.indexOf("claude-x"));
	});

	it("imports a registered agent, then saves its file", async () => {
		const log: string[] = [];
		const id = await benchmarkRegisteredAgent(
			{
				importAgent: async (a) => (log.push(`import:${a.name}:${a.model}`), { agent_id: "bifrost-helper", yaml: "id: bifrost-helper\n" }),
				createAgent: async (a) => (log.push(`create:${a.yaml.trim()}`), { id: "bifrost-helper" }),
			},
			{ name: "helper", model: "gpt-5", provider: "openai" },
		);
		expect(id).toBe("bifrost-helper");
		expect(log).toEqual(["import:helper:gpt-5", "create:id: bifrost-helper"]);
	});
});