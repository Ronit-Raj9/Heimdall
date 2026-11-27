import { describe, expect, it } from "vitest";
import { DRAFT_PREFIX, readDraft, storableDraft } from "@/lib/hooks/useFormDraft";

const store = (v: Record<string, string>) => ({ getItem: (k: string) => v[k] ?? null });

describe("form drafts (X-97)", () => {
	it("merges a stored draft over the initial value", () => {
		const s = store({ [DRAFT_PREFIX + "exp"]: JSON.stringify({ name: "nightly" }) });
		expect(readDraft(s, "exp", { name: "", budget: 5 })).toEqual({ name: "nightly", budget: 5 });
	});
	it("starts fresh on bad JSON or no storage", () => {
		expect(readDraft(store({ [DRAFT_PREFIX + "exp"]: "{bad" }), "exp", { name: "" })).toEqual({ name: "" });
		expect(readDraft(null, "exp", { name: "" })).toEqual({ name: "" });
	});
	it("never stores secret fields", () => {
		expect(storableDraft({ name: "a", token: "t", password: "p", apiKey: "k", clientSecret: "s" })).toEqual({ name: "a" });
	});
});
