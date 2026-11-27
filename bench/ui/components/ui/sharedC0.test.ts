import { comboKeyTarget, filterCombo, groupCombo, type ComboboxItem } from "@/components/ui/combobox";
import { paidLabel } from "@/components/ui/paidButton";
import { createBusyGuard } from "@/lib/hooks/useBusyGuard";
import { describe, expect, it } from "vitest";

const items: ComboboxItem[] = [
	{ value: "gpt-x", label: "GPT X", group: "OpenAI" },
	{ value: "claude-y", label: "Model Y", group: "Anthropic", hint: "claude-y" },
	{ value: "zz-test-agent", label: "Zz test agent" },
	{ value: "off", label: "Off model", group: "OpenAI", disabled: true },
];

describe("filterCombo", () => {
	it("hides test objects unless asked, and counts them", () => {
		expect(filterCombo(items, "").shown.map((i) => i.value)).toEqual(["gpt-x", "claude-y", "off"]);
		expect(filterCombo(items, "").hidden).toBe(1);
		expect(filterCombo(items, "test").shown.map((i) => i.value)).toEqual(["zz-test-agent"]);
		expect(filterCombo(items, "", { value: "zz-test-agent" }).hidden).toBe(0);
		expect(filterCombo(items, "", { showTests: true }).shown).toHaveLength(4);
	});
	it("matches every word against label, value, hint and group", () => {
		expect(filterCombo(items, "anthropic claude").shown.map((i) => i.value)).toEqual(["claude-y"]);
		expect(filterCombo(items, "nothing").shown).toEqual([]);
	});
});

describe("groupCombo", () => {
	it("keeps first-seen group order with ungrouped first", () => {
		expect(groupCombo(items).map((s) => s.group)).toEqual(["", "OpenAI", "Anthropic"]);
	});
});

describe("comboKeyTarget", () => {
	it("skips disabled items and wraps", () => {
		expect(comboKeyTarget("ArrowDown", -1, items)).toBe(0);
		expect(comboKeyTarget("ArrowDown", 2, items)).toBe(0);
		expect(comboKeyTarget("ArrowUp", 0, items)).toBe(2);
		expect(comboKeyTarget("End", 0, items)).toBe(2);
		expect(comboKeyTarget("Enter", 0, items)).toBeNull();
		expect(comboKeyTarget("ArrowDown", -1, [{ value: "a", label: "a", disabled: true }])).toBeNull();
	});
});

describe("createBusyGuard", () => {
	it("starts one call while one is in flight", async () => {
		const seen: boolean[] = [];
		const guard = createBusyGuard((b) => seen.push(b));
		let calls = 0;
		let release!: () => void;
		const first = guard.run(() => new Promise<string>((r) => ((release = () => r("ok")), calls++)));
		const second = await guard.run(() => calls++);
		expect(second).toBeUndefined();
		release();
		expect(await first).toBe("ok");
		expect(calls).toBe(1);
		expect(seen).toEqual([true, false]);
		expect(await guard.run(() => 2)).toBe(2);
	});
});

describe("paidLabel capText", () => {
	it("uses the given cap text in place of the cap", () => {
		const t = (k: string) => k;
		expect(paidLabel(t, "Rerun", null, "same budget as before")).toBe("Rerun (same budget as before)");
		expect(paidLabel(t, "Rerun", null)).toBe("labelUnknown");
	});
});