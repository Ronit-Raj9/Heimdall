import "@/lib/i18n";

import { describe, expect, it, vi } from "vitest";
import { deleteBackup } from "./operateView";

describe("delete backup", () => {
	it("never deletes when the confirmation is refused", async () => {
		const remove = vi.fn();
		const confirm = vi.fn(() => Promise.resolve(false));
		await deleteBackup(confirm, remove, "backup-1.tar.gz");
		expect(confirm).toHaveBeenCalledWith("Delete the copy backup-1.tar.gz? It cannot be brought back.", {
			destructive: true,
			confirmLabel: "Delete",
		});
		expect(remove).not.toHaveBeenCalled();
	});

	it("deletes after a yes", async () => {
		const remove = vi.fn();
		await deleteBackup(() => Promise.resolve(true), remove, "backup-1.tar.gz");
		expect(remove).toHaveBeenCalledWith("backup-1.tar.gz");
	});

	it("passes a failed delete on to the caller (M1-44)", async () => {
		const remove = vi.fn(() => Promise.reject(new Error("disk busy")));
		await expect(deleteBackup(() => Promise.resolve(true), remove, "backup-1.tar.gz")).rejects.toThrow("disk busy");
	});
});
