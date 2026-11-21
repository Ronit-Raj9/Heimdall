import { FileEditor } from "@/components/projectfs/fileEditor";
import { FILES_NS } from "@/components/projectfs/i18n";
import { SyncCard } from "@/components/projectfs/syncCard";
import { EmptyState } from "@/components/shell/emptyState";
import { PageHeader } from "@/components/shell/pageHeader";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { projectExportUrl, useListProjectFilesQuery } from "@/lib/store/apis/projectFilesApi";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { PROJECT_AREAS, type ProjectArea, type ProjectEntry } from "@/lib/types/projectfiles";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { File, Folder, FolderOpen } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** A relative file name the server will accept: no leading slash, no "..", no empty parts. */
export function validNewName(name: string): boolean {
	const n = name.trim();
	if (!n || n.startsWith("/") || n.includes("\\")) return false;
	return n.split("/").every((part) => part.length > 0 && part !== ".." && part !== "." && part !== ".git" && part !== ".heimdall");
}

function asArea(raw: unknown): ProjectArea {
	return PROJECT_AREAS.includes(raw as ProjectArea) ? (raw as ProjectArea) : "agents";
}

function parentDir(dir: string): string {
	const i = dir.lastIndexOf("/");
	return i < 0 ? "" : dir.slice(0, i);
}

function baseName(p: string): string {
	return p.slice(p.lastIndexOf("/") + 1);
}

function formatSize(n: number): string {
	if (n < 1024) return `${n} B`;
	if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
	return `${(n / 1024 / 1024).toFixed(1)} MiB`;
}

export default function FilesView() {
	const { t } = useTranslation(FILES_NS);
	const session = useSession();
	const canWrite = canUse(session, "member");
	const search = useSearch({ strict: false }) as { area?: string; dir?: string; path?: string };
	const navigate = useNavigate();
	const area = asArea(search.area);
	const dir = search.dir ?? "";
	const openPath = search.path;
	const [newName, setNewName] = useState("");
	const [nameError, setNameError] = useState(false);

	const list = useListProjectFilesQuery({ area, dir: dir || undefined });

	const go = (next: { area?: ProjectArea; dir?: string; path?: string }) => {
		const s: Record<string, string> = { area: next.area ?? area };
		const d = next.dir ?? dir;
		if (d) s.dir = d;
		if (next.path) s.path = next.path;
		void navigate({ to: "/files", search: s });
	};

	const openEntry = (e: ProjectEntry) => (e.dir ? go({ dir: e.path }) : go({ path: e.path }));

	const createFile = () => {
		if (!validNewName(newName)) {
			setNameError(true);
			return;
		}
		setNameError(false);
		const rel = (dir ? `${dir}/` : "") + newName.trim();
		setNewName("");
		go({ path: rel });
	};

	const entries = list.data?.entries ?? [];

	return (
		<div className="flex flex-col gap-4 p-6" data-testid="files-view">
			<PageHeader
				title={t("title")}
				subtitle={t("subtitle")}
				actions={
					<Button asChild size="sm" variant="outline">
						<a href={projectExportUrl()} data-testid="files-export">
							{t("export")}
						</a>
					</Button>
				}
			/>
			<div className="flex flex-wrap gap-1" role="tablist" data-testid="files-areas">
				{PROJECT_AREAS.map((a) => (
					<Button
						key={a}
						role="tab"
						aria-selected={a === area}
						size="sm"
						variant={a === area ? "default" : "ghost"}
						onClick={() => go({ area: a, dir: "", path: undefined })}
						data-testid={`files-area-${a}`}
					>
						{t(`areas.${a}`)}
					</Button>
				))}
			</div>
			{!canWrite && <p className="text-muted-foreground text-sm">{t("readOnly")}</p>}
			<div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
				<section className="flex flex-col gap-3">
					<div className="flex items-center gap-2 text-sm">
						<FolderOpen className="size-4" aria-hidden="true" />
						<button type="button" className="hover:underline" onClick={() => go({ dir: "", path: undefined })}>
							{t(`areas.${area}`)}
						</button>
						{dir && <span className="font-mono break-all" data-testid="files-dir">/ {dir}</span>}
						{dir && (
							<Button size="sm" variant="ghost" onClick={() => go({ dir: parentDir(dir), path: undefined })}>
								{t("up")}
							</Button>
						)}
					</div>
					{list.error ? (
						<p className="text-destructive text-sm" data-testid="files-error">
							{t("loadError", { message: getBenchErrorMessage(list.error) })}
						</p>
					) : entries.length === 0 && !list.isLoading ? (
						<EmptyState compact message={canWrite ? t("empty") : t("emptyViewer")} action={canWrite ? { label: t("emptyUpload"), to: "/upload" } : { label: t("emptyCatalog"), to: "/catalog" }} testId="files-empty" />
					) : (
						<Table data-testid="files-table">
							<TableHeader>
								<TableRow>
									<TableHead>{t("columns.name")}</TableHead>
									<TableHead className="text-right">{t("columns.size")}</TableHead>
									<TableHead>{t("columns.modified")}</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{entries.map((e) => (
									<TableRow key={e.path} data-state={e.path === openPath ? "selected" : undefined}>
										<TableCell>
											<button
												type="button"
												className="flex items-center gap-2 text-left hover:underline"
												onClick={() => openEntry(e)}
												data-testid={`files-entry-${e.path}`}
											>
												{e.dir ? <Folder className="size-4" aria-hidden="true" /> : <File className="size-4" aria-hidden="true" />}
												<span className="font-mono text-xs break-all">{baseName(e.path)}</span>
											</button>
										</TableCell>
										<TableCell className="text-right text-xs">{e.dir ? "" : formatSize(e.size)}</TableCell>
										<TableCell className="text-muted-foreground text-xs">
											{e.mod_time ? new Date(e.mod_time).toLocaleString() : ""}
										</TableCell>
									</TableRow>
								))}
							</TableBody>
						</Table>
					)}
					{canWrite && (
						<div className="flex flex-col gap-1">
							<div className="flex gap-2">
								<input
									aria-label={t("newFile")}
									className="bg-background h-8 flex-1 rounded-md border px-2 font-mono text-sm"
									placeholder={t("newFilePlaceholder")}
									value={newName}
									onChange={(e) => setNewName(e.target.value)}
									onKeyDown={(e) => {
										if (e.key === "Enter") createFile();
									}}
									data-testid="files-new-name"
								/>
								<Button size="sm" variant="outline" onClick={createFile}>
									{t("create")}
								</Button>
							</div>
							{nameError && <p className="text-destructive text-xs">{t("badName")}</p>}
						</div>
					)}
				</section>
				<section className="flex flex-col gap-4">
					{!openPath && <SyncCard />}
					{openPath && (
						<FileEditor
							key={`${area}/${openPath}`}
							area={area}
							path={openPath}
							onDeleted={() => go({ path: undefined })}
							onClose={() => go({ path: undefined })}
						/>
					)}
				</section>
			</div>
		</div>
	);
}
