import { FileEditor } from "@/components/projectfs/fileEditor";
import { FILES_NS } from "@/components/projectfs/i18n";
import { SyncCard } from "@/components/projectfs/syncCard";
import { LoadError } from "@/components/runs/screenStates";
import "@/components/settings/w4m8I18n";
import { EmptyState } from "@/components/shell/emptyState";
import { useEmbedded } from "@/components/shell/embedded";
import { PageHeader } from "@/components/shell/pageHeader";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { projectExportUrl, useListProjectFilesQuery } from "@/lib/store/apis/projectFilesApi";
import { canRetry } from "@/lib/requestErrors";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { PROJECT_AREAS, type ProjectArea, type ProjectEntry } from "@/lib/types/projectfiles";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { cn } from "@/lib/utils";
import {
	Bot,
	ChevronRight,
	CornerLeftUp,
	Download,
	FilePlus,
	FileText,
	FlaskConical,
	Folder,
	FolderOpen,
	Lock,
	ShieldCheck,
	Upload,
	ListChecks,
	type LucideIcon,
} from "lucide-react";
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

const AREA_ICONS: Partial<Record<ProjectArea, LucideIcon>> = {
	agents: Bot,
	benchmarks: ListChecks,
	experiments: FlaskConical,
	policies: ShieldCheck,
	uploads: Upload,
};

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
	const { t: tm } = useTranslation("w4m8");
	const session = useSession();
	const embedded = useEmbedded();
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
		// Stays on the page it is shown in (/settings?tab=files), keeping that page's tab.
		void navigate({
			to: ".",
			search: ((prev: Record<string, unknown>) => (typeof prev.tab === "string" ? { tab: prev.tab, ...s } : s)) as never,
		});
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

	const crumbs = dir ? dir.split("/") : [];

	return (
		<div className={cn("mx-auto flex w-full max-w-[1400px] min-w-0 flex-col gap-6", !embedded && "p-4 sm:p-6")} data-testid="files-view">
			{/* The action sits beside the header, not inside it, so a narrow screen stacks them instead of squeezing the title. */}
			<div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
				<PageHeader title={t("title")} subtitle={t("subtitle")} className="mb-0 min-w-0 flex-1" />
				<Button asChild size="sm" variant="outline" className="self-start">
					<a href={projectExportUrl()} data-testid="files-export">
						<Download className="size-4" aria-hidden="true" />
						{t("export")}
					</a>
				</Button>
			</div>
			{!canWrite && (
				<p className="text-muted-foreground flex items-center gap-2 text-sm">
					<Lock className="size-3.5" aria-hidden="true" />
					{t("readOnly")}
				</p>
			)}
			<div className="grid min-w-0 gap-4 lg:grid-cols-[18rem_minmax(0,1fr)]">
				<aside className="bg-card flex min-w-0 flex-col rounded-xl border" aria-label={t("explorer")}>
					<div className="flex flex-col gap-0.5 border-b p-2" role="tablist" data-testid="files-areas">
						{PROJECT_AREAS.map((a) => {
							const Icon = AREA_ICONS[a] ?? Folder;
							const active = a === area;
							return (
								<button
									key={a}
									type="button"
									role="tab"
									aria-selected={active}
									onClick={() => go({ area: a, dir: "", path: undefined })}
									className={cn(
										"focus-visible:ring-ring/60 flex items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-sm transition-colors focus-visible:ring-2 focus-visible:outline-none",
										active ? "bg-accent text-foreground font-medium" : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
									)}
									data-testid={`files-area-${a}`}
								>
									<Icon className="size-4 shrink-0" aria-hidden="true" />
									{t(`areas.${a}`)}
								</button>
							);
						})}
					</div>
					<nav aria-label={t("explorer")} className="flex min-w-0 items-center gap-1 border-b px-3 py-2 text-xs">
						<button
							type="button"
							className="text-muted-foreground hover:text-foreground shrink-0 font-medium transition-colors"
							onClick={() => go({ dir: "", path: undefined })}
						>
							{t(`areas.${area}`)}
						</button>
						{crumbs.map((part, i) => (
							<span key={i} className="flex min-w-0 items-center gap-1">
								<ChevronRight className="text-muted-foreground size-3 shrink-0" aria-hidden="true" />
								<button
									type="button"
									className={cn(
										"truncate font-mono transition-colors",
										i === crumbs.length - 1 ? "text-foreground" : "text-muted-foreground hover:text-foreground",
									)}
									onClick={() => go({ dir: crumbs.slice(0, i + 1).join("/"), path: undefined })}
									title={crumbs.slice(0, i + 1).join("/")}
								>
									{part}
								</button>
							</span>
						))}
						{dir && (
							<span className="sr-only" data-testid="files-dir">
								/ {dir}
							</span>
						)}
					</nav>
					<div className="flex min-h-0 flex-1 flex-col p-1.5">
						{dir && (
							<button
								type="button"
								onClick={() => go({ dir: parentDir(dir), path: undefined })}
								className="text-muted-foreground hover:bg-accent/60 hover:text-foreground flex items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors"
							>
								<CornerLeftUp className="size-4" aria-hidden="true" />
								{t("up")}
							</button>
						)}
						{list.error ? (
							<LoadError
								title={tm("loadErrors.files")}
								error={list.error}
								onRetry={canRetry(list.error) ? () => void list.refetch() : undefined}
								testId="files-error"
								className="m-1"
							/>
						) : list.isLoading ? (
							<div className="flex flex-col gap-1.5 p-2">
								{[0, 1, 2, 3].map((i) => (
									<Skeleton key={i} className="h-6 w-full" />
								))}
							</div>
						) : entries.length === 0 ? (
							<EmptyState
								compact
								icon={FolderOpen}
								className="m-1.5 border-0"
								message={canWrite ? t("empty") : t("emptyViewer")}
								action={canWrite ? { label: t("emptyUpload"), to: "/upload" } : { label: t("emptyCatalog"), to: "/catalog" }}
								testId="files-empty"
							/>
						) : (
							<ul className="flex max-h-[60vh] flex-col gap-0.5 overflow-y-auto" data-testid="files-table">
								{entries.map((e) => {
									const selected = e.path === openPath;
									return (
										<li key={e.path}>
											<button
												type="button"
												className={cn(
													"group flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors",
													selected ? "bg-primary/10 text-foreground" : "hover:bg-accent/60",
												)}
												aria-current={selected ? "true" : undefined}
												onClick={() => openEntry(e)}
												title={e.mod_time ? `${baseName(e.path)} · ${new Date(e.mod_time).toLocaleString()}` : baseName(e.path)}
												data-testid={`files-entry-${e.path}`}
											>
												{e.dir ? (
													<Folder className="size-4 shrink-0 fill-amber-400/30 text-amber-500" aria-hidden="true" />
												) : (
													<FileText className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
												)}
												<span className="min-w-0 flex-1 truncate">{baseName(e.path)}</span>
												{e.dir ? (
													<ChevronRight
														className="text-muted-foreground size-3.5 shrink-0 opacity-0 transition-opacity group-hover:opacity-100"
														aria-hidden="true"
													/>
												) : (
													<span className="text-muted-foreground shrink-0 text-xs tabular-nums">{formatSize(e.size)}</span>
												)}
											</button>
										</li>
									);
								})}
							</ul>
						)}
					</div>
					{canWrite && (
						<div className="flex flex-col gap-1 border-t p-2">
							<div className="flex gap-1.5">
								<input
									aria-label={t("newFile")}
									className="bg-background focus-visible:ring-ring/60 h-8 min-w-0 flex-1 rounded-md border px-2 font-mono text-xs focus-visible:ring-2 focus-visible:outline-none"
									placeholder={t("newFilePlaceholder")}
									value={newName}
									onChange={(e) => setNewName(e.target.value)}
									onKeyDown={(e) => {
										if (e.key === "Enter") createFile();
									}}
									data-testid="files-new-name"
								/>
								<Button size="sm" variant="outline" onClick={createFile} aria-label={t("create")} title={t("create")}>
									<FilePlus className="size-4" aria-hidden="true" />
								</Button>
							</div>
							{nameError && <p className="text-destructive text-xs">{t("badName")}</p>}
						</div>
					)}
				</aside>
				<section className="flex min-w-0 flex-col gap-4">
					{openPath ? (
						<FileEditor
							key={`${area}/${openPath}`}
							area={area}
							path={openPath}
							onDeleted={() => go({ path: undefined })}
							onClose={() => go({ path: undefined })}
						/>
					) : (
						<EmptyState icon={FileText} className="bg-card min-h-80 rounded-xl" message={t("pickFile")} testId="files-pick" />
					)}
					<SyncCard />
				</section>
			</div>
		</div>
	);
}