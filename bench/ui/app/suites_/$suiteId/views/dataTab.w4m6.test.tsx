import "@/lib/i18n";

import { mutation, render } from "@/lib/testing/render";
import type { DataPolicyView } from "@/lib/types/annotation";
import i18n from "i18next";
import { describe, expect, it, vi } from "vitest";
import { PolicyForm, apiSnippet } from "./dataTab";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const calls: unknown[] = [];
vi.mock("@/lib/store/apis/annotationApi", () => ({
	useSetDataPolicyMutation: (opts?: unknown) => {
		calls.push(opts);
		return mutation({ isSuccess: true });
	},
}));

const view: DataPolicyView = {
	benchmark: "support-faq",
	policy: { pii: { mode: "warn" }, access: { mode: "org" }, updated_at: "2026-11-26T10:00:00Z" },
	can_edit_access: false,
	modes: null,
	kinds: null,
} as unknown as DataPolicyView;

const t = i18n.getFixedT("en", "benchData") as never;

describe("data policy save state (p11-3)", () => {
	it("keeps Saved. across the remount after the refetch", () => {
		calls.length = 0;
		const html = render(<PolicyForm view={view} writable t={t} />);
		expect(calls[0]).toEqual({ fixedCacheKey: "data-policy-support-faq" });
		expect(html).toContain("Saved.");
	});
});

describe("Manage from code snippet (p11-7c)", () => {
	it("imports os and reads the URL from the environment", () => {
		const lines = apiSnippet("faq").split("\n");
		expect(lines.slice(0, 4)).toEqual([
			"import os",
			"from heimdall import V1Client",
			'hd = V1Client(os.environ["HEIMDALL_URL"], token=os.environ["HEIMDALL_TOKEN"])',
			'commit = os.environ.get("GIT_COMMIT", "local")',
		]);
	});
});