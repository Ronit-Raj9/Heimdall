import { pageWidthClass } from "@/components/shell/pageWidth";
import { RoleGate } from "@/components/platform/accessNotes";
import { EmptyState } from "@/components/shell/emptyState";
import { PageHeader } from "@/components/shell/pageHeader";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
	MAX_UPLOAD_BYTES,
	uploadErrorMessage,
	useUploadAgentMutation,
	useUploadProjectMutation,
	useUploadRunsMutation,
} from "@/lib/store/apis/uploadApi";
import type { UploadFilesReport, UploadKind, UploadResult, UploadRunsReport } from "@/lib/types/upload";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { ArrowRight, Bot, CircleCheck, FileArchive, FolderTree, Info, CircleMinus, ShieldCheck, Upload as UploadIcon, CircleX, type LucideIcon } from "lucide-react";
import { useRef, useState, type DragEvent } from "react";
import { useTranslation } from "react-i18next";

const MAX_MB = MAX_UPLOAD_BYTES >> 20;

const KINDS: { kind: UploadKind; icon: LucideIcon }[] = [
	{ kind: "runs", icon: ShieldCheck },
	{ kind: "project", icon: FolderTree },
	{ kind: "agent", icon: Bot },
];

function formatSize(n: number): string {
	if (n < 1024) return `${n} B`;
	if (n < 1 << 20) return `${(n / 1024).toFixed(1)} KiB`;
	return `${(n / (1 << 20)).toFixed(1)} MiB`;
}

/** Why a picked file cannot be uploaded, or null when it can. */
export function checkZipFile(file: { name: string; size: number }): "notZip" | "tooLarge" | null {
	if (!file.name.toLowerCase().endsWith(".zip")) return "notZip";
	if (file.size > MAX_UPLOAD_BYTES) return "tooLarge";
	return null;
}

/** Upload page: pick what to upload, drop a zip, see what the server kept and refused. */
export default function UploadView() {
	const { t } = useTranslation("upload");
	const [kind, setKind] = useState<UploadKind>("runs");
	const [file, setFile] = useState<File | null>(null);
	const [agentName, setAgentName] = useState("");
	const [result, setResult] = useState<UploadResult | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [uploadRuns, runsState] = useUploadRunsMutation();
	const [uploadProject, projectState] = useUploadProjectMutation();
	const [uploadAgent, agentState] = useUploadAgentMutation();
	const busy = runsState.isLoading || projectState.isLoading || agentState.isLoading;

	const pick = (f: File | null) => {
		setResult(null);
		setError(null);
		if (!f) return setFile(null);
		const bad = checkZipFile(f);
		if (bad) {
			setFile(null);
			setError(t(`drop.${bad}`, { mb: MAX_MB }));
			return;
		}
		setFile(f);
	};

	const submit = async () => {
		if (!file) return;
		setError(null);
		setResult(null);
		try {
			if (kind === "runs") {
				setResult({ kind, report: await uploadRuns(file).unwrap() });
			} else if (kind === "project") {
				setResult({ kind, report: await uploadProject(file).unwrap() });
			} else {
				setResult({ kind, report: await uploadAgent({ file, name: agentName.trim() || undefined }).unwrap() });
			}
		} catch (err) {
			// A 422 still carries the per-file report: show it.
			const data = (err as { status?: number; data?: unknown }).data;
			if ((err as { status?: number }).status === 422 && data && typeof data === "object" && "refused" in data) {
				setResult({ kind: kind as "project" | "agent", report: data as UploadFilesReport });
			} else {
				setError(uploadErrorMessage(err, t("failed")));
			}
		}
	};

	return (
		<div className={cn(pageWidthClass("wide"), "flex flex-col gap-6")} data-testid="upload-view">
			<PageHeader title={t("title")} subtitle={t("subtitle")} />
			<div className="grid gap-3 sm:grid-cols-3" role="tablist" aria-label={t("title")}>
				{KINDS.map(({ kind: k, icon: Icon }) => {
					const active = k === kind;
					return (
						<button
							key={k}
							type="button"
							role="tab"
							aria-selected={active}
							onClick={() => {
								setKind(k);
								setResult(null);
								setError(null);
							}}
							className={cn(
								"bg-card focus-visible:ring-ring/60 flex items-start gap-3 rounded-xl border p-4 text-left transition-all hover:shadow-md focus-visible:ring-2 focus-visible:outline-none",
								active ? "border-primary ring-primary/30 ring-1" : "hover:border-foreground/20",
							)}
							data-testid={`upload-tab-${k}`}
						>
							<span className={cn("flex size-9 shrink-0 items-center justify-center rounded-lg", active ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground")}>
								<Icon className="size-4.5" aria-hidden="true" />
							</span>
							<span className="flex min-w-0 flex-col gap-0.5">
								<span className="text-sm font-semibold">{t(`tabs.${k}`)}</span>
								<span className="text-muted-foreground line-clamp-2 text-xs">{t(`cards.${k}`)}</span>
							</span>
						</button>
					);
				})}
			</div>
			<p className="text-muted-foreground flex max-w-2xl items-start gap-2 text-sm">
				<Info className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
				{t(`help.${kind}`)}
			</p>
			{kind === "agent" && (
				<label className="flex max-w-sm flex-col gap-1 text-sm">
					{t("agentName")}
					<Input
						value={agentName}
						onChange={(e) => setAgentName(e.target.value)}
						placeholder={t("agentNamePlaceholder")}
						data-testid="upload-agent-name"
					/>
				</label>
			)}
			<RoleGate min="member">
			<DropZone file={file} onFile={pick} disabled={busy} kind={kind} />
			<div className="flex gap-2">
				<Button onClick={submit} disabled={!file || busy} isLoading={busy} dataTestId="upload-submit">
					{!busy && <UploadIcon className="size-4" aria-hidden="true" />}
					{busy ? t("uploading") : t("submit")}
				</Button>
				{file && (
					<Button variant="outline" onClick={() => pick(null)} disabled={busy}>
						{t("clear")}
					</Button>
				)}
			</div>
			</RoleGate>
			{error && (
				<p role="alert" className="text-destructive border-destructive/30 bg-destructive/5 flex items-center gap-2 rounded-lg border px-3 py-2 text-sm" data-testid="upload-error">
					<CircleX className="size-4 shrink-0" aria-hidden="true" />
					{error}
				</p>
			)}
			{result && <UploadResultView result={result} />}
		</div>
	);
}

/** The drag-and-drop target; a click opens the file chooser. */
export function DropZone({ file, onFile, disabled, kind = "runs" }: { file: File | null; onFile(f: File | null): void; disabled?: boolean; kind?: UploadKind }) {
	const KindIcon = KINDS.find((k) => k.kind === kind)?.icon ?? UploadIcon;
	const { t } = useTranslation("upload");
	const input = useRef<HTMLInputElement>(null);
	const [over, setOver] = useState(false);
	const onDrop = (e: DragEvent) => {
		e.preventDefault();
		setOver(false);
		if (!disabled) onFile(e.dataTransfer.files?.[0] ?? null);
	};
	return (
		<button
			type="button"
			disabled={disabled}
			onClick={() => input.current?.click()}
			onDragOver={(e) => {
				e.preventDefault();
				setOver(true);
			}}
			onDragLeave={() => setOver(false)}
			onDrop={onDrop}
			className={cn(
				"border-border/80 bg-muted/20 hover:border-primary/50 hover:bg-primary/5 flex min-h-56 flex-col items-center justify-center gap-3 rounded-xl border-2 border-dashed px-6 py-12 text-center text-sm transition-colors",
				over && "border-primary bg-primary/10",
				file && "border-solid border-emerald-500/50 bg-emerald-500/5",
				disabled && "opacity-60",
			)}
			data-testid="upload-dropzone"
		>
			<span className={cn("flex size-14 items-center justify-center rounded-full", file ? "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400" : "bg-primary/10 text-primary")}>
				{file ? <FileArchive className="size-7" aria-hidden="true" /> : <KindIcon className="size-7" aria-hidden="true" />}
			</span>
			{file ? (
				<span className="text-base font-medium">{t("drop.selected", { name: file.name, size: formatSize(file.size) })}</span>
			) : (
				<span className="flex flex-col gap-0.5">
					<span className="text-base font-medium">{t("drop.prompt")}</span>
					<span className="text-muted-foreground">{t("drop.browse")}</span>
				</span>
			)}
			<span className="text-muted-foreground text-xs">{t("drop.limit", { mb: MAX_MB })}</span>
			<input
				ref={input}
				type="file"
				accept=".zip,application/zip"
				className="hidden"
				onChange={(e) => {
					onFile(e.target.files?.[0] ?? null);
					e.target.value = "";
				}}
			/>
		</button>
	);
}

/** What the server did with the zip. */
export function UploadResultView({ result }: { result: UploadResult }) {
	return result.kind === "runs" ? <RunsResult report={result.report} /> : <FilesResult report={result.report} />;
}

function Stat({ label, value, tone }: { label: string; value: number; tone?: "ok" | "bad" }) {
	return (
		<div className="bg-card flex flex-col gap-0.5 rounded-lg border px-4 py-3">
			<span className="text-muted-foreground text-xs">{label}</span>
			<span
				className={cn(
					"text-2xl font-semibold tabular-nums",
					tone === "ok" && value > 0 && "text-emerald-600 dark:text-emerald-400",
					tone === "bad" && value > 0 && "text-destructive",
				)}
			>
				{value}
			</span>
		</div>
	);
}

const STATUS_ICON = { imported: CircleCheck, refused: CircleX, skipped: CircleMinus } as const;

function RunsResult({ report }: { report: UploadRunsReport }) {
	const { t } = useTranslation("upload");
	const exps = report.experiments ?? [];
	if (exps.length === 0) return <EmptyState compact message={t("runs.empty")} action={{ label: t("runs.openFiles"), to: "/files" }} testId="upload-runs-empty" />;
	return (
		<div className="flex flex-col gap-3" data-testid="upload-runs-result">
			<h2 className="text-base font-semibold">{t("result")}</h2>
			<div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
				<Stat label={t("stats.imported")} value={report.imported} tone="ok" />
				<Stat label={t("stats.skipped")} value={report.skipped} />
				<Stat label={t("stats.refused")} value={report.refused} tone="bad" />
				<Stat label={t("stats.runs")} value={report.runs} />
			</div>
			<p className="sr-only">{t("runs.summary", { imported: report.imported, skipped: report.skipped, refused: report.refused, runs: report.runs })}</p>
			<ul className="bg-card divide-y rounded-xl border text-sm">
				{exps.map((e) => {
					const Icon = STATUS_ICON[e.status] ?? CircleMinus;
					return (
						<li key={e.exp_id} className="flex items-start gap-3 px-4 py-3" data-testid={`upload-exp-${e.exp_id}`}>
							<Icon
								className={cn(
									"mt-0.5 size-4 shrink-0",
									e.status === "imported" && "text-emerald-600 dark:text-emerald-400",
									e.status === "refused" && "text-destructive",
									e.status === "skipped" && "text-muted-foreground",
								)}
								aria-hidden="true"
							/>
							<span className="flex min-w-0 flex-1 flex-col gap-0.5">
								<span className="flex flex-wrap items-center gap-2">
									<span className="truncate font-mono text-xs">{e.exp_id}</span>
									<span
										className={cn(
											"rounded-full px-2 py-0.5 text-xs",
											e.status === "imported" && "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
											e.status === "refused" && "bg-destructive/10 text-destructive",
											e.status === "skipped" && "bg-muted text-muted-foreground",
										)}
									>
										{t(`runs.status.${e.status}`)}
									</span>
									{e.status === "imported" && <span className="text-muted-foreground text-xs">{t("runs.runCount", { count: e.runs })}</span>}
								</span>
								{e.reason && <span className="text-muted-foreground text-xs">{e.reason}</span>}
							</span>
						</li>
					);
				})}
			</ul>
			{report.imported > 0 && (
				<Button asChild size="sm" className="self-start">
					<Link to="/experiments">
						{t("runs.openExperiments")}
						<ArrowRight className="size-4" aria-hidden="true" />
					</Link>
				</Button>
			)}
		</div>
	);
}

function FilesResult({ report }: { report: UploadFilesReport }) {
	const { t } = useTranslation("upload");
	return (
		<div className="flex flex-col gap-3 text-sm" data-testid="upload-files-result">
			<h2 className="text-base font-semibold">{t("result")}</h2>
			<div className="grid grid-cols-3 gap-3">
				<Stat label={t("stats.written")} value={report.written.length} tone="ok" />
				<Stat label={t("stats.refused")} value={report.refused.length} tone="bad" />
				<Stat label={t("stats.skipped")} value={report.skipped.length} />
			</div>
			<p className="sr-only">{t("files.summary", { written: report.written.length, refused: report.refused.length, skipped: report.skipped.length })}</p>
			{report.refused.length > 0 && (
				<section className="border-destructive/30 bg-destructive/5 flex flex-col gap-1.5 rounded-xl border p-4" data-testid="upload-files-refused">
					<h3 className="text-destructive flex items-center gap-1.5 font-medium">
						<CircleX className="size-4" aria-hidden="true" />
						{t("files.refused")}
					</h3>
					<ul className="flex flex-col gap-0.5 font-mono text-xs">
						{report.refused.map((f) => (
							<li key={`${f.area}/${f.path}`} className="break-all">
								{f.area}/{f.path}: {f.error}
							</li>
						))}
					</ul>
				</section>
			)}
			{report.written.length > 0 && (
				<section className="bg-card flex flex-col gap-1.5 rounded-xl border p-4" data-testid="upload-files-written">
					<h3 className="flex items-center gap-1.5 font-medium">
						<CircleCheck className="size-4 text-emerald-600 dark:text-emerald-400" aria-hidden="true" />
						{t("files.written")}
					</h3>
					<ul className="flex max-h-64 flex-col gap-0.5 overflow-auto font-mono text-xs">
						{report.written.map((f) => (
							<li key={`${f.area}/${f.path}`} className="break-all">
								{f.area}/{f.path}
							</li>
						))}
					</ul>
					<Button asChild size="sm" variant="outline" className="mt-1 self-start">
						<Link to="/files">
							{t("files.openFiles")}
							<ArrowRight className="size-4" aria-hidden="true" />
						</Link>
					</Button>
				</section>
			)}
			{report.skipped.length > 0 && (
				<details className="bg-card rounded-xl border px-4 py-3">
					<summary className="text-muted-foreground cursor-pointer">{t("files.skipped")}</summary>
					<ul className="mt-2 font-mono text-xs">
						{report.skipped.map((s) => (
							<li key={s}>{s}</li>
						))}
					</ul>
				</details>
			)}
		</div>
	);
}
