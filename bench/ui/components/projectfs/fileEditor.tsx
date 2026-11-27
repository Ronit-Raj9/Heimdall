import { useConfirm } from "@/components/platform/confirmDialog";
import { useLeaveGuard } from "@/lib/hooks/useLeaveGuard";
import i18n from "@/lib/i18n";
import "@/components/settings/uf8aI18n";
import { Button } from "@/components/ui/button";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	projectRawUrl,
	useDeleteProjectFileMutation,
	useGetProjectFileQuery,
	useGetProjectFileVersionsQuery,
	useLazyGetProjectFileVersionQuery,
	useSaveProjectFileMutation,
} from "@/lib/store/apis/projectFilesApi";
import { errorStatus } from "@/lib/store/apis/session";
import { canUse, useSession } from "@/lib/session/sessionContext";
import type { ProjectArea, ProjectFileSaved } from "@/lib/types/projectfiles";
import { Check, ChevronRight, Download, FileText, History, Save, Trash2, X } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { FILES_NS } from "./i18n";

export interface FileEditorProps {
	area: ProjectArea;
	path: string;
	onSaved?: (saved: ProjectFileSaved) => void;
	onDeleted?: () => void;
	onClose?: () => void;
}

/**
 * A plain monospace editor over one project file: load, edit, save with If-Match (409 tells the
 * user to reload), delete, and the file's saved versions with "Load into editor" to restore one.
 * A missing file opens empty and is created on the first save. Viewers see it read-only.
 */
export function FileEditor({ area, path, onSaved, onDeleted, onClose }: FileEditorProps) {
	const { t } = useTranslation(FILES_NS);
	const confirm = useConfirm();
	const session = useSession();
	const canWrite = canUse(session, "member");
	const file = useGetProjectFileQuery({ area, path });
	const versions = useGetProjectFileVersionsQuery({ area, path });
	const [loadVersion] = useLazyGetProjectFileVersionQuery();
	const [save, saveState] = useSaveProjectFileMutation();
	const [remove, removeState] = useDeleteProjectFileMutation();
	const loadedText = file.data && file.data.encoding === "utf-8" ? file.data.content : "";
	const [text, setText] = useState(loadedText);
	const [note, setNote] = useState("");
	const [showHistory, setShowHistory] = useState(false);
	const [message, setMessage] = useState<{ kind: "ok" | "error" | "conflict"; text: string } | null>(null);

	const missing = errorStatus(file.error) === 404;
	const binary = file.data?.encoding === "base64";
	const sha = file.data?.entry.sha256;

	useEffect(() => {
		setText(loadedText);
		setMessage(null);
	}, [loadedText, file.data, area, path]);

	async function onSave() {
		setMessage(null);
		try {
			const saved = await save({ area, path, content: text, ifMatch: missing ? undefined : sha, note: note || undefined }).unwrap();
			setNote("");
			setMessage({ kind: "ok", text: t("editor.saved") });
			onSaved?.(saved);
		} catch (err) {
			if (errorStatus(err) === 409) setMessage({ kind: "conflict", text: t("editor.conflict") });
			else setMessage({ kind: "error", text: t("editor.error", { message: getBenchErrorMessage(err) }) });
		}
	}

	async function onDelete() {
		if (!(await confirm(t("editor.deleteConfirm", { path })))) return;
		try {
			await remove({ area, path, ifMatch: sha }).unwrap();
			onDeleted?.();
		} catch (err) {
			setMessage({ kind: "error", text: t("editor.error", { message: getBenchErrorMessage(err) }) });
		}
	}

	async function onRestore(id: string) {
		const res = await loadVersion({ area, path, id });
		if (res.data && res.data.encoding === "utf-8") setText(res.data.content);
	}

	const loadError = file.error && !missing ? getBenchErrorMessage(file.error) : null;

	const dirty = !binary && text !== loadedText;
	useLeaveGuard(dirty, { body: i18n.t("unsaved.body", { ns: "uf8a" }) });
	const segments = path.split("/");

	return (
		<div className="bg-card flex min-w-0 flex-col overflow-hidden rounded-xl border" data-testid="file-editor">
			<div className="flex flex-wrap items-center gap-2 border-b px-3 py-2">
				<div className="flex min-w-0 flex-1 items-center gap-1.5 text-sm" data-testid="file-editor-path" title={`${area}/${path}`}>
					<FileText className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
					<span className="text-muted-foreground shrink-0">{area}</span>
					{segments.map((seg, i) => (
						<span key={i} className="flex min-w-0 items-center gap-1.5">
							<ChevronRight className="text-muted-foreground size-3 shrink-0" aria-hidden="true" />
							<span className={i === segments.length - 1 ? "truncate font-medium" : "text-muted-foreground truncate"}>{seg}</span>
						</span>
					))}
					{dirty && (
						<span className="bg-chart-warning/10 text-chart-warning-ink ml-1 flex shrink-0 items-center gap-1 rounded-full px-2 py-0.5 text-xs">
							<span className="bg-chart-warning size-1.5 rounded-full" aria-hidden="true" />
							{t("editor.unsaved")}
						</span>
					)}
				</div>
				<div className="flex flex-wrap items-center gap-1">
					{canWrite && !binary && (
						<Button size="sm" disabled={saveState.isLoading || file.isLoading} onClick={onSave} data-testid="file-editor-save">
							<Save className="size-4" aria-hidden="true" />
							{saveState.isLoading ? t("editor.saving") : t("editor.save")}
						</Button>
					)}
					<Button
						size="sm"
						variant={showHistory ? "secondary" : "ghost"}
						onClick={() => setShowHistory((v) => !v)}
						aria-expanded={showHistory}
						data-testid="file-editor-history"
					>
						<History className="size-4" aria-hidden="true" />
						{t("editor.history")}
					</Button>
					{!missing && (
						<Button asChild size="sm" variant="ghost">
							<a href={projectRawUrl(area, path)}>
								<Download className="size-4" aria-hidden="true" />
								{t("editor.download")}
							</a>
						</Button>
					)}
					{canWrite && !binary && !missing && (
						<Button
							size="sm"
							variant="ghost"
							className="text-destructive hover:text-destructive"
							disabled={removeState.isLoading}
							onClick={onDelete}
							data-testid="file-editor-delete"
						>
							<Trash2 className="size-4" aria-hidden="true" />
							{t("editor.delete")}
						</Button>
					)}
					{onClose && (
						<Button size="sm" variant="ghost" onClick={onClose} aria-label={t("editor.close")} title={t("editor.close")}>
							<X className="size-4" aria-hidden="true" />
						</Button>
					)}
				</div>
			</div>
			{(file.isLoading || missing || loadError || message) && (
				<div className="flex flex-col gap-1 border-b px-3 py-2">
					{file.isLoading && <p className="text-muted-foreground text-sm">{t("editor.loading")}</p>}
					{missing && (
						<p className="text-muted-foreground text-sm" data-testid="file-editor-new">
							{t("editor.newFile")}
						</p>
					)}
					{loadError && <p className="text-destructive text-sm">{loadError}</p>}
					{message && (
						<div
							className={
								message.kind === "ok"
									? "flex items-center gap-1.5 text-sm text-emerald-600 dark:text-emerald-400"
									: "text-destructive flex flex-wrap items-center gap-2 text-sm"
							}
							data-testid={`file-editor-${message.kind}`}
							role="status"
						>
							{message.kind === "ok" && <Check className="size-4" aria-hidden="true" />}
							{message.text}
							{message.kind === "conflict" && (
								<Button size="sm" variant="outline" onClick={() => void file.refetch()}>
									{t("editor.reload")}
								</Button>
							)}
						</div>
					)}
				</div>
			)}
			<div className={showHistory ? "grid min-w-0 md:grid-cols-[minmax(0,1fr)_16rem]" : "min-w-0"}>
				{binary ? (
					<p className="text-muted-foreground p-6 text-sm" data-testid="file-editor-binary">
						{t("editor.binary")}
					</p>
				) : (
					<textarea
						aria-label={`${area}/${path}`}
						className="bg-background min-h-[28rem] w-full resize-y border-0 p-4 font-mono text-xs leading-5 focus-visible:outline-none"
						data-testid="file-editor-text"
						readOnly={!canWrite}
						spellCheck={false}
						value={text}
						onChange={(e) => setText(e.target.value)}
					/>
				)}
				{showHistory && (
					<div className="flex flex-col gap-2 border-t p-3 text-sm md:border-t-0 md:border-l" data-testid="file-editor-versions">
						<h3 className="text-muted-foreground text-xs font-medium tracking-wide uppercase">{t("editor.versions")}</h3>
						{(versions.data?.versions.length ?? 0) === 0 ? (
							<p className="text-muted-foreground text-xs">{t("editor.noVersions")}</p>
						) : (
							<ul className="flex max-h-[28rem] flex-col gap-1 overflow-y-auto">
								{versions.data?.versions.map((v) => (
									<li key={v.id} className="hover:bg-accent/50 flex flex-col gap-0.5 rounded-md px-2 py-1.5">
										<span className="text-xs font-medium tabular-nums">{new Date(v.at).toLocaleString()}</span>
										<span className="text-muted-foreground line-clamp-2 text-xs">
											{(v.actor_name || v.actor) && (
												<span title={v.actor || undefined}>{t("editor.by", { actor: v.actor_name || v.actor })}</span>
											)}
											{v.note && <span className="italic"> {v.note === "deleted" ? t("editor.deleted") : v.note}</span>}
										</span>
										{v.sha256 && canWrite && !binary && (
											<Button size="sm" variant="link" className="h-auto self-start p-0 text-xs" onClick={() => void onRestore(v.id)}>
												{t("editor.restore")}
											</Button>
										)}
									</li>
								))}
							</ul>
						)}
					</div>
				)}
			</div>
			{canWrite && !binary && (
				<div className="border-t px-3 py-2">
					<input
						aria-label={t("editor.note")}
						className="placeholder:text-muted-foreground h-7 w-full bg-transparent text-xs focus-visible:outline-none"
						maxLength={200}
						placeholder={t("editor.note")}
						value={note}
						onChange={(e) => setNote(e.target.value)}
					/>
				</div>
			)}
		</div>
	);
}

export default FileEditor;