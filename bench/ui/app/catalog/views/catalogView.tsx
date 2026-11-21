import { JobLogPanel } from "@/components/jobs/jobLogPanel";
import { EmptyState } from "@/components/shell/emptyState";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { FilterSelect, FilterTokens, SegmentedControl, ToolbarSearch, type FilterToken } from "@/components/ui/toolbar";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useAddCatalogEntryMutation, useGetBenchCatalogQuery, useUploadCatalogDatasetMutation } from "@/lib/store/apis/catalogApi";
import type { CatalogAddRequest, CatalogAddResponse, CatalogEntry } from "@/lib/types/catalog";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { CheckCircle2, Info, Library, X } from "lucide-react";
import { type ReactNode, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	CATEGORIES,
	CATEGORY_ICON,
	CATEGORY_TINT,
	type CatalogFilters,
	type Category,
	cardDescription,
	cleanTitle,
	entryCategories,
	filterEntries,
	installedHref,
	NO_FILTERS,
	SECTIONS,
	type SectionId,
	type SourceFilter,
	sectionOf,
} from "./catalogModel";

export { cleanTitle, entryCategories, filterEntries, installedHref, sectionOf } from "./catalogModel";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		catalog: typeof import("@/locales/en/catalog.json");
	}
}

/** Where the import's data comes from. */
export type DataMode = "upload" | "url" | "path";

export interface ImportForm {
	mode: DataMode;
	from: string;
	url: string;
	file?: File | null;
	/** Upstream commit for repo zips without git history (set ref=...). */
	ref: string;
	suite: string;
	limit: number;
}

/** The form an importer entry opens with: upload by default, the known URL prefilled. */
export function initialImportForm(e: CatalogEntry): ImportForm {
	return { mode: "upload", from: e.dataset_path ?? "", url: e.fetch_url ?? "", file: null, ref: "", suite: e.suite_id ?? "", limit: 5 };
}

/** The start button is enabled once the chosen data option has its input. */
export function importReady(f: ImportForm): boolean {
	if (f.mode === "upload") return !!f.file;
	if (f.mode === "url") return /^https:\/\/\S+$/.test(f.url.trim());
	return !!f.from.trim();
}

const MODES: DataMode[] = ["upload", "url", "path"];
const MODE_KEY = { upload: "modeUpload", url: "modeUrl", path: "modePath" } as const;
const SOURCES: SourceFilter[] = ["all", "ready", "needs"];
const SOURCE_KEY = { all: "sourceAll", ready: "sourceReady", needs: "sourceNeedsData" } as const;
const SECTION_KEY = {
	featured: ["sectionFeatured", "sectionFeaturedHint"],
	try: ["sectionTry", "sectionTryHint"],
	import: ["sectionImport", "sectionImportHint"],
} as const;

export interface CatalogBodyProps {
	entries: CatalogEntry[];
	sourceAvailable: boolean;
	starterAgents: string[];
	filters: CatalogFilters;
	onFilters(f: CatalogFilters): void;
	onAdd(e: CatalogEntry): void;
	busyId?: string | null;
	/** Entry whose details panel is open (no import form). */
	detailsFor?: CatalogEntry | null;
	onDetails?(e: CatalogEntry | null): void;
	importFor?: CatalogEntry | null;
	importForm?: ImportForm;
	onImportForm?(f: ImportForm): void;
	onStartImport?(): void;
	onCancelImport?(): void;
	lastAdded?: CatalogAddResponse | null;
	error?: string | null;
	jobPanel?: ReactNode;
}

function Pill({ children, className, testId }: { children: ReactNode; className?: string; testId?: string }) {
	return (
		<span className={cn("inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap", className)} data-testid={testId}>
			{children}
		</span>
	);
}

function CategoryTile({ category, size = "md" }: { category: Category; size?: "md" | "lg" }) {
	const Icon = CATEGORY_ICON[category];
	return (
		<span className={cn("flex shrink-0 items-center justify-center rounded-xl", CATEGORY_TINT[category], size === "lg" ? "size-12" : "size-10")}>
			<Icon className={size === "lg" ? "size-6" : "size-5"} aria-hidden />
		</span>
	);
}

/** Upload and fetch options for an importer, shown in the side panel. */
function DataOptions({ e }: { e: CatalogEntry }) {
	const { t } = useTranslation("catalog");
	if (e.kind !== "importer" || !e.upload_expects) return null;
	return (
		<div className="bg-muted/40 flex flex-col gap-1 rounded-lg p-3 text-xs" data-testid={`catalog-data-${e.id}`}>
			<div>
				<span className="font-medium">{t("cardUpload")}</span> {e.upload_expects}
			</div>
			<div>
				<span className="font-medium">{t("cardFetch")}</span> {e.fetch_allowed ? (e.fetch_url ? t("cardFetchKnown") : t("cardFetchAny")) : t("cardFetchNo")}
			</div>
			{e.data_note && <div className="text-muted-foreground">{e.data_note}</div>}
		</div>
	);
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
	return (
		<div className="flex flex-col gap-0.5">
			<dt className="text-muted-foreground text-xs">{label}</dt>
			<dd className="text-sm break-words">{children}</dd>
		</div>
	);
}

/** Facts that do not belong on the card. */
function EntryFacts({ e }: { e: CatalogEntry }) {
	const { t } = useTranslation("catalog");
	const mono = (xs: string[]) => <span className="font-mono text-xs">{xs.join(", ")}</span>;
	return (
		<dl className="flex flex-col gap-3">
			{e.suite_id && e.kind !== "example" && <Fact label={t("suiteId")}>{mono([e.suite_id])}</Fact>}
			{(e.suites?.length ?? 0) > 0 && <Fact label={t("suites")}>{mono(e.suites!)}</Fact>}
			{(e.agents?.length ?? 0) > 0 && <Fact label={t("agents")}>{mono(e.agents!)}</Fact>}
			{(e.benchmarks?.length ?? 0) > 0 && <Fact label={t("benchmarks")}>{mono(e.benchmarks!)}</Fact>}
			{e.grading && <Fact label={t("grading")}>{e.grading}</Fact>}
			{e.license && <Fact label={t("license")}>{e.license}</Fact>}
			{(e.needs?.length ?? 0) > 0 && <Fact label={t("needs")}>{e.needs!.join(", ")}</Fact>}
			{e.source && (
				<Fact label={t("source")}>
					<code className="text-xs break-all">{e.source}</code>
				</Fact>
			)}
		</dl>
	);
}

/** Right-hand drawer: entry details, and the import form for importers. */
function SidePanel({ e, onClose, children, testId }: { e: CatalogEntry; onClose(): void; children: ReactNode; testId: string }) {
	const { t } = useTranslation("catalog");
	const { name, tagline, example } = cleanTitle(e);
	const cat = entryCategories(e)[0];
	return (
		<div className="fixed inset-0 z-50 flex justify-end" role="dialog" aria-modal="true" aria-label={name}>
			<button type="button" aria-label={t("close")} className="absolute inset-0 bg-black/30 backdrop-blur-[1px]" onClick={onClose} />
			<aside className="bg-background border-border relative flex h-full w-full max-w-md flex-col gap-5 overflow-y-auto border-l p-6 shadow-xl" data-testid={testId}>
				<div className="flex items-start gap-3">
					<CategoryTile category={cat} size="lg" />
					<div className="min-w-0 flex-1">
						<div className="flex items-center gap-2">
							<h2 className="truncate text-lg font-semibold tracking-tight">{name}</h2>
							{example && <Pill className="bg-secondary text-secondary-foreground">{t("pillExample")}</Pill>}
						</div>
						<p className="text-muted-foreground text-sm">{tagline ?? t(`cat_${cat}`)}</p>
					</div>
					<Button size="icon" variant="ghost" onClick={onClose} aria-label={t("close")}>
						<X className="size-4" />
					</Button>
				</div>
				{e.summary && <p className="text-sm leading-relaxed">{e.summary}</p>}
				{children}
			</aside>
		</div>
	);
}

function ImportPanel(props: CatalogBodyProps & { importFor: CatalogEntry; importForm: ImportForm }) {
	const { t } = useTranslation("catalog");
	const e = props.importFor;
	const f = props.importForm;
	const set = (p: Partial<ImportForm>) => props.onImportForm?.({ ...f, ...p });
	return (
		<SidePanel e={e} onClose={() => props.onCancelImport?.()} testId="catalog-import-form">
			<DataOptions e={e} />
			<div className="bg-muted inline-flex w-fit flex-wrap gap-1 rounded-lg p-1" role="tablist">
				{MODES.filter((m) => m !== "url" || e.fetch_allowed).map((m) => (
					<button
						key={m}
						type="button"
						role="tab"
						aria-selected={f.mode === m}
						className={cn("rounded-md px-3 py-1 text-sm transition-colors", f.mode === m ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground")}
						onClick={() => set({ mode: m })}
						data-testid={`catalog-mode-${m}`}
					>
						{t(MODE_KEY[m])}
					</button>
				))}
			</div>
			{f.mode === "upload" && (
				<label className="flex flex-col gap-1.5 text-sm font-medium">
					{t("uploadLabel")}
					<Input type="file" accept=".zip,.jsonl,.json,.parquet,.csv,.tsv,.txt" onChange={(ev) => set({ file: ev.target.files?.[0] ?? null })} data-testid="catalog-import-file" />
					<span className="text-muted-foreground text-xs font-normal">{t("uploadHelp", { expects: e.upload_expects ?? "", path: e.dataset_path ?? "uploads/datasets" })}</span>
				</label>
			)}
			{f.mode === "url" && (
				<label className="flex flex-col gap-1.5 text-sm font-medium">
					{t("urlLabel")}
					<Input value={f.url} placeholder="https://" onChange={(ev) => set({ url: ev.target.value })} data-testid="catalog-import-url" />
					<span className="text-muted-foreground text-xs font-normal">{t("urlHelp")}</span>
				</label>
			)}
			{f.mode === "path" && (
				<label className="flex flex-col gap-1.5 text-sm font-medium">
					{t("fromLabel")}
					<Input value={f.from} onChange={(ev) => set({ from: ev.target.value })} data-testid="catalog-import-from" />
					<span className="text-muted-foreground text-xs font-normal">{t("fromHelp")}</span>
				</label>
			)}
			{e.data_note && (
				<label className="flex flex-col gap-1.5 text-sm font-medium">
					{t("refLabel")}
					<Input value={f.ref} placeholder="40-hex commit" onChange={(ev) => set({ ref: ev.target.value })} data-testid="catalog-import-ref" />
				</label>
			)}
			<div className="flex gap-3">
				<label className="flex flex-1 flex-col gap-1.5 text-sm font-medium">
					{t("suiteLabel")}
					<Input value={f.suite} onChange={(ev) => set({ suite: ev.target.value })} data-testid="catalog-import-suite" />
				</label>
				<label className="flex w-32 flex-col gap-1.5 text-sm font-medium">
					{t("limitLabel")}
					<Input type="number" min={1} max={500} value={f.limit} onChange={(ev) => set({ limit: Number(ev.target.value) || 1 })} data-testid="catalog-import-limit" />
				</label>
			</div>
			{props.error && (
				<div className="text-chart-error-ink text-sm" data-testid="catalog-import-error">
					{t("addFailed", { message: props.error })}
				</div>
			)}
			<div className="mt-auto flex justify-end gap-2 pt-2">
				<Button variant="ghost" onClick={props.onCancelImport}>
					{t("cancel")}
				</Button>
				<Button disabled={!importReady(f) || props.busyId === e.id} onClick={props.onStartImport} data-testid="catalog-import-start">
					{props.busyId === e.id ? t("adding") : t("startImport")}
				</Button>
			</div>
		</SidePanel>
	);
}

function CardAction({ e, busy, canWrite, onAdd }: { e: CatalogEntry; busy: boolean; canWrite: boolean; onAdd(): void }) {
	const { t } = useTranslation("catalog");
	if (e.installed)
		return (
			<Button size="sm" variant="outline" asChild>
				<a href={installedHref(e)} data-testid={`catalog-open-${e.id}`}>
					{t("open")}
				</a>
			</Button>
		);
	if (!e.available)
		return (
			<span className="text-muted-foreground text-xs" title={e.hint} data-testid={`catalog-unavailable-${e.id}`}>
				{t("unavailable")}
			</span>
		);
	if (!canWrite) return <span className="text-muted-foreground text-xs">{t("memberOnly")}</span>;
	return (
		<Button size="sm" variant={e.kind === "importer" ? "outline" : "default"} disabled={busy} onClick={onAdd} data-testid={`catalog-add-${e.id}`} title={e.hint}>
			{busy ? t("adding") : e.kind === "importer" ? t("import") : t("add")}
		</Button>
	);
}

function EntryCard({ e, props, canWrite }: { e: CatalogEntry; props: CatalogBodyProps; canWrite: boolean }) {
	const { t } = useTranslation("catalog");
	const { name, example } = cleanTitle(e);
	const cat = entryCategories(e)[0];
	return (
		<div
			className="border-border bg-card group flex flex-col gap-3 rounded-xl border p-5 transition-all hover:-translate-y-0.5 hover:shadow-md"
			data-testid={`catalog-entry-${e.id}`}
		>
			<div className="flex items-start gap-3">
				<CategoryTile category={cat} />
				<div className="min-w-0 flex-1">
					<div className="flex items-center gap-2">
						<h3 className="truncate text-base font-semibold" title={e.title}>
							{name}
						</h3>
						{example && <Pill className="bg-secondary text-secondary-foreground shrink-0">{t("pillExample")}</Pill>}
					</div>
					<p className="text-muted-foreground mt-1 line-clamp-2 text-sm">{cardDescription(e)}</p>
				</div>
			</div>
			<div className="mt-auto flex items-center justify-between gap-2 pt-1">
				<div className="flex min-w-0 flex-wrap items-center gap-1.5">
					<Pill className="bg-muted text-muted-foreground">{t(`cat_${cat}`)}</Pill>
					{e.installed ? (
						<Pill className="bg-chart-success/10 text-chart-success-ink" testId={`catalog-installed-${e.id}`}>
							<CheckCircle2 className="size-3" aria-hidden />
							{t("installed")}
						</Pill>
					) : (
						e.kind === "example" && <Pill className="bg-chart-success/10 text-chart-success-ink">{t("pillFree")}</Pill>
					)}
				</div>
				<div className="flex shrink-0 items-center gap-1">
					<Button size="icon" variant="ghost" className="size-8" onClick={() => props.onDetails?.(e)} aria-label={t("details")} title={t("details")} data-testid={`catalog-details-${e.id}`}>
						<Info className="size-4" />
					</Button>
					<CardAction e={e} busy={props.busyId === e.id} canWrite={canWrite} onAdd={() => props.onAdd(e)} />
				</div>
			</div>
		</div>
	);
}

/** The catalog screen without data fetching (tested directly). */
export function CatalogBody(props: CatalogBodyProps) {
	const { t } = useTranslation("catalog");
	const canWrite = canUse(useSession(), "member");
	const { filters } = props;
	const shown = filterEntries(props.entries, filters);
	const present = useMemo(() => {
		const s = new Set<Category>();
		for (const e of props.entries) for (const c of entryCategories(e)) s.add(c);
		return CATEGORIES.filter((c) => s.has(c));
	}, [props.entries]);
	const set = (patch: Partial<CatalogFilters>) => props.onFilters({ ...filters, ...patch });
	const tokens: FilterToken[] = [
		filters.category !== "all" && { key: "category", label: `${t("category")}: ${t(`cat_${filters.category}`)}`, onRemove: () => set({ category: "all" }) },
		filters.source !== "all" && { key: "source", label: t(SOURCE_KEY[filters.source]), onRemove: () => set({ source: "all" }) },
	].filter((x): x is FilterToken => Boolean(x));
	const res = props.lastAdded?.result;
	const grouped = SECTIONS.map((id) => ({ id, items: shown.filter((e) => sectionOf(e) === id) })).filter((g) => g.items.length > 0);
	const details = props.detailsFor && !props.importFor ? props.detailsFor : null;
	return (
		<div className="mx-auto flex w-full max-w-[1200px] flex-col gap-8 p-4 sm:p-6" data-testid="catalog-view">
			<header className="flex flex-col gap-5">
				<div>
					<h1 className="text-2xl font-semibold tracking-tight sm:text-3xl">{t("title")}</h1>
					<p className="text-muted-foreground mt-1 text-sm">{t("subtitle")}</p>
				</div>
				<div className="flex flex-col gap-2">
					<div className="flex flex-wrap items-center gap-2" role="toolbar" data-testid="catalog-toolbar">
						<ToolbarSearch value={filters.query} onChange={(v) => set({ query: v })} placeholder={t("search")} label={t("search")} testId="catalog-search" />
						<FilterSelect
							label={t("category")}
							allLabel={t("filterAll")}
							options={present.map((c) => ({ value: c, label: t(`cat_${c}`) }))}
							value={filters.category === "all" ? null : filters.category}
							onChange={(v) => set({ category: (v ?? "all") as Category | "all" })}
							testId="catalog-filter-category"
						/>
						<SegmentedControl
							label={t("sourceLabel")}
							options={SOURCES.map((s) => ({ value: s, label: t(SOURCE_KEY[s]) }))}
							value={filters.source}
							onChange={(v) => set({ source: v })}
							testId="catalog-source"
						/>
					</div>
					<FilterTokens tokens={tokens} onClear={() => set({ category: "all", source: "all" })} testId="catalog-tokens" />
				</div>
				{props.starterAgents.length > 0 && (
					<p className="text-muted-foreground text-xs" data-testid="catalog-starter">
						{t("starter", { agents: props.starterAgents.join(", ") })}
					</p>
				)}
			</header>
			{!props.sourceAvailable && (
				<div className="border-chart-warning/60 bg-chart-warning/5 rounded-xl border p-4 text-sm" data-testid="catalog-no-source">
					{t("noSource")}{" "}
					<a className="font-medium underline" href="/upload">
						{t("noSourceAction")}
					</a>
				</div>
			)}
			{props.error && !props.importFor && (
				<div className="border-chart-error/50 bg-chart-error/5 text-chart-error-ink rounded-xl border p-4 text-sm" data-testid="catalog-error">
					{t("addFailed", { message: props.error })}
				</div>
			)}
			{props.lastAdded && (
				<div className="border-chart-success/50 bg-chart-success/5 flex items-center gap-2 rounded-xl border p-4 text-sm" data-testid="catalog-added">
					<CheckCircle2 className="text-chart-success-ink size-4 shrink-0" aria-hidden />
					{res ? (
						<span>
							{t("resultWritten", { count: res.written?.length ?? 0 })}
							{(res.skipped?.length ?? 0) > 0 && <>; {t("resultSkipped", { count: res.skipped?.length ?? 0 })}</>}.{" "}
							<Link to="/suites" className="font-medium underline">
								{t("openSuites")}
							</Link>
						</span>
					) : (
						<span>{t("importStarted")}</span>
					)}
				</div>
			)}
			{props.jobPanel}
			{shown.length === 0 ? (
				<EmptyState icon={Library} message={t("empty")} testId="catalog-empty" action={{ label: t("clearFilters"), onClick: () => props.onFilters(NO_FILTERS) }} />
			) : (
				grouped.map((g) => (
					<section key={g.id} className="flex flex-col gap-4" data-testid={`catalog-section-${g.id}`}>
						<div>
							<h2 className="text-lg font-semibold tracking-tight">{t(SECTION_KEY[g.id as SectionId][0])}</h2>
							<p className="text-muted-foreground text-sm">{t(SECTION_KEY[g.id as SectionId][1])}</p>
						</div>
						<div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
							{g.items.map((e) => (
								<EntryCard key={e.id} e={e} props={props} canWrite={canWrite} />
							))}
						</div>
					</section>
				))
			)}
			{props.importFor && props.importForm && <ImportPanel {...props} importFor={props.importFor} importForm={props.importForm} />}
			{details && (
				<SidePanel e={details} onClose={() => props.onDetails?.(null)} testId="catalog-details-panel">
					<DataOptions e={details} />
					<EntryFacts e={details} />
					{!details.available && (
						<div className="bg-muted/40 rounded-lg p-3 text-sm">
							{details.hint && <p className="text-muted-foreground mb-1">{details.hint}</p>}
							<a className="font-medium underline" href="/docs?doc=bring-your-own">
								{t("unavailableAction")}
							</a>
						</div>
					)}
					<div className="mt-auto flex justify-end pt-2">
						<CardAction
							e={details}
							busy={props.busyId === details.id}
							canWrite={canWrite}
							onAdd={() => {
								props.onDetails?.(null);
								props.onAdd(details);
							}}
						/>
					</div>
				</SidePanel>
			)}
		</div>
	);
}

function errorMessage(err: unknown): string {
	const e = err as { data?: { error?: { message?: string }; message?: string }; error?: string };
	return e?.data?.error?.message ?? e?.data?.message ?? e?.error ?? "unknown error";
}

function CatalogSkeleton() {
	return (
		<div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6 p-4 sm:p-6" data-testid="catalog-loading">
			<Skeleton className="h-9 w-64" />
			<div className="flex gap-2">
				{Array.from({ length: 6 }, (_, i) => (
					<Skeleton key={i} className="h-8 w-24 rounded-full" />
				))}
			</div>
			<div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
				{Array.from({ length: 6 }, (_, i) => (
					<Skeleton key={i} className="h-36 rounded-xl" />
				))}
			</div>
		</div>
	);
}

export default function CatalogView() {
	const { t } = useTranslation("catalog");
	const { data, isLoading, isError, refetch } = useGetBenchCatalogQuery();
	const [addEntry] = useAddCatalogEntryMutation();
	const [uploadDataset] = useUploadCatalogDatasetMutation();
	const [filters, setFilters] = useState<CatalogFilters>(NO_FILTERS);
	const [busyId, setBusyId] = useState<string | null>(null);
	const [detailsFor, setDetailsFor] = useState<CatalogEntry | null>(null);
	const [importFor, setImportFor] = useState<CatalogEntry | null>(null);
	const [importForm, setImportForm] = useState<ImportForm>({ mode: "upload", from: "", url: "", file: null, ref: "", suite: "", limit: 5 });
	const [lastAdded, setLastAdded] = useState<CatalogAddResponse | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [jobId, setJobId] = useState<string | null>(null);
	const entries = useMemo(() => data?.entries ?? [], [data]);

	if (isLoading) return <CatalogSkeleton />;
	if (isError || !data)
		return (
			<div className="p-6">
				<EmptyState message={t("loadError")} testId="catalog-load-error" action={{ label: t("reload"), onClick: () => void refetch() }} />
			</div>
		);

	const run = async (e: CatalogEntry, body: Omit<CatalogAddRequest, "id"> = {}, file?: File | null) => {
		setBusyId(e.id);
		setError(null);
		try {
			if (file) {
				const up = await uploadDataset({ id: e.id, file }).unwrap();
				body = { ...body, from: up.from };
			}
			const res = await addEntry({ id: e.id, ...body }).unwrap();
			setLastAdded(res);
			if (res.job) setJobId(res.job.id);
			setImportFor(null);
		} catch (err) {
			setError(errorMessage(err));
		} finally {
			setBusyId(null);
		}
	};

	return (
		<CatalogBody
			entries={entries}
			sourceAvailable={data.source_available}
			starterAgents={data.starter_agents ?? []}
			filters={filters}
			onFilters={setFilters}
			busyId={busyId}
			detailsFor={detailsFor}
			onDetails={setDetailsFor}
			onAdd={(e) => {
				if (e.kind === "importer") {
					setError(null);
					setImportFor(e);
					setImportForm(initialImportForm(e));
					return;
				}
				void run(e);
			}}
			importFor={importFor}
			importForm={importForm}
			onImportForm={setImportForm}
			onStartImport={() => {
				if (!importFor) return;
				const f = importForm;
				const body: Omit<CatalogAddRequest, "id"> = { suite: f.suite, limit: f.limit };
				if (f.ref.trim()) body.set = { ref: f.ref.trim() };
				if (f.mode === "url") body.url = f.url.trim();
				if (f.mode === "path") body.from = f.from.trim();
				void run(importFor, body, f.mode === "upload" ? f.file : null);
			}}
			onCancelImport={() => {
				setImportFor(null);
				setError(null);
			}}
			lastAdded={lastAdded}
			error={error}
			jobPanel={jobId ? <JobLogPanel jobId={jobId} onClose={() => setJobId(null)} /> : null}
		/>
	);
}
