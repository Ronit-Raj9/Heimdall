import "@/lib/i18n";

import type { AgentTemplate } from "@/lib/types/agentBuilder";
import { Globe, Network, Package, SquareTerminal } from "lucide-react";
import { describe, expect, it } from "vitest";
import { looksLikeKey, missingFieldLabels, paramDetails } from "./connectForm";
import { agentFormDirty, agentIdFromName, storableParams, templateGroups, templateIcon } from "./newAgentView";

const tpl = (id: string, category: AgentTemplate["category"], adapter = "command", params: AgentTemplate["params"] = []): AgentTemplate => ({
	id,
	name: id,
	category,
	description: "",
	adapter,
	providers: ["openai"],
	default_model: "m",
	builds: false,
	params,
});

// Fix wave lane 2b: the new agent gallery, name and missing-field helpers.
describe("new agent gallery (fix wave 2b)", () => {
	it("puts the most used templates first and shows each template once", () => {
		const all = [tpl("codex", "cli"), tpl("http-url", "bring-your-own", "remote_http"), tpl("command", "starter"), tpl("wrap-app", "bring-your-own")];
		const { mostUsed, groups } = templateGroups(all);
		expect(mostUsed.map((x) => x.id)).toEqual(["http-url", "command"]);
		const inGroups = groups.flatMap((g) => g.items.map((x) => x.id));
		expect(inGroups).toEqual(["wrap-app", "codex"]);
		expect(groups[0].cat).toBe("bring-your-own");
	});

	it("gives each kind of template its own icon", () => {
		expect(templateIcon(tpl("http-url", "bring-your-own", "remote_http"))).toBe(Globe);
		expect(templateIcon(tpl("a2a-card", "bring-your-own", "a2a"))).toBe(Network);
		expect(templateIcon(tpl("docker-image", "bring-your-own"))).toBe(Package);
		expect(templateIcon(tpl("codex", "cli"))).toBe(SquareTerminal);
	});

	it("keeps the typed name and saves it as a valid id", () => {
		expect(agentIdFromName("My Support Bot")).toBe("my-support-bot");
		expect(agentIdFromName("  -Bot_1 ")).toBe("bot_1");
		expect(agentIdFromName("a".repeat(80))).toHaveLength(63);
	});

	it("names the missing fields and the either-or rule of wrap-app", () => {
		const wrap = tpl("wrap-app", "bring-your-own", "command", [
			{ name: "repo_url", label: "Git repository", required: false },
			{ name: "image", label: "Docker image", required: false },
			{ name: "command", label: "Start command", required: true },
		]);
		expect(missingFieldLabels(wrap, {})).toEqual({ labels: ["Start command"], wrapEither: true });
		expect(missingFieldLabels(wrap, { command: "x", image: "a:1" })).toEqual({ labels: [], wrapEither: false });
	});

	it("warns when a secret field holds what looks like a key", () => {
		expect(looksLikeKey("sk-abc")).toBe(true);
		expect(looksLikeKey("x".repeat(31))).toBe(true);
		expect(looksLikeKey("MY_AGENT_TOKEN")).toBe(false);
	});
});

describe("new agent draft (fix wave 2b, X-81)", () => {
	const t = tpl("http-url", "bring-your-own", "remote_http", [
		{ name: "endpoint_url", label: "Endpoint", default: "" },
		{ name: "auth", label: "Auth", kind: "secret" },
		{ name: "api_key_header", label: "Header", default: "Authorization" },
	] as AgentTemplate["params"]);
	it("never keeps secret fields in the draft", () => {
		expect(storableParams(t, { endpoint_url: "https://x", auth: "sk-1", api_key_header: "X" })).toEqual({ endpoint_url: "https://x" });
	});
	it("is dirty only after a change from the defaults", () => {
		expect(agentFormDirty(null, "x", "", {})).toBe(false);
		expect(agentFormDirty(t, null, "", { api_key_header: "Authorization" })).toBe(false);
		expect(agentFormDirty(t, null, "", { api_key_header: "Authorization", endpoint_url: "https://x" })).toBe(true);
		expect(agentFormDirty(t, "mine", "", {})).toBe(true);
	});
});

describe("field details (G1, M2-80)", () => {
	it("reads details as optional", () => {
		const base = { name: "endpoint_url", label: "Endpoint", required: true } as AgentTemplate["params"][number];
		expect(paramDetails(base)).toBeUndefined();
		expect(paramDetails({ ...base, details: "  " } as never)).toBeUndefined();
		expect(paramDetails({ ...base, details: "POST with JSON." } as never)).toBe("POST with JSON.");
	});
});
