import "@/lib/i18n";

import type { AgentVersion } from "@/lib/store/apis/evaluationsApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import VersionsTab, { authorOf, saveOutcome } from "./versionsTab";

const state = vi.hoisted(() => ({ versions: [] as AgentVersion[] }));

vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/evaluationsApi")>()),
	useGetAgentVersionsQuery: () => ({ data: { agent: "codex", versions: state.versions }, isLoading: false, isError: false }),
	useGetAgentVersionQuery: () => ({ data: undefined, isLoading: false, isError: false }),
	useCreateAgentVersionMutation: () => [vi.fn(), { isLoading: false, isError: false }],
}));

const v = (over: Partial<AgentVersion>): AgentVersion => ({
	version: 1,
	hash: "h1",
	created_at: "2026-11-20T10:00:00Z",
	created_by: "",
	note: "",
	runs: 0,
	...over,
});

describe("versions tab: save as new version (N32)", () => {
	it("a new version clears the note, opens it and says which version it is", () => {
		expect(saveOutcome({ ...v({ version: 3 }), created: true })).toEqual({
			clearNote: true,
			select: 3,
			message: { key: "versions.created", version: 3 },
		});
	});
	it("nothing changed keeps the note, opens the existing version and says no version was made", () => {
		expect(saveOutcome({ ...v({ version: 2 }), created: false })).toEqual({
			clearNote: false,
			select: 2,
			message: { key: "versions.unchanged", version: 2 },
		});
	});
	it("an answer without the created flag is a new version (older servers)", () => {
		expect(saveOutcome(v({ version: 4 }) as never).clearNote).toBe(true);
	});
});

describe("versions tab: author (N33)", () => {
	it("shows the display name, with the email as a tooltip only when the server sent it", () => {
		expect(authorOf(v({ created_by: "", created_by_name: "ronit" }))).toEqual({ text: "ronit", title: undefined });
		expect(authorOf(v({ created_by: "ronit@example.com", created_by_name: "ronit" }))).toEqual({
			text: "ronit",
			title: "ronit@example.com",
		});
		expect(authorOf(v({ created_by: "ana" }))).toEqual({ text: "ana", title: "ana" });
		expect(authorOf(v({}))).toEqual({ text: "-", title: undefined });
	});
	it("renders the name in the Author cell, never an email it was not sent", () => {
		state.versions = [v({ version: 1, created_by: "", created_by_name: "ronit" })];
		const html = renderToStaticMarkup(<VersionsTab agentId="codex" />);
		expect(html).toContain(">ronit<");
		expect(html).not.toContain("@");
	});
});