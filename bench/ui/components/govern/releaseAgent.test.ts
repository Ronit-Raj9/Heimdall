import "@/lib/i18n";

import { liveNow } from "@/app/releases/views/releasesView";
import i18n from "@/lib/i18n";
import type { ReleaseRecord } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { releaseOptionLabel } from "./releaseActions";
import { liveReleases, releaseAgent } from "./releaseAgent";

const base = { release_id: "rel_mgztv8h0e9", agent_config_id: "codex", created_at: "2026-11-21T10:00:00Z", approver: "ana" } as ReleaseRecord;

describe("releaseAgent (Q-16)", () => {
	it("reads agent_id and version first", () => {
		expect(releaseAgent({ agent_id: "codex", version: 2, agent_config_id: "other" })).toEqual({ agent: "codex", version: 2 });
	});
	it("names a comparison release from its agent_config_id", () => {
		expect(releaseAgent({ agent_config_id: "codex" })).toEqual({ agent: "codex", version: 0 });
		expect(releaseAgent({ agent_config_id: "agents/codex.yaml@v3" })).toEqual({ agent: "codex", version: 3 });
		expect(releaseAgent({ agent_config_id: "" })).toBeNull();
	});
	it("counts both kinds for Live now, newest per agent", () => {
		const t = i18n.getFixedT("en", "uf7b") as never;
		expect(liveNow([base], t)).toEqual(["codex (released 21 Nov by ana)"]);
		const later = { ...base, release_id: "rel_2", agent_id: "codex", version: 2, created_at: "2026-11-23T10:00:00Z" };
		expect(liveReleases([base, later]).map((x) => x.release.release_id)).toEqual(["rel_2"]);
	});
	it("labels the record select with the agent, not the release id", () => {
		expect(releaseOptionLabel(base)).toBe("codex, 21 Nov 2026");
	});
});

describe("compliance hint (Q-210, Q-225)", () => {
	it("names no project folder", () => {
		expect(i18n.getFixedT("en", "govern")("releases.complianceHint")).toBe("Makes a report you can download below.");
	});
});
