import { describe, expect, it } from "vitest";
import { withoutTests } from "@/lib/testObjects";
import type { ReleaseRecord } from "@/lib/types/bench";
import { isTestRelease } from "./releasesView";

const rec = (p: Partial<ReleaseRecord>): ReleaseRecord => ({ release_id: "r", exp_id: "e_1", agent_config_id: "codex", approver: "ana", created_at: "2026-11-28T00:00:00Z", ...p }) as ReleaseRecord;

describe("releases hide test agents (K3)", () => {
	it("hides releases of zz-test agents by default and counts them", () => {
		const list = [rec({ agent_id: "zz-test-r7-q2-agent", version: 1 }), rec({ agent_id: "codex", version: 2 }), rec({ agent_config_id: "agents/zz-test-x.yaml" })];
		const { shown, hidden } = withoutTests(list, isTestRelease, false);
		expect(shown.map((r) => r.agent_id)).toEqual(["codex"]);
		expect(hidden).toBe(2);
		expect(withoutTests(list, isTestRelease, true).shown).toHaveLength(3);
	});
});
