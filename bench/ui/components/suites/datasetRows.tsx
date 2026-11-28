import { BlockSkeleton, EmptyNote, InlineError, LoadError, Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Pager, pageWindow } from "@/components/ui/pager";
import {
	useDeleteDatasetTaskMutation,
	useGetDatasetVersionQuery,
	useListDatasetTasksQuery,
	useWriteDatasetTasksMutation,
} from "@/lib/store/apis/ufl5Api";
import type { DatasetRowInput, DatasetTaskRow, DatasetTasksRequest, DatasetTasksResult } from "@/lib/types/ufl5";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import "./ufl5I18n";

const areaClass = "border-input bg-background min-h-20 w-full rounded-md border px-3 py-2 text-sm";

function Textarea(props: React.TextareaHTMLAttributes<HTMLTextAreaElement>) {
	return <textarea className={areaClass} {...props} />;
}

export const ROWS_PAGE = 50;

/** The row form: id (optional when adding), question, accepted answers one per line. */
export interface RowForm {
	id: string;
	input: string;
	expected: string;
}

export const EMPTY_ROW: RowForm = { id: "", input: "", expected: "" };

/**
 * The body POST /suites/{id}/dataset/tasks reads (handlers.DatasetTasksRequest): one row; mode add for a
 * new row (a taken id is a problem), upsert to replace the row of that id.
 */
export function rowRequest(f: RowForm, replace: boolean): DatasetTasksRequest {
	const row: DatasetRowInput = { input: f.input.trim() };
	const id = f.id.trim();
	if (id) row.id = id;
	const expected = f.expected
		.split("\n")
		.map((s) => s.trim())
		.filter(Boolean);
	if (expected.length) row.expected = expected;
	return { tasks: [row], mode: replace ? "upsert" : "add", source: "web" };
}

/** The server's problems with the rows, or null when it wrote them. */
export function rowProblems(r: DatasetTasksResult): string | null {
	const p = r.problems ?? [];
	return p.length ? p.map((x) => x.message).join("; ") : null;
}

/** Rows of a question-and-answer benchmark: list a page, add, replace, delete. */
export function DatasetRowsSection({ suite, writable }: { suite: string; writable: boolean }) {
	const { t } = useTranslation("ufl5");
	const [page, setPage] = useState(1);
	const q = useListDatasetTasksQuery({ suite, offset: (page - 1) * ROWS_PAGE, limit: ROWS_PAGE });
	const [edit, setEdit] = useState<{ form: RowForm; replace: boolean } | null>(null);
	const [del, setDel] = useState<string | null>(null);
	const rows = q.data?.tasks ?? [];
	const win = pageWindow(q.data?.total ?? 0, page, ROWS_PAGE);
	return (
		<Section
			title={t("rows.title")}
			testId="suite-data-rows"
			actions={
				writable ? (
					<Button size="sm" variant="outline" onClick={() => setEdit({ form: EMPTY_ROW, replace: false })} data-testid="suite-data-rows-add">
						{t("rows.add")}
					</Button>
				) : undefined
			}
		>
			{q.isLoading ? (
				<BlockSkeleton rows={3} testId="suite-data-rows-loading" />
			) : q.error ? (
				<LoadError title={t("rows.loadError")} error={q.error} onRetry={() => void q.refetch()} testId="suite-data-rows-error" />
			) : rows.length === 0 ? (
				<EmptyNote testId="suite-data-rows-empty">{t("rows.empty")}</EmptyNote>
			) : (
				<>
					<div className="bg-card min-w-0 overflow-x-auto rounded-xl border">
						<table className="w-full text-sm">
							<thead className="text-muted-foreground border-b text-left text-xs">
								<tr>
									<th className="px-4 py-2 font-medium">{t("rows.task")}</th>
									<th className="hidden px-4 py-2 font-medium sm:table-cell">{t("rows.titleCol")}</th>
									<th className="px-4 py-2 text-right font-medium">
										<span className="sr-only">{t("rows.actions")}</span>
									</th>
								</tr>
							</thead>
							<tbody>
								{rows.map((r) => (
									<RowLine key={r.id} r={r} writable={writable} onReplace={() => setEdit({ form: { ...EMPTY_ROW, id: r.id }, replace: true })} onDelete={() => setDel(r.id)} />
								))}
							</tbody>
						</table>
					</div>
					<Pager
						page={win.page}
						pageCount={win.pageCount}
						onPage={setPage}
						rangeText={t("rows.range", { first: win.first, last: win.last, total: q.data?.total ?? 0 })}
						testId="suite-data-rows-pager"
					/>
				</>
			)}
			{edit && <RowDialog suite={suite} form={edit.form} replace={edit.replace} onClose={() => setEdit(null)} />}
			{del && <DeleteDialog suite={suite} task={del} onClose={() => setDel(null)} />}
		</Section>
	);
}

function RowLine({ r, writable, onReplace, onDelete }: { r: DatasetTaskRow; writable: boolean; onReplace(): void; onDelete(): void }) {
	const { t } = useTranslation("ufl5");
	return (
		<tr className="border-b last:border-0" data-testid={`suite-data-row-${r.id}`}>
			<td className="max-w-48 truncate px-4 py-2 font-mono text-xs" title={r.id}>
				{r.id}
			</td>
			<td className="text-muted-foreground hidden max-w-96 truncate px-4 py-2 sm:table-cell">{r.title || "-"}</td>
			<td className="px-4 py-2 text-right whitespace-nowrap">
				{!r.from_rows ? (
					<span className="text-muted-foreground text-xs">{t("rows.byHand")}</span>
				) : (
					writable && (
						<span className="inline-flex gap-1">
							<Button size="sm" variant="ghost" onClick={onReplace} data-testid={`suite-data-row-replace-${r.id}`}>
								{t("rows.replace")}
							</Button>
							<Button size="sm" variant="ghost" className="text-danger-ink" onClick={onDelete} data-testid={`suite-data-row-delete-${r.id}`}>
								{t("rows.delete")}
							</Button>
						</span>
					)
				)}
			</td>
		</tr>
	);
}

function RowDialog({ suite, form: form0, replace, onClose }: { suite: string; form: RowForm; replace: boolean; onClose(): void }) {
	const { t } = useTranslation("ufl5");
	const [form, setForm] = useState(form0);
	const [write, st] = useWriteDatasetTasksMutation();
	const [error, setError] = useState<unknown>(null);
	const [problem, setProblem] = useState<string | null>(null);
	const save = async () => {
		setError(null);
		setProblem(null);
		try {
			const res = await write({ suite, ...rowRequest(form, replace) }).unwrap();
			const why = rowProblems(res);
			if (why) setProblem(why);
			else onClose();
		} catch (e) {
			setError(e);
		}
	};
	return (
		<Dialog
			open
			onOpenChange={(o) => !o && onClose()}
			title={replace ? t("rows.replaceTitle", { id: form.id }) : t("rows.addTitle")}
			closeLabel={t("rows.close")}
			testId="suite-data-row-dialog"
			footer={
				<>
					<Button variant="outline" onClick={onClose}>
						{t("rows.cancel")}
					</Button>
					<Button disabled={!form.input.trim() || st.isLoading} onClick={() => void save()} data-testid="suite-data-row-save">
						{st.isLoading ? t("rows.saving") : t("rows.save")}
					</Button>
				</>
			}
		>
			<div className="flex flex-col gap-3 text-sm">
				{!replace && (
					<label className="flex flex-col gap-1">
						<span className="text-muted-foreground">{t("rows.id")}</span>
						<Input value={form.id} onChange={(e) => setForm({ ...form, id: e.target.value })} data-testid="suite-data-row-id" />
					</label>
				)}
				<label className="flex flex-col gap-1">
					<span className="text-muted-foreground">{t("rows.question")}</span>
					<Textarea rows={4} value={form.input} onChange={(e) => setForm({ ...form, input: e.target.value })} data-testid="suite-data-row-input" />
				</label>
				<label className="flex flex-col gap-1">
					<span className="text-muted-foreground">{t("rows.expected")}</span>
					<Textarea rows={3} value={form.expected} onChange={(e) => setForm({ ...form, expected: e.target.value })} data-testid="suite-data-row-expected" />
					<span className="text-muted-foreground text-xs">{t("rows.expectedHint")}</span>
				</label>
				{problem && (
					<p role="alert" className="text-danger-ink text-sm" data-testid="suite-data-row-problem">
						{problem}
					</p>
				)}
				<InlineError error={error} testId="suite-data-row-error" />
			</div>
		</Dialog>
	);
}

function DeleteDialog({ suite, task, onClose }: { suite: string; task: string; onClose(): void }) {
	const { t } = useTranslation("ufl5");
	const [remove, st] = useDeleteDatasetTaskMutation();
	const [error, setError] = useState<unknown>(null);
	const go = async () => {
		setError(null);
		try {
			await remove({ suite, task }).unwrap();
			onClose();
		} catch (e) {
			setError(e);
		}
	};
	return (
		<Dialog
			open
			role="alertdialog"
			hideClose
			size="sm"
			onOpenChange={(o) => !o && onClose()}
			title={t("rows.deleteTitle", { id: task })}
			description={t("rows.deleteHint")}
			closeLabel={t("rows.close")}
			testId="suite-data-row-delete-dialog"
			footer={
				<>
					<Button variant="outline" onClick={onClose}>
						{t("rows.cancel")}
					</Button>
					<Button variant="destructive" disabled={st.isLoading} onClick={() => void go()} data-testid="suite-data-row-delete-confirm">
						{t("rows.delete")}
					</Button>
				</>
			}
		>
			<InlineError error={error} testId="suite-data-row-delete-error" />
		</Dialog>
	);
}

/** One version's changes (GET /suites/{id}/dataset/versions/{n}): the ids added, replaced and removed. */
export function DatasetVersionChanges({ suite, n }: { suite: string; n: number | null }) {
	const { t } = useTranslation("ufl5");
	const q = useGetDatasetVersionQuery(n == null ? skipToken : { suite, n });
	if (n == null) return null;
	if (q.isLoading) return <BlockSkeleton rows={2} testId="suite-data-version-detail-loading" />;
	if (q.error) return <LoadError title={t("versions.loadError")} error={q.error} onRetry={() => void q.refetch()} testId="suite-data-version-detail-error" />;
	const v = q.data?.version;
	if (!v) return null;
	const parts: [string, string[] | null][] = [
		[t("versions.added"), v.added],
		[t("versions.updated"), v.updated],
		[t("versions.removed"), v.removed],
	];
	return (
		<dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-sm" data-testid="suite-data-version-detail">
			{parts.map(([label, ids]) => (
				<div key={label} className="contents">
					<dt className="text-muted-foreground">{label}</dt>
					<dd className="font-mono text-xs break-all">{ids && ids.length ? ids.join(", ") : "-"}</dd>
				</div>
			))}
			<dt className="text-muted-foreground">{t("versions.total")}</dt>
			<dd className="tabular-nums">{Object.keys(q.data?.tasks ?? {}).length || v.tasks}</dd>
		</dl>
	);
}
