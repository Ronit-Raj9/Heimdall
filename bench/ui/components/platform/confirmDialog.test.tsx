import { tagOf } from "@/lib/testing/render";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ConfirmDialog, confirmKind, createConfirmStore, initialFocusTarget, useConfirm } from "./confirmDialog";

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (k: string) => k }) }));

describe("confirm store", () => {
	it("resolves true on OK", async () => {
		const onChange = vi.fn();
		const store = createConfirmStore(onChange);
		const p = store.ask("Delete x?");
		expect(store.current()?.message).toBe("Delete x?");
		store.settle(true);
		await expect(p).resolves.toBe(true);
		expect(store.current()).toBeNull();
		expect(onChange).toHaveBeenLastCalledWith(null);
	});

	it("resolves false on Cancel", async () => {
		const store = createConfirmStore(() => undefined);
		const p = store.ask("Delete x?");
		store.settle(false);
		await expect(p).resolves.toBe(false);
	});

	it("resolves false on Escape and ignores other keys", async () => {
		const store = createConfirmStore(() => undefined);
		const p = store.ask("Delete x?");
		expect(store.onKey("Enter")).toBe(false);
		expect(store.current()).not.toBeNull();
		expect(store.onKey("Escape")).toBe(true);
		await expect(p).resolves.toBe(false);
		expect(store.onKey("Escape")).toBe(false);
	});

	it("cancels an open confirmation when a second one is asked", async () => {
		const store = createConfirmStore(() => undefined);
		const first = store.ask("one");
		const second = store.ask("two");
		await expect(first).resolves.toBe(false);
		store.settle(true);
		await expect(second).resolves.toBe(true);
	});
});

describe("ConfirmDialog", () => {
	it("renders an accessible alertdialog with OK and Cancel", () => {
		const html = renderToStaticMarkup(<ConfirmDialog message="Delete x?" onConfirm={() => undefined} onCancel={() => undefined} />);
		const dialog = tagOf(html, "confirm-dialog");
		expect(dialog).toContain('role="alertdialog"');
		expect(dialog).toContain('aria-modal="true"');
		expect(html).toContain("Delete x?");
		expect(html).toContain('data-testid="confirm-dialog-ok"');
		expect(html).toContain('data-testid="confirm-dialog-cancel"');
		expect(html).toContain("confirmDialog.ok");
	});

	it("uses custom labels", () => {
		const html = renderToStaticMarkup(
			<ConfirmDialog message="m" opts={{ title: "T", confirmLabel: "Yes", cancelLabel: "No" }} onConfirm={() => undefined} onCancel={() => undefined} />,
		);
		expect(html).toContain(">T<");
		expect(html).toContain(">Yes<");
		expect(html).toContain(">No<");
	});
});

describe("useConfirm without a provider", () => {
	it("accepts every confirmation", async () => {
		let fn: ReturnType<typeof useConfirm> | null = null;
		function Probe() {
			fn = useConfirm();
			return null;
		}
		renderToStaticMarkup(<Probe />);
		await expect(fn!("x")).resolves.toBe(true);
	});
});

describe("initial focus (Q-1)", () => {
	it("opens on Cancel for paid, destructive and plain confirms", () => {
		expect(initialFocusTarget("paid")).toBe("cancel");
		expect(initialFocusTarget("destructive")).toBe("cancel");
		expect(initialFocusTarget("plain")).toBe("cancel");
	});
	it("classifies options", () => {
		expect(confirmKind({ costUsd: 5 })).toBe("paid");
		expect(confirmKind({ costUsd: null })).toBe("paid");
		expect(confirmKind({ costUsd: 5, destructive: true })).toBe("paid");
		expect(confirmKind({ destructive: true })).toBe("destructive");
		expect(confirmKind({})).toBe("plain");
	});
});

