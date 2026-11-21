import { useConfirm } from "@/components/platform/confirmDialog";
import { JobRunButton } from "@/components/jobs/jobRunButton";
import { EmptyState } from "@/components/shell/emptyState";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import {
	authoringError,
	useCreateSuiteTaskMutation,
	useDeleteSuiteFileMutation,
	useGetSuiteFileQuery,
	useGetSuiteFilesQuery,
	useGetSuiteLintQuery,
	usePutSuiteFileMutation,
	useUploadSuiteZipMutation,
} from "@/lib/store/apis/suiteAuthoringApi";
import type { SuiteSummary } from "@/lib/types/bench";
import type { SuiteLintReport } from "@/lib/types/suiteAuthoring";
import { FileText, Lock } from "lucide-react";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

const MAX_FILE = 1 << 20;
const ID_RE = /^[a-z0-9][a-z0-9._-]{0,63}$/;

/** Edit tab: the suite's files with a text editor, add task, uploads, lint and lock. */
export function EditTab({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("suiteAuthoring");
	const writable = canUse(useSession(), "member");
	const [selected, setSelected] = useState<string | null>(null);
	if (!writable) {
		return <EmptyState icon={Lock} compact message={t("edit.readOnly")} testId="suite-edit-readonly" />;
	}
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="suite-edit-tab">
			<LintPanel suiteId={suite.id} />
			<AddTaskForm suiteId={suite.id} onCreated={(task) => setSelected(`tasks/${task}/instruction.md`)} />
			<div className="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-[18rem_1fr]">
				<FileList suiteId={suite.id} selected={selected} onSelect={setSelected} />
				<div className="flex min-w-0 flex-col gap-4">
					{selected ? (
						<FileEditor key={selected} suiteId={suite.id} path={selected} onDeleted={() => setSelected(null)} />
					) : (
						<EmptyState icon={FileText} compact message={t("edit.pick")} testId="suite-edit-pick" />
					)}
					<Uploads suiteId={suite.id} defaultDir={selected ? selected.split("/").slice(0, -1).join("/") : ""} onPicked={setSelected} />
				</div>
			</div>
		</div>
	);
}

/** Sync lint (GET /suites/{id}/lint) plus the suite.lint and suite.lock job buttons. */
function LintPanel({ suiteId }: { suiteId: string }) {
	const { t } = useTranslation("suiteAuthoring");
	const lint = useGetSuiteLintQuery(suiteId);
	return (
		<div className="flex min-w-0 flex-col gap-3 rounded-md border p-3" data-testid="suite-edit-lint">
			<div className="flex flex-wrap items-center gap-2">
				<span className="text-sm font-medium">{t("edit.lint")}</span>
				<Button size="sm" variant="outline" onClick={() => void lint.refetch()} disabled={lint.isFetching} data-testid="suite-edit-lint-now">
					{t("edit.lintNow")}
				</Button>
				<JobRunButton kind="suite.lint" params={{ suite: suiteId }} label={t("edit.lintJob")} variant="outline" />
				<JobRunButton kind="suite.lock" params={{ suite: suiteId }} label={t("edit.lock")} confirm={t("edit.lockConfirm")} variant="outline" />
			</div>
			{lint.error ? (
				<p className="text-destructive text-sm">{authoringError(lint.error, t("edit.lintUnavailable"))}</p>
			) : lint.data ? (
				<LintResult report={lint.data} />
			) : null}
		</div>
	);
}

export function LintResult({ report }: { report: SuiteLintReport }) {
	const { t } = useTranslation("suiteAuthoring");
	if (report.load_error) {
		return (
			<p role="alert" className="text-destructive text-sm" data-testid="suite-edit-lint-load-error">
				{t("edit.loadErrorLint")}: {report.load_error}
			</p>
		);
	}
	return (
		<div className="flex flex-col gap-1 text-sm" data-testid="suite-edit-lint-result">
			{report.errors === 0 ? (
				<p className="text-chart-success-ink" data-testid="suite-edit-lint-ok">
					{t("edit.lintOk", { tasks: report.tasks })} {report.warnings > 0 && t("edit.lintSummary", { errors: 0, warnings: report.warnings })}
				</p>
			) : (
				<p className="text-destructive">{t("edit.lintSummary", { errors: report.errors, warnings: report.warnings })}</p>
			)}
			<ul className="flex max-h-48 flex-col gap-0.5 overflow-auto font-mono text-xs">
				{report.problems.map((p, i) => (
					<li key={i} className={p.severity === "error" ? "text-destructive" : "text-muted-foreground"}>
						{p.severity} {p.path}: {p.message}
					</li>
				))}
			</ul>
		</div>
	);
}

function AddTaskForm({ suiteId, onCreated }: { suiteId: string; onCreated(task: string): void }) {
	const { t } = useTranslation("suiteAuthoring");
	const [createTask, { isLoading, error }] = useCreateSuiteTaskMutation();
	const [id, setId] = useState("");
	const [title, setTitle] = useState("");
	const [instruction, setInstruction] = useState("");
	const [done, setDone] = useState<string | null>(null);
	const submit = async (e: FormEvent) => {
		e.preventDefault();
		if (!ID_RE.test(id)) return;
		const res = await createTask({ suite: suiteId, id, title: title || undefined, instruction: instruction || undefined });
		if ("data" in res && res.data) {
			setDone(res.data.task);
			onCreated(res.data.task);
			setId("");
			setTitle("");
			setInstruction("");
		}
	};
	return (
		<details className="rounded-md border px-3 py-2" data-testid="suite-edit-add-task">
			<summary className="cursor-pointer text-sm font-medium">{t("edit.addTask")}</summary>
			<form className="flex flex-col gap-3 pt-3" onSubmit={(e) => void submit(e)}>
				<div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
					<label className="flex flex-col gap-1 text-sm">
						{t("edit.taskId")}
						<Input value={id} onChange={(e) => setId(e.target.value.toLowerCase())} placeholder="t01" data-testid="suite-edit-task-id" />
					</label>
					<label className="flex flex-col gap-1 text-sm">
						{t("edit.taskTitle")}
						<Input value={title} maxLength={120} onChange={(e) => setTitle(e.target.value)} />
					</label>
				</div>
				<label className="flex flex-col gap-1 text-sm">
					{t("edit.taskInstruction")}
					<textarea
						className="bg-card min-h-24 rounded-md border px-3 py-2 font-mono text-sm"
						value={instruction}
						onChange={(e) => setInstruction(e.target.value)}
					/>
				</label>
				{error && <p className="text-destructive text-sm">{authoringError(error, t("edit.taskError"))}</p>}
				{done && <p className="text-sm">{t("edit.taskCreated", { task: done })}</p>}
				<div>
					<Button type="submit" size="sm" disabled={!ID_RE.test(id) || isLoading} data-testid="suite-edit-create-task">
						{t("edit.createTask")}
					</Button>
				</div>
			</form>
		</details>
	);
}

function FileList({ suiteId, selected, onSelect }: { suiteId: string; selected: string | null; onSelect(p: string): void }) {
	const { t } = useTranslation("suiteAuthoring");
	const files = useGetSuiteFilesQuery(suiteId);
	const [newPath, setNewPath] = useState("");
	const list = useMemo(() => (files.data?.files ?? []).filter((f) => !f.dir), [files.data]);
	return (
		<div className="flex min-w-0 flex-col gap-2 rounded-md border p-2" data-testid="suite-edit-files">
			<span className="px-1 text-sm font-medium">{t("edit.files")}</span>
			{files.error && <p className="text-destructive px-1 text-xs">{authoringError(files.error, t("edit.loadError"))}</p>}
			{list.length === 0 && !files.isLoading && !files.error && <p className="text-muted-foreground px-1 text-xs">{t("edit.noFiles")}</p>}
			<ul className="flex max-h-[28rem] flex-col overflow-auto font-mono text-xs">
				{list.map((f) => (
					<li key={f.path}>
						<button
							type="button"
							onClick={() => onSelect(f.path)}
							className={`w-full truncate rounded-sm px-1 py-0.5 text-left hover:bg-muted ${selected === f.path ? "bg-muted font-semibold" : ""}`}
							title={f.path}
						>
							{f.path}
						</button>
					</li>
				))}
			</ul>
			<form
				className="flex flex-col gap-1 border-t pt-2"
				onSubmit={(e) => {
					e.preventDefault();
					const p = newPath.trim().replace(/^\/+/, "");
					if (p) {
						onSelect(p);
						setNewPath("");
					}
				}}
			>
				<Input value={newPath} onChange={(e) => setNewPath(e.target.value)} placeholder={t("edit.newFileHint")} aria-label={t("edit.newFile")} />
				<Button type="submit" size="sm" variant="outline" disabled={!newPath.trim()}>
					{t("edit.addFile")}
				</Button>
			</form>
		</div>
	);
}

function FileEditor({ suiteId, path, onDeleted }: { suiteId: string; path: string; onDeleted(): void }) {
	const { t } = useTranslation("suiteAuthoring");
	const confirm = useConfirm();
	const file = useGetSuiteFileQuery({ suite: suiteId, path });
	const [put, putState] = usePutSuiteFileMutation();
	const [del] = useDeleteSuiteFileMutation();
	const [text, setText] = useState<string | null>(null);
	const [saved, setSaved] = useState(false);
	const isNew = (file.error as { status?: number } | undefined)?.status === 404;
	useEffect(() => {
		if (file.data && !file.data.binary) setText(file.data.content ?? "");
		if (isNew) setText((cur) => cur ?? "");
	}, [file.data, isNew]);
	const save = async () => {
		if (text === null) return;
		setSaved(false);
		const res = await put({ suite: suiteId, path, content: text, ifMatch: file.data?.sha256 });
		if (!("error" in res)) setSaved(true);
	};
	const remove = async () => {
		if (!(await confirm(t("edit.confirmDelete", { path })))) return;
		const res = await del({ suite: suiteId, path });
		if (!("error" in res)) onDeleted();
	};
	return (
		<div className="flex min-w-0 flex-col gap-2 rounded-md border p-3" data-testid="suite-edit-editor">
			<div className="flex flex-wrap items-center gap-2">
				<span className="min-w-0 flex-1 truncate font-mono text-sm">{path}</span>
				{saved && <span className="text-muted-foreground text-xs">{t("edit.saved")}</span>}
				<Button size="sm" onClick={() => void save()} disabled={text === null || putState.isLoading} data-testid="suite-edit-save">
					{putState.isLoading ? t("edit.saving") : t("edit.save")}
				</Button>
				{!isNew && path !== "suite.yaml" && (
					<Button size="sm" variant="outline" onClick={() => void remove()}>
						{t("edit.delete")}
					</Button>
				)}
			</div>
			{file.error && !isNew && <p className="text-destructive text-sm">{authoringError(file.error, t("edit.loadError"))}</p>}
			{file.data?.binary ? (
				<p className="text-muted-foreground text-sm">{t("edit.binary")}</p>
			) : (
				<textarea
					className="bg-card min-h-80 w-full rounded-md border px-3 py-2 font-mono text-xs"
					spellCheck={false}
					value={text ?? ""}
					disabled={text === null}
					onChange={(e) => {
						setText(e.target.value);
						setSaved(false);
					}}
					aria-label={path}
					data-testid="suite-edit-textarea"
				/>
			)}
			{putState.error && <p className="text-destructive text-sm">{authoringError(putState.error, t("edit.saveError"))}</p>}
		</div>
	);
}

function Uploads({ suiteId, defaultDir, onPicked }: { suiteId: string; defaultDir: string; onPicked(p: string): void }) {
	const { t } = useTranslation("suiteAuthoring");
	const [dir, setDir] = useState(defaultDir);
	const [put, putState] = usePutSuiteFileMutation();
	const [upload, uploadState] = useUploadSuiteZipMutation();
	const [msg, setMsg] = useState<string | null>(null);
	useEffect(() => setDir(defaultDir), [defaultDir]);
	const target = dir.trim().replace(/^\/+|\/+$/g, "");
	const onFile = async (f: File | undefined) => {
		if (!f) return;
		setMsg(null);
		if (f.size > MAX_FILE) {
			setMsg(t("edit.tooLarge"));
			return;
		}
		const p = target ? `${target}/${f.name}` : f.name;
		const res = await put({ suite: suiteId, path: p, content: f });
		if (!("error" in res)) onPicked(p);
	};
	const onZip = async (f: File | undefined) => {
		if (!f || !target) return;
		setMsg(null);
		const res = await upload({ suite: suiteId, dir: target, file: f });
		if ("data" in res && res.data) setMsg(t("edit.uploaded", { count: res.data.files.length }));
	};
	const busy = putState.isLoading || uploadState.isLoading;
	const err = putState.error ?? uploadState.error;
	return (
		<div className="flex min-w-0 flex-col gap-2 rounded-md border p-3" data-testid="suite-edit-uploads">
			<label className="flex flex-col gap-1 text-sm">
				{t("edit.uploadDir")}
				<Input value={dir} onChange={(e) => setDir(e.target.value)} placeholder="tasks/t01/repo" data-testid="suite-edit-upload-dir" />
			</label>
			<div className="flex flex-wrap gap-3 text-sm">
				<label className="flex flex-col gap-1">
					{t("edit.uploadFile")}
					<input type="file" disabled={busy} onChange={(e) => void onFile(e.target.files?.[0])} data-testid="suite-edit-upload-file" />
				</label>
				<label className="flex flex-col gap-1">
					{t("edit.uploadZip")}
					<input type="file" accept=".zip,application/zip" disabled={busy || !target} onChange={(e) => void onZip(e.target.files?.[0])} data-testid="suite-edit-upload-zip" />
				</label>
			</div>
			{busy && <p className="text-muted-foreground text-xs">{t("edit.uploading")}</p>}
			{msg && <p className="text-sm">{msg}</p>}
			{err && <p className="text-destructive text-sm">{authoringError(err, t("edit.uploadError"))}</p>}
		</div>
	);
}

export default EditTab;
