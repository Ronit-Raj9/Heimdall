import { EmptyState } from "@/components/shell/emptyState";
import { PageHeader } from "@/components/shell/pageHeader";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
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
import { FileArchive, Upload } from "lucide-react";
import { useRef, useState, type DragEvent } from "react";
import { useTranslation } from "react-i18next";

const MAX_MB = MAX_UPLOAD_BYTES >> 20;

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
		<div className="flex flex-col gap-4 p-6" data-testid="upload-view">
			<PageHeader title={t("title")} subtitle={t("subtitle")} />
			<Tabs
				value={kind}
				onValueChange={(v) => {
					setKind(v as UploadKind);
					setResult(null);
					setError(null);
				}}
			>
				<TabsList>
					<TabsTrigger value="runs" data-testid="upload-tab-runs">
						{t("tabs.runs")}
					</TabsTrigger>
					<TabsTrigger value="project" data-testid="upload-tab-project">
						{t("tabs.project")}
					</TabsTrigger>
					<TabsTrigger value="agent" data-testid="upload-tab-agent">
						{t("tabs.agent")}
					</TabsTrigger>
				</TabsList>
			</Tabs>
			<p className="text-muted-foreground max-w-2xl text-sm">{t(`help.${kind}`)}</p>
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
			<DropZone file={file} onFile={pick} disabled={busy} />
			<div className="flex gap-2">
				<Button onClick={submit} disabled={!file || busy} isLoading={busy} dataTestId="upload-submit">
					{busy ? t("uploading") : t("submit")}
				</Button>
				{file && (
					<Button variant="outline" onClick={() => pick(null)} disabled={busy}>
						{t("clear")}
					</Button>
				)}
			</div>
			{error && (
				<p role="alert" className="text-destructive text-sm" data-testid="upload-error">
					{error}
				</p>
			)}
			{result && <UploadResultView result={result} />}
		</div>
	);
}

/** The drag-and-drop target; a click opens the file chooser. */
export function DropZone({ file, onFile, disabled }: { file: File | null; onFile(f: File | null): void; disabled?: boolean }) {
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
				"border-border/80 flex flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed px-6 py-10 text-center text-sm transition-colors",
				over && "border-primary bg-primary/5",
				disabled && "opacity-60",
			)}
			data-testid="upload-dropzone"
		>
			{file ? <FileArchive className="size-6" aria-hidden="true" /> : <Upload className="size-6" aria-hidden="true" />}
			<span>{file ? t("drop.selected", { name: file.name, size: formatSize(file.size) }) : t("drop.prompt")}</span>
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

function RunsResult({ report }: { report: UploadRunsReport }) {
	const { t } = useTranslation("upload");
	const exps = report.experiments ?? [];
	if (exps.length === 0) return <EmptyState compact message={t("runs.empty")} testId="upload-runs-empty" />;
	return (
		<div className="flex flex-col gap-2" data-testid="upload-runs-result">
			<p className="text-sm font-medium">{t("runs.summary", report)}</p>
			<ul className="divide-border rounded-md border text-sm">
				{exps.map((e) => (
					<li key={e.exp_id} className="flex flex-wrap items-baseline gap-2 px-3 py-2" data-testid={`upload-exp-${e.exp_id}`}>
						<span className="font-mono">{e.exp_id}</span>
						<span
							className={cn(
								"rounded px-1.5 text-xs",
								e.status === "imported" && "bg-green-500/15 text-green-700 dark:text-green-400",
								e.status === "refused" && "bg-destructive/15 text-destructive",
								e.status === "skipped" && "bg-muted text-muted-foreground",
							)}
						>
							{t(`runs.status.${e.status}`)}
						</span>
						{e.status === "imported" && <span className="text-muted-foreground text-xs">{e.runs}</span>}
						{e.reason && <span className="text-muted-foreground text-xs">{e.reason}</span>}
					</li>
				))}
			</ul>
			{report.imported > 0 && (
				<Link to="/experiments" className="text-primary text-sm underline">
					{t("runs.openExperiments")}
				</Link>
			)}
		</div>
	);
}

function FilesResult({ report }: { report: UploadFilesReport }) {
	const { t } = useTranslation("upload");
	return (
		<div className="flex flex-col gap-2 text-sm" data-testid="upload-files-result">
			<p className="font-medium">
				{t("files.summary", { written: report.written.length, refused: report.refused.length, skipped: report.skipped.length })}
			</p>
			{report.refused.length > 0 && (
				<section data-testid="upload-files-refused">
					<h3 className="text-destructive font-medium">{t("files.refused")}</h3>
					<ul className="font-mono text-xs">
						{report.refused.map((f) => (
							<li key={`${f.area}/${f.path}`}>
								{f.area}/{f.path}: {f.error}
							</li>
						))}
					</ul>
				</section>
			)}
			{report.written.length > 0 && (
				<section data-testid="upload-files-written">
					<h3 className="font-medium">{t("files.written")}</h3>
					<ul className="max-h-64 overflow-auto font-mono text-xs">
						{report.written.map((f) => (
							<li key={`${f.area}/${f.path}`}>
								{f.area}/{f.path}
							</li>
						))}
					</ul>
					<Link to="/files" className="text-primary underline">
						{t("files.openFiles")}
					</Link>
				</section>
			)}
			{report.skipped.length > 0 && (
				<details>
					<summary className="text-muted-foreground cursor-pointer">{t("files.skipped")}</summary>
					<ul className="font-mono text-xs">
						{report.skipped.map((s) => (
							<li key={s}>{s}</li>
						))}
					</ul>
				</details>
			)}
		</div>
	);
}
