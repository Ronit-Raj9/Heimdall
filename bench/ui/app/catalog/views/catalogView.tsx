import { JobLogPanel } from "@/components/jobs/jobLogPanel";
import { EmptyState } from "@/components/shell/emptyState";
import { useConfirm } from "@/components/platform/confirmDialog";
import "@/components/suites/uf6bI18n";
import "@/app/evaluate/views/ufd3I18n";
import { importCommand } from "@/components/scenario/scenario.utils";
import { CopyCodeBlock } from "@/components/suites/copyCodeBlock";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Pager, pageSlice } from "@/components/ui/pager";
import { LoadError } from "@/components/runs/screenStates";
import { notify } from "@/lib/notify";
import { firstReason, WhyDisabled } from "@/components/ui/whyDisabled";
import { FilterSelect, FilterTokens, SegmentedControl, ToolbarSearch, type FilterToken } from "@/components/ui/toolbar";
import { isBuiltinSuite } from "@/components/suites/suiteDisplay";
import { SuiteRunButton } from "@/components/suites/suiteRunButton";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import { Dialog } from "@/components/ui/dialog";
import { idProblem, wholeNumberProblem } from "@/lib/inputChecks";
import { transportErrorText } from "@/lib/requestErrors";
import { useAddCatalogEntryMutation, useGetBenchCatalogQuery, useUploadCatalogDatasetMutation } from "@/lib/store/apis/catalogApi";
import type { Job } from "@/lib/types/bench";
import type { CatalogAddRequest, CatalogAddResponse, CatalogEntry } from "@/lib/types/catalog";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { CheckCircle2, Library } from "lucide-react";
import { parseAsInteger, parseAsString, useQueryState } from "nuqs";
import { categoryFromUrl, filterToUrl, sourceFromUrl } from "./catalogUrlFilters";
import { type ReactNode, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	CATEGORIES,
	CATEGORY_ICON,
	type CatalogFilters,
	type Category,
	cardDescription,
	catalogEntries,
	entryCategories,
	filterEntries,
	installedHref,
	NO_FILTERS,
	SECTIONS,
	type SectionId,
	type SourceFilter,
	sectionOf,
} from "./catalogModel";
import { displayFor } from "./catalogDisplay";
import { w4m5Text } from "@/components/onboard/w4m5I18n";

export { displayFor } from "./catalogDisplay";
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
	/** The task count as typed (wave 4, p09-2): checked by importProblem, sent as a number. */
	limit: number | string;
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

/** Why Start import is disabled, or null when the form can start (wave 4, p09-2). */
export function importProblem(f: ImportForm): string | null {
	const url = f.url.trim();
	const http = /^http:\/\//i.test(url);
	return firstReason(
		f.mode === "upload" && !f.file && w4m5Text("import.needFile"),
		f.mode === "url" && !http && !/^https:\/\/\S+$/.test(url) && w4m5Text("import.needLink"),
		f.mode === "url" && http && w4m5Text("import.httpLink"),
		idProblem(f.suite, "benchmark id"),
		wholeNumberProblem(f.limit, 1, 500),
	);
}

const MODES: DataMode[] = ["upload", "url", "path"];
const MODE_KEY = { upload: "modeUpload", url: "modeUrl", path: "modePath" } as const;
const SOURCES: SourceFilter[] = ["all", "ready", "needs"];
const SOURCE_KEY = { all: "sourceAll", ready: "sourceReady", needs: "sourceNeedsData" } as const;
const SECTION_KEY = { featured: "sectionFeatured", try: "sectionTry", import: "sectionImport" } as const;

export interface CatalogBodyProps {
	entries: CatalogEntry[];
	sourceAvailable: boolean;
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
	/** A failed import job: its error and the tasks it skipped (fix wave 3, N263). */
	importFailure?: ImportFailure | null;
	/** Suite ids the server's built-in library serves: such entries show Built in and Run, listed first. */
	builtinSuites?: ReadonlySet<string>;
	/** 1-based page of the filtered list (M6-65); 1 when absent. */
	page?: number;
	onPage?(page: number): void;
}

/** A failed catalog import: the job's error and every task it skipped with the reason. */
export interface ImportFailure {
	error: string;
	skipped: { id: string; reason: string }[];
}

/** The failure of a finished catalog.import job, or null when it did not fail. */
export function importFailureOf(job: Job | null | undefined): ImportFailure | null {
	if (!job || job.kind !== "catalog.import" || job.status !== "failed") return null;
	const raw = job.result?.skipped;
	const skipped = (Array.isArray(raw) ? raw : []).flatMap((s) => {
		const o = s as { id?: unknown; reason?: unknown } | null;
		return o && typeof o.id === "string" ? [{ id: o.id, reason: typeof o.reason === "string" ? o.reason : "" }] : [];
	});
	return { error: job.error ?? "", skipped };
}

/** The built-in library suite an entry stands for, or undefined. */
export function builtinSuiteOf(e: CatalogEntry, builtin: ReadonlySet<string> | undefined): string | undefined {
	if (!builtin || builtin.size === 0) return undefined;
	if (e.suite_id && builtin.has(e.suite_id)) return e.suite_id;
	return e.suites?.find((id) => builtin.has(id));
}

/** The Source cell key: a built-in benchmark has its data, so it never reads "Needs data" (Q-197). */
export function sourceKeyOf(e: CatalogEntry, builtin: ReadonlySet<string> | undefined): "sourceReady" | "sourceNeedsData" {
	return e.needs_data && !builtinSuiteOf(e, builtin) ? "sourceNeedsData" : "sourceReady";
}

/** Quiet text tag, e.g. "Example". */
function QuietTag({ children }: { children: ReactNode }) {
	return <span className="text-muted-foreground shrink-0 text-xs">{children}</span>;
}

/** Small muted category icon shown inline with a title. */
function CategoryIcon({ category }: { category: Category }) {
	const Icon = CATEGORY_ICON[category];
	return <Icon className="text-muted-foreground size-4 shrink-0" aria-hidden />;
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
				<span className="font-medium">{t("cardFetch")}</span>{" "}
				{e.fetch_allowed ? (e.fetch_url ? t("cardFetchKnown") : t("cardFetchAny")) : t("cardFetchNo")}
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
			{e.importer && <Fact label={t("importer")}>{mono([e.importer])}</Fact>}
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
			{e.kind === "importer" && e.importer && <ImporterCommand importer={e.importer} />}
		</dl>
	);
}

/**
 * Decision 5 (M6-49): the command line text that the old Import tab showed, closed by default under
 * the importer's facts.
 */
function ImporterCommand({ importer }: { importer: string }) {
	const { t } = useTranslation("ufd3");
	const { t: tb } = useTranslation("bench");
	return (
		<details className="min-w-0 rounded-md border px-3 py-2" data-testid={`catalog-cli-${importer}`}>
			<summary className="cursor-pointer text-sm font-medium">{t("suitesCli.summary")}</summary>
			<div className="mt-2 flex min-w-0 flex-col gap-1.5">
				<p className="text-muted-foreground text-xs">{t("suitesCli.intro")}</p>
				<CopyCodeBlock
					code={importCommand(importer)}
					label={tb("imports.commandLabel", { name: importer })}
					testId={`catalog-cli-${importer}-command`}
				/>
			</div>
		</details>
	);
}

/** Right-hand drawer: entry details, and the import form for importers. */
/** The details or import drawer: the shared Dialog as a right-hand drawer (focus trap, Escape, overlay close). */
function SidePanel({ e, onClose, children, testId }: { e: CatalogEntry; onClose(): void; children: ReactNode; testId: string }) {
	const { t } = useTranslation("catalog");
	const { title: name, description, example } = displayFor(e);
	const cat = entryCategories(e)[0];
	return (
		<Dialog
			open
			onOpenChange={(open) => !open && onClose()}
			side="right"
			size="md"
			closeLabel={t("close")}
			title={
				<span className="flex min-w-0 items-center gap-2">
					<CategoryIcon category={cat} />
					<span className="truncate">{name}</span>
					{example && <QuietTag>{t("pillExample")}</QuietTag>}
				</span>
			}
			description={description}
			testId={testId}
		>
			<div className="flex flex-col gap-5">
				{e.summary && <p className="text-sm leading-relaxed">{e.summary}</p>}
				{children}
			</div>
		</Dialog>
	);
}

function ImportPanel(props: CatalogBodyProps & { importFor: CatalogEntry; importForm: ImportForm }) {
	const { t } = useTranslation("catalog");
	const { t: tu } = useTranslation("uf6b");
	const e = props.importFor;
	const f = props.importForm;
	const set = (p: Partial<ImportForm>) => props.onImportForm?.({ ...f, ...p });
	const why = importProblem(f);
	return (
		<SidePanel e={e} onClose={() => props.onCancelImport?.()} testId="catalog-import-form">
			<p className="text-sm text-pretty" data-testid="catalog-import-what">
				{tu("catalog.importWhat", { count: Number(f.limit) || 0 })}
			</p>
			<DataOptions e={e} />
			<div className="bg-muted inline-flex w-fit flex-wrap gap-1 rounded-lg p-1" role="tablist">
				{MODES.filter((m) => m !== "url" || e.fetch_allowed).map((m) => (
					<button
						key={m}
						type="button"
						role="tab"
						aria-selected={f.mode === m}
						className={cn(
							"rounded-md px-3 py-1 text-sm transition-colors",
							f.mode === m ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground",
						)}
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
					<Input
						type="file"
						accept=".zip,.jsonl,.json,.parquet,.csv,.tsv,.txt"
						onChange={(ev) => set({ file: ev.target.files?.[0] ?? null })}
						data-testid="catalog-import-file"
					/>
					{f.file && (
						<span className="text-xs font-normal" data-testid="catalog-import-chosen">
							{w4m5Text("import.chosen", { name: f.file.name })}
						</span>
					)}
					<span className="text-muted-foreground text-xs font-normal">
						{t("uploadHelp", { expects: e.upload_expects ?? "", path: e.dataset_path ?? "uploads/datasets" })}
					</span>
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
					<Input
						value={f.ref}
						placeholder="40-hex commit"
						onChange={(ev) => set({ ref: ev.target.value })}
						data-testid="catalog-import-ref"
					/>
				</label>
			)}
			<div className="flex gap-3">
				<label className="flex flex-1 flex-col gap-1.5 text-sm font-medium">
					{t("suiteLabel")}
					<Input value={f.suite} onChange={(ev) => set({ suite: ev.target.value })} data-testid="catalog-import-suite" />
				</label>
				<label className="flex w-32 flex-col gap-1.5 text-sm font-medium">
					{t("limitLabel")}
					<Input
						type="number"
						min={1}
						max={500}
						value={f.limit}
						onChange={(ev) => set({ limit: ev.target.value })}
						data-testid="catalog-import-limit"
					/>
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
				<Button
					disabled={why !== null || !importReady(f) || props.busyId === e.id}
					aria-describedby={why ? "catalog-import-why" : undefined}
					onClick={props.onStartImport}
					data-testid="catalog-import-start"
				>
					{props.busyId === e.id ? t("adding") : t("startImport")}
				</Button>
			</div>
			<WhyDisabled reason={props.busyId === e.id ? null : why} testId="catalog-import-why" className="text-right" />
		</SidePanel>
	);
}

function CardAction({
	e,
	busy,
	canWrite,
	onAdd,
	builtinSuite,
}: {
	e: CatalogEntry;
	busy: boolean;
	canWrite: boolean;
	onAdd(): void;
	builtinSuite?: string;
}) {
	const { t } = useTranslation("catalog");
	const { t: tu } = useTranslation("uf6b");
	if (builtinSuite)
		return (
			<span className="flex items-center gap-2" data-testid={`catalog-builtin-${e.id}`}>
				<QuietTag>{t("builtIn")}</QuietTag>
				<SuiteRunButton suiteId={builtinSuite} testId={`catalog-run-${e.id}`} />
			</span>
		);
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
	// M6-63: the button says what it does and that it costs nothing.
	const hint = e.kind === "importer" ? tu("catalog.importHint") : tu("catalog.addHint");
	return (
		<Button
			size="sm"
			variant={e.kind === "importer" ? "outline" : "default"}
			disabled={busy}
			onClick={onAdd}
			data-testid={`catalog-add-${e.id}`}
			title={hint}
			className="h-7"
		>
			{busy ? t("adding") : e.kind === "importer" ? t("import") : t("add")}
		</Button>
	);
}

/**
 * One catalog entry as a table row (M6-65): the name opens the details (M6-66), the line under it
 * says what the benchmark tests; category and source from sm up; the action at the right. On a
 * phone the row is two lines, name then description, with the action beside it.
 */
function EntryRow({ e, props, canWrite }: { e: CatalogEntry; props: CatalogBodyProps; canWrite: boolean }) {
	const { t } = useTranslation("catalog");
	const { t: tu } = useTranslation("uf6b");
	const { title, example } = displayFor(e);
	const cat = entryCategories(e)[0];
	const description = cardDescription(e);
	return (
		<TableRow data-testid={`catalog-entry-${e.id}`}>
			<TableCell priority="primary" className="max-w-0 py-2 whitespace-normal sm:w-auto">
				<div className="flex min-w-0 items-center gap-2">
					<CategoryIcon category={cat} />
					<button
						type="button"
						className="hover:text-foreground min-h-9 min-w-0 line-clamp-2 text-left text-sm sm:line-clamp-1 font-medium underline-offset-4 hover:underline"
						onClick={() => props.onDetails?.(e)}
						aria-label={tu("catalog.detailsOf", { name: title })}
						title={title}
						data-testid={`catalog-details-${e.id}`}
					>
						{title}
					</button>
					{example && <QuietTag>{t("pillExample")}</QuietTag>}
				</div>
				{description && <p className="text-muted-foreground line-clamp-1 text-xs text-pretty sm:line-clamp-2">{description}</p>}
			</TableCell>
			<TableCell priority="secondary" className="text-muted-foreground py-2 text-xs">{t(`cat_${cat}`)}</TableCell>
			<TableCell priority="secondary" className="text-muted-foreground hidden py-2 text-xs md:table-cell">
				{t(sourceKeyOf(e, props.builtinSuites))}
			</TableCell>
			<TableCell priority="status" className="py-2 text-right">
				<CardAction
					e={e}
					busy={props.busyId === e.id}
					canWrite={canWrite}
					onAdd={() => props.onAdd(e)}
					builtinSuite={builtinSuiteOf(e, props.builtinSuites)}
				/>
			</TableCell>
		</TableRow>
	);
}

/** The catalog screen without data fetching (tested directly). */
export function CatalogBody(props: CatalogBodyProps) {
	const { t } = useTranslation("catalog");
	const { t: tu } = useTranslation("uf6b");
	const canWrite = canUse(useSession(), "member");
	const { filters } = props;
	// Built-in library entries first, the rest in their usual order (stable sort).
	const shown = filterEntries(props.entries, filters)
		.map((e, i) => ({ e, i, b: builtinSuiteOf(e, props.builtinSuites) ? 0 : 1 }))
		.sort((x, y) => x.b - y.b || x.i - y.i)
		.map((x) => x.e);
	const present = useMemo(() => {
		const s = new Set<Category>();
		for (const e of props.entries) for (const c of entryCategories(e)) s.add(c);
		return CATEGORIES.filter((c) => s.has(c));
	}, [props.entries]);
	const set = (patch: Partial<CatalogFilters>) => props.onFilters({ ...filters, ...patch });
	const tokens = (
		[
			filters.category !== "all" && {
				key: "category",
				label: `${t("category")}: ${t(`cat_${filters.category}`)}`,
				onRemove: () => set({ category: "all" }),
			},
			filters.source !== "all" && { key: "source", label: t(SOURCE_KEY[filters.source]), onRemove: () => set({ source: "all" }) },
		] as (FilterToken | false | "" | null | undefined)[]
	).filter((x): x is FilterToken => Boolean(x));
	const res = props.lastAdded?.result;
	// M6-65: 50 entries a page, in section order, then grouped by section within the page.
	const ordered = SECTIONS.flatMap((id) => shown.filter((e) => sectionOf(e) === id));
	const slice = pageSlice(ordered, props.page ?? 1);
	const grouped = SECTIONS.map((id) => ({ id, items: slice.rows.filter((e) => sectionOf(e) === id) })).filter((g) => g.items.length > 0);
	const details = props.detailsFor && !props.importFor ? props.detailsFor : null;
	return (
		<div className="flex w-full min-w-0 flex-col gap-8" data-testid="catalog-view">
			<header className="flex flex-col gap-5">
				<div>
					{/* X-42: the Benchmarks hub header holds the page's one h1; the tab's title is an h2. */}
					<h2 className="text-lg font-semibold">{t("title")}</h2>
					<p className="text-muted-foreground mt-1 text-sm text-pretty">{t("subtitle")}</p>
				</div>
				<div className="flex flex-col gap-2">
					<div className="flex flex-wrap items-center gap-2" role="toolbar" data-testid="catalog-toolbar">
						<ToolbarSearch
							value={filters.query}
							onChange={(v) => set({ query: v })}
							placeholder={t("search")}
							label={t("search")}
							testId="catalog-search"
						/>
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
				<div
					className="border-chart-error/50 bg-chart-error/5 text-chart-error-ink rounded-xl border p-4 text-sm"
					data-testid="catalog-error"
				>
					{t("addFailed", { message: props.error })}
				</div>
			)}
			{props.lastAdded && (
				<div
					className="border-chart-success/50 bg-chart-success/5 flex items-center gap-2 rounded-xl border p-4 text-sm"
					data-testid="catalog-added"
				>
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
			{props.importFailure && (
				<div
					className="border-chart-error/50 bg-chart-error/5 flex flex-col gap-2 rounded-xl border p-4 text-sm"
					data-testid="catalog-import-failed"
				>
					<p className="text-chart-error-ink">{t("importFailed", { message: props.importFailure.error })}</p>
					{props.importFailure.skipped.length > 0 && (
						<div>
							<p className="font-medium">{t("skippedTasks", { count: props.importFailure.skipped.length })}</p>
							<ul className="mt-1 flex flex-col gap-0.5 pl-4" data-testid="catalog-import-skipped">
								{props.importFailure.skipped.map((s) => (
									<li key={s.id} className="list-disc">
										<span className="font-mono text-xs">{s.id}</span>: {s.reason}
									</li>
								))}
							</ul>
						</div>
					)}
				</div>
			)}
			{props.jobPanel}
			{shown.length === 0 ? (
				<EmptyState
					icon={Library}
					message={t("empty")}
					testId="catalog-empty"
					action={{ label: t("clearFilters"), onClick: () => props.onFilters(NO_FILTERS) }}
				/>
			) : (
				grouped.map((g) => (
					<section key={g.id} className="flex min-w-0 flex-col gap-3" data-testid={`catalog-section-${g.id}`}>
						<h2 className="text-muted-foreground text-sm font-medium">{t(SECTION_KEY[g.id as SectionId])}</h2>
						<div className="min-w-0 border-y">
							<Table className="table-fixed">
								<TableHeader className="sr-only sm:not-sr-only">
									<TableRow>
										<TableHead priority="primary">{tu("catalog.colName")}</TableHead>
										<TableHead priority="secondary" className="w-32">{t("category")}</TableHead>
										<TableHead priority="secondary" className="hidden w-32 md:table-cell">{t("sourceLabel")}</TableHead>
										<TableHead priority="status" className="w-28 text-right sm:w-44">
											<span className="sr-only">{tu("catalog.colAction")}</span>
										</TableHead>
									</TableRow>
								</TableHeader>
								<TableBody>
									{g.items.map((e) => (
										<EntryRow key={e.id} e={e} props={props} canWrite={canWrite} />
									))}
								</TableBody>
							</Table>
						</div>
					</section>
				))
			)}
			{shown.length > 0 && props.onPage && (
				<Pager
					page={slice.page}
					pageCount={slice.pageCount}
					onPage={props.onPage}
					rangeText={tu("catalog.pageOf", { first: slice.first, last: slice.last, total: ordered.length })}
					testId="catalog-pager"
				/>
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

/** The question Add asks before it writes (M6-62): what is added, under which id, and that nothing runs. */
export function addQuestion(e: CatalogEntry, tu: (k: string, o: Record<string, string>) => string): string {
	const name = displayFor(e).title;
	if (e.kind === "example") return tu("catalog.addAskExample", { name });
	return tu("catalog.addAsk", { name, id: e.suite_id ?? e.id });
}

function errorMessage(err: unknown): string {
	const transport = transportErrorText(err);
	if (transport) return transport;
	const e = err as { data?: { error?: { message?: string }; message?: string }; error?: string };
	return e?.data?.error?.message ?? e?.data?.message ?? e?.error ?? "unknown error";
}

function CatalogSkeleton() {
	return (
		<div className="flex w-full min-w-0 flex-col gap-6" data-testid="catalog-loading">
			<Skeleton className="h-9 w-64" />
			<div className="flex gap-2">
				{Array.from({ length: 6 }, (_, i) => (
					<Skeleton key={i} className="h-8 w-24 rounded-full" />
				))}
			</div>
			<div className="flex min-w-0 flex-col gap-2">
				{Array.from({ length: 8 }, (_, i) => (
					<Skeleton key={i} className="h-12 rounded-md" />
				))}
			</div>
		</div>
	);
}

export default function CatalogView() {
	const { t } = useTranslation("catalog");
	const { data, isLoading, isError, refetch, error: loadError } = useGetBenchCatalogQuery();
	const suites = useGetSuitesLightQuery();
	const builtinSuites = useMemo(
		() => new Set((suites.data?.suites ?? []).filter((s) => isBuiltinSuite(s) && !s.error).map((s) => s.id)),
		[suites.data],
	);
	const [addEntry] = useAddCatalogEntryMutation();
	const confirm = useConfirm();
	const { t: tu } = useTranslation("uf6b");
	const [uploadDataset] = useUploadCatalogDatasetMutation();
	// p12-2, X-85: the search term and the category and source filters live in the address.
	const [q, setQ] = useQueryState("q", parseAsString.withDefault("").withOptions({ history: "replace" }));
	const [categoryParam, setCategoryParam] = useQueryState("category", parseAsString.withOptions({ history: "replace" }));
	const [sourceParam, setSourceParam] = useQueryState("source", parseAsString.withOptions({ history: "replace" }));
	const filters: CatalogFilters = {
		...NO_FILTERS,
		category: categoryFromUrl(categoryParam),
		source: sourceFromUrl(sourceParam),
		query: q ?? "",
	};
	const [page, setPage] = useQueryState("page", parseAsInteger.withDefault(1).withOptions({ history: "replace" }));
	const onFilters = (f: CatalogFilters) => {
		void setPage(null);
		if (f.category !== filters.category) void setCategoryParam(filterToUrl(f.category));
		if (f.source !== filters.source) void setSourceParam(filterToUrl(f.source));
		if (f.query !== filters.query) void setQ(f.query === "" ? null : f.query);
	};
	const [busyId, setBusyId] = useState<string | null>(null);
	const [detailsFor, setDetailsFor] = useState<CatalogEntry | null>(null);
	const [importFor, setImportFor] = useState<CatalogEntry | null>(null);
	const [importForm, setImportForm] = useState<ImportForm>({ mode: "upload", from: "", url: "", file: null, ref: "", suite: "", limit: 5 });
	const [lastAdded, setLastAdded] = useState<CatalogAddResponse | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [jobId, setJobId] = useState<string | null>(null);
	const [importFailure, setImportFailure] = useState<ImportFailure | null>(null);
	// Internal entries left out and one entry per suite (N6-1, M6-64).
	const entries = useMemo(() => catalogEntries(data?.entries ?? []), [data]);

	if (isLoading) return <CatalogSkeleton />;
	if (isError || !data)
		return (
			<LoadError title={t("loadError")} error={loadError} onRetry={() => void refetch()} testId="catalog-load-error" />
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
			setImportFailure(null);
			if (!res.job) notify({ text: tu("catalog.added", { name: e.title || e.id }) });
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
			builtinSuites={builtinSuites}
			sourceAvailable={data.source_available}
			filters={filters}
			onFilters={onFilters}
			page={page}
			onPage={(p) => void setPage(p)}
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
				// M6-62: Add writes to the project, so it asks first and says nothing runs or is charged.
				void (async () => {
					const ok = await confirm(addQuestion(e, (k, o) => String(tu(k as never, o as never))), { title: tu("catalog.addTitle"), confirmLabel: tu("catalog.addYes") });
					if (ok) await run(e);
				})();
			}}
			importFor={importFor}
			importForm={importForm}
			onImportForm={setImportForm}
			onStartImport={() => {
				if (!importFor) return;
				const f = importForm;
				const body: Omit<CatalogAddRequest, "id"> = { suite: f.suite, limit: Number(f.limit) };
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
			importFailure={importFailure}
			jobPanel={
				jobId ? (
					<JobLogPanel
						jobId={jobId}
						onClose={() => setJobId(null)}
						onFinished={(job) => {
							const failed = importFailureOf(job);
							setImportFailure(failed);
							if (failed) setLastAdded(null); // "Import started" no longer holds
						}}
					/>
				) : null
			}
		/>
	);
}