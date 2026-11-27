import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { adapterIcon, adapterKey } from "../../../agents/views/agentDisplay";
import { pullNameOf, PullServeStatus, pythonSnippet, statusFor, typescriptSnippet } from "./pullServePanel";

vi.mock("@/lib/store/apis/pullAgentsApi", () => ({ useGetPullAgentsQuery: () => ({ isLoading: false }) }));

describe("pull agent setup", () => {
	it("reads pull.name, else the agent id", () => {
		expect(pullNameOf("id: a\nadapter: pull\npull:\n  repo: tarball\n  name: support-bot\n", "a")).toBe("support-bot");
		expect(pullNameOf("id: a\nadapter: pull\npull:\n  repo: tarball\nbudgets:\n  name: x\n", "a")).toBe("a");
		expect(pullNameOf("id: a\nadapter: pull\n", "a")).toBe("a");
	});

	it("labels the pull adapter", () => {
		expect(adapterKey("pull")).toBe("pull");
		expect(adapterIcon("pull")).not.toBe(adapterIcon("other"));
	});

	it("builds both snippets with the name and origin", () => {
		expect(pythonSnippet("bot", "https://h.example")).toContain('serve("bot", fn, url="https://h.example", token=os.environ["HEIMDALL_TOKEN"])');
		expect(typescriptSnippet("bot", "https://h.example")).toContain('import { serve } from "@heimdall/bench-client";');
		expect(typescriptSnippet("bot", "https://h.example")).toContain('await serve("bot", fn, { url: "https://h.example", token: process.env.HEIMDALL_TOKEN });');
	});

	it("shows a connected agent's processes and queue", () => {
		const list = [{ agent: "bot", connected: true, last_poll: "2026-11-22T10:00:00Z", processes: 2, queued: 3, in_flight: 1 }];
		const html = renderToStaticMarkup(<PullServeStatus status={statusFor(list, "bot")} name="bot" origin="https://h.example" />);
		expect(html).toContain("Serve this agent from your code");
		expect(html).toContain('data-state="connected"');
		expect(html).toContain("3 waiting, 1 answering");
		expect(html).toContain("Runs do not start while no process serves it.");
		expect(html).not.toMatch(/[–—]/);
	});

	it("shows an agent no process serves as not connected", () => {
		const html = renderToStaticMarkup(<PullServeStatus status={statusFor([], "bot")} name="bot" origin="https://h.example" />);
		expect(html).toContain('data-state="offline"');
		expect(html).toContain("Not connected");
		expect(html).toContain("Never");
	});
});
