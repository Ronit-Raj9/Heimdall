import "@/lib/i18n";

import type { AgentConfigDoc } from "@/lib/types/agentBuilder";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { checkOf, ConfigTab, saveKey } from "./configTab";

const doc: AgentConfigDoc = {
	id: "zz-test-loop",
	path: "agents/zz-test-loop.yaml",
	yaml: "id: zz-test-loop\nadapter: command\n",
	sha256: "abc",
	fields: {
		id: "zz-test-loop",
		adapter: "command",
		model: "gpt-x",
		provider: "openai",
		image: "",
		wall_s: 600,
		cpus: 1,
		mem_gb: 2,
		turns: 0,
		env: {},
	},
};

const state: { saved: Partial<AgentConfigDoc> | undefined; keys: unknown[] } = { saved: undefined, keys: [] };

vi.mock("@/lib/store/apis/agentBuilderApi", () => ({
	useGetAgentConfigQuery: () => ({ isLoading: false, isError: false, data: doc, refetch: () => undefined }),
	usePutAgentConfigMutation: (opts?: { fixedCacheKey?: string }) => {
		state.keys.push(opts?.fixedCacheKey);
		return [vi.fn(), { isLoading: false, data: state.saved, reset: () => undefined }];
	},
	useValidateAgentConfigMutation: () => [vi.fn(), { isLoading: false }],
}));
vi.mock("@/lib/store/apis/pullAgentsApi", () => ({ useGetPullAgentsQuery: () => ({ isLoading: false }) }));
vi.mock("@/lib/session/sessionContext", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/session/sessionContext")>()),
	useSession: () => ({ authMode: "local" }),
}));
vi.mock("@tanstack/react-router", () => ({
	Link: ({ children }: { children: React.ReactNode }) => <a>{children}</a>,
}));

const render = () => renderToStaticMarkup(<ConfigTab agentId="zz-test-loop" />);

describe("ConfigTab save and check messages", () => {
	it("shows the saved version above the editor, from the save shared with the parent", () => {
		state.keys = [];
		state.saved = { ...doc, version: 2, version_created: true };
		const html = render();
		expect(html).toContain("Saved as version 2.");
		// The parent and the editor read the same save, so a remount of the editor keeps the message.
		expect(state.keys.length).toBeGreaterThanOrEqual(2);
		expect(new Set(state.keys)).toEqual(new Set([saveKey("zz-test-loop")]));
		expect(html.indexOf('data-testid="config-saved"')).toBeLessThan(html.indexOf('data-testid="config-tab"'));
		expect(html).not.toMatch(/[\u2013\u2014]/);
	});

	it("says when a save kept the version", () => {
		state.saved = { ...doc, version: 3, version_created: false };
		expect(render()).toContain("Saved. Still version 3: model and provider changes do not make a new version.");
	});

	it("shows no saved message before a save", () => {
		state.saved = undefined;
		expect(render()).not.toContain('data-testid="config-saved"');
	});

	it("turns a failed check into the server's error text", () => {
		const err = { status: 400, data: { error: { message: "request body is not valid JSON" } } };
		expect(checkOf({ error: err })).toEqual({ ok: false, error: "request body is not valid JSON" });
		expect(checkOf({ data: { ok: true } })).toEqual({ ok: true, error: undefined });
		expect(checkOf({ data: { ok: false, error: "bad yaml" } })).toEqual({ ok: false, error: "bad yaml" });
	});
});