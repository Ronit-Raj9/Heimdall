import { describe, expect, it, vi } from "vitest";
import { render } from "@/lib/testing/render";
import { archiveFlow, unarchiveFlow } from "./archiveAction";
import { ShowArchivedChip, showArchivedLabelKey, withoutArchived } from "./showArchivedChip";
import { ARCHIVE_ROLE, archivedCountOf, archivedParam, archivePath, archiveTags, isArchived } from "@/lib/store/apis/archiveApi";
import { listEvaluationsSearch } from "@/lib/store/apis/evaluationsApi";
import { listExperimentsSearch } from "@/lib/store/apis/benchApi";
import { runsRequest } from "@/app/runs/views/runsTable";
import { deleteMonitorTestFlow } from "@/app/monitors/views/monitorTestDelete";
import en from "@/locales/en/ufd5.json";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const texts = { question: "q", title: "Archive x?", confirm: "Archive", cancel: "Keep it", done: "Archived x" };

describe("archive API shapes", () => {
	it("builds the archive and unarchive routes per kind", () => {
		expect(archivePath("evaluation", "ev 1")).toBe("/evaluations/ev%201/archive");
		expect(archivePath("experiment", "e1", true)).toBe("/experiments/e1/unarchive");
		expect(archivePath("run", "r1")).toBe("/runs/r1/archive");
		expect(archivePath("improvement", "i1")).toBe("/improvements/i1/archive");
		expect(archivePath("finding", "f1", true)).toBe("/findings/f1/unarchive");
	});
	it("refreshes the kind's list and the object", () => {
		expect(archiveTags("run", "r1")).toEqual([
			{ type: "BenchRun", id: "LIST" },
			{ type: "BenchRun", id: "r1" },
		]);
		expect(archiveTags("evaluation", "e").map((t) => t.type)).toContain("BenchExperiment");
	});
	it("offers archive to the role D1's server allows", () => {
		expect(ARCHIVE_ROLE.experiment).toBe("admin");
		expect(ARCHIVE_ROLE.run).toBe("admin");
		expect(ARCHIVE_ROLE.finding).toBe("member");
	});
	it("reads archived_at as optional", () => {
		expect(isArchived({})).toBe(false);
		expect(isArchived({ archived_at: null })).toBe(false);
		expect(isArchived({ archived_at: "2026-11-27T10:00:00Z" })).toBe(true);
		expect(isArchived(undefined)).toBe(false);
	});
	it("sends archived=include only when the switch is on", () => {
		expect(archivedParam(false)).toEqual({});
		expect(archivedParam(true)).toEqual({ archived: "include" });
		expect(listEvaluationsSearch({ archived: "include" })).toEqual({ archived: "include" });
		expect(listEvaluationsSearch({})).toEqual({});
		expect(listExperimentsSearch({ arm_stats: true, archived: "only" })).toEqual({ arm_stats: "1", archived: "only" });
		expect(runsRequest({ archived: "1" }, null)).toEqual({ limit: 50, archived: "include" });
		expect(runsRequest({}, null)).toEqual({ limit: 50 });
	});
	it("takes the server's archived count when it gives one", () => {
		expect(archivedCountOf({ archived_count: 3 })).toBe(3);
		expect(archivedCountOf({ archived: 2 })).toBe(2);
		expect(archivedCountOf({ experiments: [] })).toBeUndefined();
		expect(archivedCountOf([])).toBeUndefined();
		expect(archivedCountOf(null)).toBeUndefined();
	});
});

describe("archive flow", () => {
	it("confirms, archives and offers Undo that unarchives", async () => {
		const confirm = vi.fn(async () => true);
		const archive = vi.fn(async () => undefined);
		const unarchive = vi.fn(async () => undefined);
		const notify = vi.fn(() => 1);
		expect(await archiveFlow({ confirm, archive, unarchive, notify, texts })).toBeNull();
		expect(confirm).toHaveBeenCalledWith("q", { title: "Archive x?", confirmLabel: "Archive", cancelLabel: "Keep it" });
		expect(archive).toHaveBeenCalledOnce();
		const toast = (notify.mock.calls[0] as unknown as [{ text: string; undo: () => Promise<unknown> }])[0];
		expect(toast.text).toBe("Archived x");
		await toast.undo();
		expect(unarchive).toHaveBeenCalledOnce();
	});
	it("does nothing when kept", async () => {
		const archive = vi.fn(async () => undefined);
		expect(await archiveFlow({ confirm: async () => false, archive, unarchive: async () => undefined, notify: vi.fn(() => 1), texts })).toBe("cancelled");
		expect(archive).not.toHaveBeenCalled();
	});
	it("returns the server's reason when it refuses", async () => {
		const notify = vi.fn(() => 1);
		const out = await archiveFlow({
			confirm: async () => true,
			archive: async () => Promise.reject({ status: 409, data: { error: { message: "the run is still running" } } }),
			unarchive: async () => undefined,
			notify,
			texts,
		});
		expect(out).toContain("still running");
		expect(notify).not.toHaveBeenCalled();
	});
	it("unarchives without a confirm and says so", async () => {
		const notify = vi.fn(() => 1);
		expect(await unarchiveFlow(async () => undefined, "Brought back x", notify)).toBeNull();
		expect(notify).toHaveBeenCalledWith({ text: "Brought back x" });
	});
});

describe("Show archived switch", () => {
	it("hides archived rows unless on and counts them", () => {
		const rows = [{ id: "a" }, { id: "b", archived_at: "2026-11-01T00:00:00Z" }];
		expect(withoutArchived(rows, isArchived, false)).toEqual({ shown: [{ id: "a" }], archived: 1 });
		expect(withoutArchived(rows, isArchived, true).shown).toHaveLength(2);
	});
	it("labels with the count when known", () => {
		expect(showArchivedLabelKey(false, 4)).toEqual({ key: "archive.showCount", count: 4 });
		expect(showArchivedLabelKey(false, undefined)).toEqual({ key: "archive.show" });
		expect(showArchivedLabelKey(true, 4)).toEqual({ key: "archive.hide" });
		expect(en.archive.showCount).toBe("Show archived ({{count}})");
	});
	it("renders nothing when the server says none are archived", () => {
		expect(render(<ShowArchivedChip count={0} on={false} onChange={() => undefined} />)).toBe("");
		expect(render(<ShowArchivedChip count={2} on={false} onChange={() => undefined} />)).toContain('aria-pressed="false"');
	});
	it("says what archive means, in plain words", () => {
		expect(en.archive.body).toBe("Hidden from lists. Nothing is deleted. You can bring it back.");
		for (const v of [...Object.values(en.archive), ...Object.values(en.monitorDelete)]) expect(v).not.toMatch(/[–—]/);
	});
});

describe("monitor test delete", () => {
	const opts = { destructive: true as const, title: "Delete safety rule test m?", confirmLabel: "Delete test", cancelLabel: "Keep it" };
	it("asks with the destructive confirm that names the test, then deletes", async () => {
		const confirm = vi.fn(async () => true);
		const remove = vi.fn(async () => undefined);
		expect(await deleteMonitorTestFlow(confirm, remove, { question: "q", opts })).toBe("deleted");
		expect(confirm).toHaveBeenCalledWith("q", opts);
		expect(remove).toHaveBeenCalledOnce();
	});
	it("keeps it on cancel and shows a refusal", async () => {
		const remove = vi.fn(async () => undefined);
		expect(await deleteMonitorTestFlow(async () => false, remove, { question: "q", opts })).toBe("cancelled");
		expect(remove).not.toHaveBeenCalled();
		const out = await deleteMonitorTestFlow(async () => true, async () => Promise.reject({ status: 403, data: { error: { message: "admins only" } } }), { question: "q", opts });
		expect(out).toEqual({ error: expect.stringContaining("admins only") });
	});
});
