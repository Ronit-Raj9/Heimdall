import { LabeledModelPicker } from "@/components/models/labeledModelPicker";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { benchApi, getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useGetRowImportQuery, useImportSuiteRowsMutation } from "@/lib/store/apis/suiteBuildApi";
import { useAppDispatch } from "@/lib/store/hooks";
import type {
	AnswerMatch,
	GradeOn,
	MapField,
	RowAttachment,
	RowColumn,
	RowFormat,
	RowImportRequest,
	RowImportResult,
	RowImportStatus,
	RowMapping,
} from "@/lib/types/suiteBuild";
import { useEffect, useRef, useState, type ChangeEvent } from "react";
import { useTranslation } from "react-i18next";

// Row import with a column mapping step, attached files and background imports (lane Y13).
// Shared by the Build tab of a benchmark and the evaluate wizard's "Your own tasks" card.

export type T = ReturnType<typeof useTranslation<"suiteBuild">>["t"];

export const selectClass = "border-input bg-background h-9 rounded-md border px-2 text-sm";

export function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
	return (
		<label className="flex min-w-0 flex-col gap-1 text-sm">
			<span className="text-muted-foreground">{label}</span>
			{children}
			{hint && <span className="text-muted-foreground text-xs">{hint}</span>}
		</label>
	);
}

export function ErrorLine({ error, testId }: { error: unknown; testId: string }) {
	if (!error) return null;
	return (
		<p role="alert" className="text-chart-error-ink text-xs" data-testid={testId}>
			{getBenchErrorMessage(error as never)}
		</p>
	);
}

/** Judge provider, model and votes inputs, shared by the grader section and the wizard. */
export function JudgeFields({
	provider,
	model,
	votes,
	disabled,
	onChange,
	t,
}: {
	provider: string;
	model: string;
	votes: number;
	disabled?: boolean;
	onChange: (patch: { provider?: string; model?: string; votes?: number }) => void;
	t: T;
}) {
	return (
		<div className="grid min-w-0 grid-cols-1 gap-2 sm:grid-cols-3" data-testid="suite-build-judge">
			<div className="min-w-0 sm:col-span-2">
				<LabeledModelPicker
					label={t("grader.model")}
					value={provider || model ? { provider, model } : null}
					disabled={disabled}
					onChange={(v) => onChange({ provider: v.provider, model: v.model })}
					testId="suite-build-judge-model"
				/>
			</div>
			<Field label={t("grader.votes")} hint={t("grader.votesHint")}>
				<select className={selectClass} value={votes} disabled={disabled} onChange={(e) => onChange({ votes: Number(e.target.value) })}>
					{[1, 3, 5].map((n) => (
						<option key={n} value={n}>
							{n}
						</option>
					))}
				</select>
			</Field>
			<p className="text-muted-foreground text-xs sm:col-span-3">{t("grader.pinHint")}</p>
		</div>
	);
}

/** CSV or JSONL from the file's name; null when the name says neither. */
export function formatOfFile(name: string): "csv" | "jsonl" | null {
	const n = name.toLowerCase();
	if (n.endsWith(".csv") || n.endsWith(".tsv")) return "csv";
	if (n.endsWith(".jsonl") || n.endsWith(".ndjson") || n.endsWith(".json")) return "jsonl";
	return null;
}

const MiB = 1024 * 1024;
export const LIMITS = { syncData: 8 * MiB, data: 48 * MiB, file: 5 * MiB, files: 40 * MiB } as const;

/** Fields shown in the mapping step, in display order. */
export const MAP_FIELDS = ["question", "messages", "expected", "rubric", "reference", "wrong_answers", "user_turns", "attachments", "id", "title", "split"] as const satisfies readonly MapField[];

/** Size of text in bytes as sent (UTF-8). */
export const byteSize = (s: string) => new TextEncoder().encode(s).length;

/** How data must be sent: inline, in the background, or not at all (over the limit). */
export function dataMode(bytes: number): "sync" | "async" | "too_big" {
	if (bytes > LIMITS.data) return "too_big";
	return bytes > LIMITS.syncData ? "async" : "sync";
}

export function formatBytes(n: number): string {
	if (n < 1024) return `${n} B`;
	if (n < MiB) return `${(n / 1024).toFixed(1)} KiB`;
	return `${(n / MiB).toFixed(1)} MiB`;
}

export interface PickedFile {
	name: string;
	size: number;
	media_type?: string;
	content_base64: string;
}

/** The first limit the files break, as a message key and its values; null when they fit. */
export function attachmentProblem(files: readonly Pick<PickedFile, "name" | "size">[]): { key: "attach.tooBig" | "attach.tooMuch"; name?: string } | null {
	const big = files.find((f) => f.size > LIMITS.file);
	if (big) return { key: "attach.tooBig", name: big.name };
	if (files.reduce((n, f) => n + f.size, 0) > LIMITS.files) return { key: "attach.tooMuch" };
	return null;
}

/** Request attachments keyed by file name. */
export function attachmentsBody(files: readonly PickedFile[]): Record<string, RowAttachment> | undefined {
	if (files.length === 0) return undefined;
	return Object.fromEntries(files.map((f) => [f.name, { content_base64: f.content_base64, ...(f.media_type ? { media_type: f.media_type } : {}) }]));
}

function readBase64(file: File): Promise<string> {
	return new Promise((resolve, reject) => {
		const r = new FileReader();
		r.onload = () => {
			const s = String(r.result ?? "");
			resolve(s.slice(s.indexOf(",") + 1));
		};
		r.onerror = () => reject(r.error);
		r.readAsDataURL(file);
	});
}

/** Whether the grade-on choice applies: some preview task is a conversation. */
export function hasConversations(result: Pick<RowImportResult, "tasks"> | null): boolean {
	return Boolean(result?.tasks.some((x) => (x.turns ?? 0) > 1 || x.grade_on));
}

/** Option text of a column: its path, kind and a short example. */
export function columnLabel(c: RowColumn, t: T): string {
	const kind = t(`rows.map.kind.${c.kind}`);
	const ex = c.example ? `: ${c.example.length > 40 ? `${c.example.slice(0, 40)}...` : c.example}` : "";
	return `${c.path} (${kind})${ex}`;
}

/** Mapping step: one column per task field, metadata columns, and the remember checkbox. */
export function MappingStep({
	columns,
	mapping,
	onChange,
	saveMapping,
	onSaveMapping,
	showGradeOn,
	gradeOn,
	onGradeOn,
	disabled,
	fields = MAP_FIELDS,
	showMetadata = true,
	t,
}: {
	columns: readonly RowColumn[];
	mapping: RowMapping;
	onChange: (m: RowMapping) => void;
	saveMapping?: boolean;
	/** Absent: no remember checkbox (the Add benchmark page saves the mapping in the draft's plan). */
	onSaveMapping?: (v: boolean) => void;
	showGradeOn: boolean;
	gradeOn?: GradeOn;
	onGradeOn?: (g: GradeOn) => void;
	disabled?: boolean;
	/** The task fields offered; default every field the Build tab import reads. */
	fields?: readonly (typeof MAP_FIELDS)[number][];
	showMetadata?: boolean;
	t: T;
}) {
	const meta = new Set(mapping.metadata ?? []);
	const toggleMeta = (path: string) => {
		const next = meta.has(path) ? [...meta].filter((p) => p !== path) : [...meta, path];
		onChange({ ...mapping, metadata: next });
	};
	return (
		<div className="flex min-w-0 flex-col gap-3 rounded-md border px-3 py-3" data-testid="suite-build-mapping">
			<div>
				<p className="text-sm font-medium">{t("rows.map.title")}</p>
				<p className="text-muted-foreground text-xs">{t("rows.map.help")}</p>
			</div>
			<div className="grid min-w-0 grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
				{fields.map((f) => (
					<Field key={f} label={t(`rows.map.field.${f}`)}>
						<select
							className={selectClass}
							value={mapping[f] ?? ""}
							disabled={disabled}
							onChange={(e) => onChange({ ...mapping, [f]: e.target.value })}
							data-testid={`suite-build-map-${f}`}
						>
							<option value="">{t("rows.map.auto")}</option>
							<option value="-">{t("rows.map.skip")}</option>
							{columns.map((c) => (
								<option key={c.path} value={c.path}>
									{columnLabel(c, t)}
								</option>
							))}
						</select>
					</Field>
				))}
			</div>
			{showMetadata && columns.length > 0 && (
				<fieldset className="flex min-w-0 flex-col gap-1" data-testid="suite-build-map-metadata">
					<legend className="text-muted-foreground text-sm">{t("rows.map.metadata")}</legend>
					<div className="flex flex-wrap gap-x-4 gap-y-1">
						{columns.map((c) => (
							<label key={c.path} className="flex items-center gap-1.5 font-mono text-xs">
								<input type="checkbox" checked={meta.has(c.path)} disabled={disabled} onChange={() => toggleMeta(c.path)} />
								{c.path}
							</label>
						))}
					</div>
					<p className="text-muted-foreground text-xs">{t("rows.map.metadataHint")}</p>
				</fieldset>
			)}
			{showGradeOn && (
				<fieldset className="flex flex-wrap items-center gap-4 text-sm" data-testid="suite-build-grade-on">
					<legend className="text-muted-foreground mb-1">{t("rows.map.gradeOn")}</legend>
					{(["final", "transcript"] as const).map((g) => (
						<label key={g} className="flex items-center gap-1.5">
							<input type="radio" name="grade-on" checked={gradeOn === g} disabled={disabled} onChange={() => onGradeOn?.(g)} data-testid={`suite-build-grade-on-${g}`} />
							{g === "final" ? t("rows.map.gradeOnFinal") : t("rows.map.gradeOnTranscript")}
						</label>
					))}
				</fieldset>
			)}
			{onSaveMapping && (
				<label className="flex items-center gap-2 text-sm">
					<input type="checkbox" checked={!!saveMapping} disabled={disabled} onChange={(e) => onSaveMapping(e.target.checked)} data-testid="suite-build-map-save" />
					{t("rows.map.save")}
				</label>
			)}
		</div>
	);
}

/** File input for the files rows name, with names, sizes and the size limits. */
export function AttachmentsInput({ files, onChange, disabled, t }: { files: readonly PickedFile[]; onChange: (f: PickedFile[]) => void; disabled?: boolean; t: T }) {
	const [problem, setProblem] = useState("");
	const onPick = async (e: ChangeEvent<HTMLInputElement>) => {
		const picked = Array.from(e.target.files ?? []);
		e.target.value = "";
		if (picked.length === 0) return;
		const merged = [...files.filter((f) => !picked.some((p) => p.name === f.name)), ...picked.map((p) => ({ name: p.name, size: p.size }))];
		const bad = attachmentProblem(merged);
		if (bad) {
			setProblem(t(`rows.${bad.key}`, { name: bad.name }));
			return;
		}
		setProblem("");
		const read = await Promise.all(picked.map(async (p) => ({ name: p.name, size: p.size, media_type: p.type || undefined, content_base64: await readBase64(p) })));
		onChange([...files.filter((f) => !picked.some((p) => p.name === f.name)), ...read]);
	};
	return (
		<div className="flex min-w-0 flex-col gap-1" data-testid="suite-build-attachments">
			<Field label={t("rows.attach.label")} hint={t("rows.attach.help")}>
				<input type="file" multiple disabled={disabled} onChange={(e) => void onPick(e)} className="text-sm" data-testid="suite-build-attachments-file" />
			</Field>
			{files.length > 0 && (
				<ul className="flex flex-col gap-0.5 text-xs">
					{files.map((f) => (
						<li key={f.name} className="flex items-center gap-2" data-testid={`suite-build-attachment-${f.name}`}>
							<span className="font-mono">{f.name}</span>
							<span className="text-muted-foreground tabular-nums">{formatBytes(f.size)}</span>
							<button type="button" className="text-primary hover:underline" onClick={() => onChange(files.filter((x) => x.name !== f.name))}>
								{t("rows.attach.remove")}
							</button>
						</li>
					))}
				</ul>
			)}
			{problem && (
				<p role="alert" className="text-chart-error-ink text-xs" data-testid="suite-build-attachments-error">
					{problem}
				</p>
			)}
		</div>
	);
}

/** Progress of a background import: bar, status, problems and what to do next. */
export function ImportProgress({ status, t }: { status: RowImportStatus; t: T }) {
	const pct = status.total > 0 ? Math.min(100, Math.round((status.written / status.total) * 100)) : 0;
	const failed = status.status === "failed" || status.status === "interrupted";
	return (
		<div role="status" className="flex min-w-0 flex-col gap-2 rounded-md border px-3 py-2 text-sm" data-testid="suite-build-import-progress">
			<div className="flex flex-wrap items-center gap-2">
				<span className="font-medium">{t("rows.progress.title")}</span>
				<Badge variant={status.status === "done" ? "success" : failed ? "destructive" : "info"} data-testid="suite-build-import-status">
					{t(`rows.progress.status.${status.status}`)}
				</Badge>
				<span className="text-muted-foreground ml-auto text-xs tabular-nums">{t("rows.progress.count", { written: status.written, total: status.total })}</span>
			</div>
			<div className="bg-muted h-2 w-full overflow-hidden rounded-full" role="progressbar" aria-valuemin={0} aria-valuemax={status.total} aria-valuenow={status.written}>
				<div className="bg-primary h-full transition-[width]" style={{ width: `${pct}%` }} />
			</div>
			{status.error && <p className="text-chart-error-ink text-xs">{status.error}</p>}
			{status.problem_count > 0 && <p className="text-chart-warning-ink text-xs">{t("rows.progress.problems", { count: status.problem_count })}</p>}
			{status.problems.length > 0 && <ProblemList problems={status.problems} t={t} />}
			{status.next && <p className="text-muted-foreground text-xs">{status.next}</p>}
		</div>
	);
}

export function RowPreview({ result, t }: { result: RowImportResult; t: T }) {
	const total = result.tasks_total ?? result.tasks.length;
	const extras = result.tasks.some((r) => (r.turns ?? 0) > 1 || r.attachments || (r.metadata && Object.keys(r.metadata).length > 0));
	return (
		<div className="flex min-w-0 flex-col gap-2" data-testid="suite-build-preview">
			<p className="text-sm">{t("rows.previewSummary", { ok: total, bad: result.problems.length })}</p>
			{total > result.tasks.length && <p className="text-muted-foreground text-xs">{t("rows.previewMore", { shown: result.tasks.length, total })}</p>}
			{result.tasks.length > 0 && (
				<div className="max-h-80 overflow-auto rounded-md border">
					<table className="w-full text-left text-xs">
						<thead className="bg-muted/50 text-muted-foreground sticky top-0">
							<tr>
								<th className="px-2 py-1.5 font-medium">{t("rows.col.line")}</th>
								<th className="px-2 py-1.5 font-medium">{t("rows.col.task")}</th>
								<th className="px-2 py-1.5 font-medium">{t("rows.col.question")}</th>
								<th className="px-2 py-1.5 font-medium">{result.grader === "judge" ? t("rows.col.rubric") : t("rows.col.answer")}</th>
								<th className="px-2 py-1.5 font-medium">{t("rows.col.wrong")}</th>
								{extras && <th className="px-2 py-1.5 font-medium">{t("rows.col.details")}</th>}
							</tr>
						</thead>
						<tbody className="divide-border divide-y">
							{result.tasks.map((r) => (
								<tr key={r.id} data-testid={`suite-build-preview-${r.id}`}>
									<td className="text-muted-foreground px-2 py-1 tabular-nums">{r.line}</td>
									<td className="px-2 py-1 font-mono">
										{r.id}
										{r.held_out && <span className="text-muted-foreground ml-1 font-sans">({t("private.heldOut")})</span>}
									</td>
									<td className="px-2 py-1">{r.question}</td>
									<td className="px-2 py-1">{r.check}</td>
									<td className="px-2 py-1" data-testid={`suite-build-preview-wrong-${r.id}`}>
										{(r.wrong_answers ?? []).length > 0 ? (
											<ul className="flex flex-col gap-0.5">
												{(r.wrong_answers ?? []).map((w, i) => (
													<li key={`${i}-${w}`}>{w}</li>
												))}
											</ul>
										) : null}
										{r.generated_wrong && <span className="text-muted-foreground mt-0.5 block">{t("rows.generatedWrong")}</span>}
									</td>
									{extras && (
										<td className="px-2 py-1">
											<span className="flex flex-wrap gap-1">
												{(r.turns ?? 0) > 1 && (
													<Badge variant="info" data-testid={`suite-build-turns-${r.id}`}>
														{t("rows.turns", { count: r.turns })}
													</Badge>
												)}
												{r.attachments ? <Badge variant="neutral">{t("rows.attachCount", { count: r.attachments })}</Badge> : null}
												{Object.entries(r.metadata ?? {}).map(([k, v]) => (
													<Badge key={k} variant="outline" className="font-mono font-normal">
														{k}: {v}
													</Badge>
												))}
											</span>
										</td>
									)}
								</tr>
							))}
						</tbody>
					</table>
				</div>
			)}
			{result.problems.length > 0 && <ProblemList problems={result.problems} t={t} />}
		</div>
	);
}

export function ProblemList({ problems, t }: { problems: RowImportResult["problems"]; t: T }) {
	return (
		<ul className="text-chart-warning-ink flex flex-col gap-0.5 text-xs" data-testid="suite-build-problems">
			{problems.map((p, i) => (
				<li key={`${p.line}-${i}`}>{t("rows.problem", { line: p.line, message: p.message })}</li>
			))}
		</ul>
	);
}

/** Mapping fields the server needs: empty fields and metadata left out. */
export function mappingBody(m: RowMapping | null): RowMapping | undefined {
	if (!m) return undefined;
	const out: RowMapping = {};
	for (const [k, v] of Object.entries(m)) {
		if (k === "metadata") continue;
		if (typeof v === "string" && v) (out as Record<string, string>)[k] = v;
	}
	if (m.metadata?.length) out.metadata = m.metadata;
	return Object.keys(out).length ? out : undefined;
}

export interface RowsImportFormProps {
	suiteId: string;
	/** The benchmark grades with the exact answer grader (answer match modes apply). */
	exact: boolean;
	format: "rows" | "tickets";
	/** Called once tasks were written, with how many. */
	onImported?: (count: number) => void;
	t: T;
}

/**
 * Paste or upload rows, preview them, map columns, attach files, then import (in the background
 * for large data, with live progress).
 */
export function RowsImportForm({ suiteId, exact, format, onImported, t }: RowsImportFormProps) {
	const tickets = format === "tickets";
	const dispatch = useAppDispatch();
	const [data, setData] = useState("");
	const [fileFormat, setFileFormat] = useState<"csv" | "jsonl">("csv");
	const [source, setSource] = useState("");
	const [match, setMatch] = useState<AnswerMatch>("exact");
	const [heldOut, setHeldOut] = useState(false);
	const [mapping, setMapping] = useState<RowMapping | null>(null);
	const [saveMapping, setSaveMapping] = useState(false);
	const [gradeOn, setGradeOn] = useState<GradeOn | undefined>(undefined);
	const [files, setFiles] = useState<PickedFile[]>([]);
	const [preview, setPreview] = useState<RowImportResult | null>(null);
	const [done, setDone] = useState<RowImportResult | null>(null);
	const [started, setStarted] = useState<RowImportStatus | null>(null);
	const [run, state] = useImportSuiteRowsMutation();
	const polled = useGetRowImportQuery(
		{ suite: suiteId, id: started?.id ?? "" },
		{ skip: started?.status !== "running", pollingInterval: 2000 },
	);
	useEffect(() => {
		if (polled.data && started && polled.data.id === started.id && polled.data !== started) setStarted(polled.data);
	}, [polled.data, started]);
	const job = started;
	const reported = useRef("");
	useEffect(() => {
		if (!job || job.status === "running" || reported.current === job.id) return;
		reported.current = job.id;
		dispatch(benchApi.util.invalidateTags([{ type: "BenchSuites", id: "LIST" }, { type: "BenchSuites", id: `${suiteId}/health` }]));
		if (job.written > 0) onImported?.(job.written);
	}, [job, dispatch, suiteId, onImported]);

	const bytes = byteSize(data);
	const mode = dataMode(bytes);
	const fmt: RowFormat = tickets ? "tickets" : fileFormat;
	const body = (m: RowMapping | null, g: GradeOn | undefined, f: PickedFile[]): Omit<RowImportRequest, "dry_run"> => {
		const mb = mappingBody(m);
		const att = attachmentsBody(f);
		return {
			format: fmt,
			data,
			held_out: heldOut,
			...(exact && !tickets ? { match } : {}),
			...(source ? { source } : {}),
			...(mb ? { mapping: mb } : {}),
			...(mb && saveMapping ? { save_mapping: true } : {}),
			...(g ? { grade_on: g } : {}),
			...(att ? { attachments: att } : {}),
			...(mode === "async" ? { async: true } : {}),
		};
	};
	const reset = () => {
		setPreview(null);
		setDone(null);
		setStarted(null);
		setMapping(null);
	};
	const onFile = (e: ChangeEvent<HTMLInputElement>) => {
		const file = e.target.files?.[0];
		if (!file) return;
		const f = formatOfFile(file.name);
		if (f && !tickets) setFileFormat(f);
		setSource(file.name);
		reset();
		void file.text().then(setData);
	};
	const dryRun = async (m: RowMapping | null, g: GradeOn | undefined, f: PickedFile[]) => {
		setDone(null);
		setStarted(null);
		const res = await run({ suite: suiteId, ...body(m, g, f), dry_run: true });
		if ("data" in res && res.data) {
			setPreview(res.data);
			if (!m) setMapping(res.data.mapping ?? {});
		}
	};
	const remap = (m: RowMapping) => {
		setMapping(m);
		void dryRun(m, gradeOn, files);
	};
	const regrade = (g: GradeOn) => {
		setGradeOn(g);
		void dryRun(mapping, g, files);
	};
	const reattach = (f: PickedFile[]) => {
		setFiles(f);
		if (preview) void dryRun(mapping, gradeOn, f);
	};
	const doImport = async () => {
		const res = await run({ suite: suiteId, ...body(mapping, gradeOn, files), dry_run: false });
		if (!("data" in res) || !res.data) return;
		setPreview(null);
		if (res.data.import) {
			setStarted(res.data.import);
			return;
		}
		setDone(res.data);
		setData("");
		if (res.data.tasks.length > 0) onImported?.(res.data.tasks_total ?? res.data.tasks.length);
	};
	const count = preview ? (preview.tasks_total ?? preview.tasks.length) : 0;
	const busy = state.isLoading || job?.status === "running";
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid={`suite-build-${format}`}>
			<p className="text-muted-foreground max-w-3xl text-xs">{tickets ? t("rows.ticketsHelp") : t("rows.help")}</p>
			<p className="text-muted-foreground max-w-3xl text-xs" data-testid="suite-build-verbatim">
				{t("rows.verbatim")}
			</p>
			{tickets && exact && (
				<p role="note" className="border-chart-warning/50 bg-chart-warning/10 rounded-md border px-3 py-2 text-xs" data-testid="suite-build-tickets-exact">
					{t("rows.ticketsNeedJudge")}
				</p>
			)}
			<div className="flex flex-wrap items-end gap-3">
				<Field label={t("rows.file")}>
					<input type="file" accept={tickets ? ".json,.jsonl,.txt" : ".csv,.tsv,.jsonl,.ndjson,.json"} onChange={onFile} className="text-sm" data-testid={`suite-build-${format}-file`} />
				</Field>
				{!tickets && (
					<Field label={t("rows.format")}>
						<select className={selectClass} value={fileFormat} onChange={(e) => (setFileFormat(e.target.value as "csv" | "jsonl"), reset())}>
							<option value="csv">CSV</option>
							<option value="jsonl">JSON Lines</option>
						</select>
					</Field>
				)}
				{exact && !tickets && (
					<Field label={t("rows.match")}>
						<select className={selectClass} value={match} onChange={(e) => (setMatch(e.target.value as AnswerMatch), reset())} data-testid="suite-build-rows-match">
							{(["exact", "contains_all", "numeric", "regex", "choice"] as const).map((m) => (
								<option key={m} value={m}>
									{t(`rows.matchMode.${m}`)}
								</option>
							))}
						</select>
					</Field>
				)}
			</div>
			<textarea
				className="border-input bg-background min-h-40 w-full rounded-md border p-2 font-mono text-xs"
				value={data}
				placeholder={tickets ? t("rows.ticketsPlaceholder") : fileFormat === "csv" ? t("rows.csvPlaceholder") : t("rows.jsonlPlaceholder")}
				aria-label={t("rows.paste")}
				onChange={(e) => (setData(e.target.value), reset())}
				data-testid={`suite-build-${format}-data`}
			/>
			{mode === "too_big" && (
				<p role="alert" className="text-chart-error-ink text-xs" data-testid="suite-build-too-big">
					{t("rows.tooBig")}
				</p>
			)}
			{mode === "async" && <p className="text-muted-foreground text-xs">{t("rows.background")}</p>}
			<label className="flex items-center gap-2 text-sm">
				<input type="checkbox" checked={heldOut} onChange={(e) => (setHeldOut(e.target.checked), reset())} data-testid={`suite-build-${format}-held-out`} />
				{t("rows.heldOut")}
			</label>
			{!tickets && <AttachmentsInput files={files} onChange={reattach} disabled={busy} t={t} />}
			<div className="flex flex-wrap gap-2">
				<Button variant="outline" disabled={!data.trim() || mode === "too_big" || busy} onClick={() => void dryRun(mapping, gradeOn, files)} dataTestId={`suite-build-${format}-preview`}>
					{t("rows.preview")}
				</Button>
				<Button disabled={!preview || count === 0 || busy} isLoading={state.isLoading} onClick={() => void doImport()} dataTestId={`suite-build-${format}-import`}>
					{preview ? t("rows.import", { count }) : t("rows.importIdle")}
				</Button>
			</div>
			<ErrorLine error={state.error} testId={`suite-build-${format}-error`} />
			{preview && !tickets && mapping && (
				<MappingStep
					columns={preview.columns ?? []}
					mapping={mapping}
					onChange={remap}
					saveMapping={saveMapping}
					onSaveMapping={setSaveMapping}
					showGradeOn={hasConversations(preview)}
					gradeOn={gradeOn}
					onGradeOn={regrade}
					disabled={state.isLoading}
					t={t}
				/>
			)}
			{preview && <RowPreview result={preview} t={t} />}
			{job && <ImportProgress status={job} t={t} />}
			{done && (
				<div role="status" className="border-primary/40 bg-primary/5 flex flex-col gap-1 rounded-md border px-3 py-2 text-sm" data-testid={`suite-build-${format}-done`}>
					<p className="font-medium">{t("rows.done", { count: done.tasks_total ?? done.tasks.length })}</p>
					{done.mapping_saved && <p className="text-muted-foreground text-xs">{t("rows.map.saved")}</p>}
					<p className="text-muted-foreground text-xs">{done.next}</p>
					{done.problems.length > 0 && <ProblemList problems={done.problems} t={t} />}
				</div>
			)}
		</div>
	);
}
