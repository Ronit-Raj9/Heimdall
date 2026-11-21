import { useConfirm } from "@/components/platform/confirmDialog";
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

	return (
		<div className="flex flex-col gap-3" data-testid="file-editor">
			<div className="flex flex-wrap items-center justify-between gap-2">
				<code className="text-sm font-medium break-all" data-testid="file-editor-path">
					{area}/{path}
				</code>
				<div className="flex gap-2">
					{!missing && (
						<Button asChild size="sm" variant="ghost">
							<a href={projectRawUrl(area, path)}>{t("editor.download")}</a>
						</Button>
					)}
					{onClose && (
						<Button size="sm" variant="ghost" onClick={onClose}>
							{t("editor.close")}
						</Button>
					)}
				</div>
			</div>
			{file.isLoading && <p className="text-muted-foreground text-sm">{t("editor.loading")}</p>}
			{missing && <p className="text-muted-foreground text-sm" data-testid="file-editor-new">{t("editor.newFile")}</p>}
			{loadError && <p className="text-destructive text-sm">{loadError}</p>}
			{binary ? (
				<p className="text-muted-foreground text-sm" data-testid="file-editor-binary">{t("editor.binary")}</p>
			) : (
				<textarea
					aria-label={`${area}/${path}`}
					className="bg-background min-h-80 w-full rounded-md border p-3 font-mono text-xs leading-5"
					data-testid="file-editor-text"
					readOnly={!canWrite}
					spellCheck={false}
					value={text}
					onChange={(e) => setText(e.target.value)}
				/>
			)}
			{canWrite && !binary && (
				<div className="flex flex-wrap items-center gap-2">
					<input
						aria-label={t("editor.note")}
						className="bg-background h-8 min-w-48 flex-1 rounded-md border px-2 text-sm"
						maxLength={200}
						placeholder={t("editor.note")}
						value={note}
						onChange={(e) => setNote(e.target.value)}
					/>
					<Button size="sm" disabled={saveState.isLoading || file.isLoading} onClick={onSave} data-testid="file-editor-save">
						{saveState.isLoading ? t("editor.saving") : t("editor.save")}
					</Button>
					{!missing && (
						<Button size="sm" variant="destructive" disabled={removeState.isLoading} onClick={onDelete}>
							{t("editor.delete")}
						</Button>
					)}
				</div>
			)}
			{message && (
				<div
					className={message.kind === "ok" ? "text-sm text-emerald-600" : "text-destructive flex items-center gap-2 text-sm"}
					data-testid={`file-editor-${message.kind}`}
					role="status"
				>
					{message.text}
					{message.kind === "conflict" && (
						<Button size="sm" variant="outline" onClick={() => void file.refetch()}>
							{t("editor.reload")}
						</Button>
					)}
				</div>
			)}
			<details className="text-sm" data-testid="file-editor-versions">
				<summary className="cursor-pointer font-medium">{t("editor.versions")}</summary>
				{(versions.data?.versions.length ?? 0) === 0 ? (
					<p className="text-muted-foreground mt-2">{t("editor.noVersions")}</p>
				) : (
					<ul className="mt-2 flex flex-col gap-1">
						{versions.data?.versions.map((v) => (
							<li key={v.id} className="flex flex-wrap items-center gap-2">
								<span className="font-mono text-xs">{new Date(v.at).toLocaleString()}</span>
								{v.actor && <span className="text-muted-foreground">{t("editor.by", { actor: v.actor })}</span>}
								{v.note && <span className="italic">{v.note === "deleted" ? t("editor.deleted") : v.note}</span>}
								{v.sha256 && canWrite && !binary && (
									<Button size="sm" variant="link" onClick={() => void onRestore(v.id)}>
										{t("editor.restore")}
									</Button>
								)}
							</li>
						))}
					</ul>
				)}
			</details>
		</div>
	);
}

export default FileEditor;
