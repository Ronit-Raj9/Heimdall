import { JobLogPanel } from "@/components/jobs/jobLogPanel";
import { EmptyState } from "@/components/shell/emptyState";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useAddCatalogEntryMutation, useGetBenchCatalogQuery } from "@/lib/store/apis/catalogApi";
import type { CatalogAddResponse, CatalogEntry, CatalogKind } from "@/lib/types/catalog";
import { Link } from "@tanstack/react-router";
import { Library } from "lucide-react";
import { type ReactNode, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		catalog: typeof import("@/locales/en/catalog.json");
	}
}

export type CatalogFilter = "all" | CatalogKind;

/** Entries matching the kind filter and a free-text query over title, id, summary and tags. */
export function filterEntries(entries: CatalogEntry[], filter: CatalogFilter, query: string): CatalogEntry[] {
	const q = query.trim().toLowerCase();
	return entries.filter((e) => {
		if (filter !== "all" && e.kind !== filter) return false;
		if (!q) return true;
		return [e.title, e.id, e.summary, ...(e.tags ?? []), ...(e.benchmarks ?? [])].join(" ").toLowerCase().includes(q);
	});
}

export interface ImportForm {
	from: string;
	suite: string;
	limit: number;
}

export interface CatalogBodyProps {
	entries: CatalogEntry[];
	sourceAvailable: boolean;
	starterAgents: string[];
	filter: CatalogFilter;
	onFilter(f: CatalogFilter): void;
	query: string;
	onQuery(q: string): void;
	onAdd(e: CatalogEntry): void;
	busyId?: string | null;
	importFor?: CatalogEntry | null;
	importForm?: ImportForm;
	onImportForm?(f: ImportForm): void;
	onStartImport?(): void;
	onCancelImport?(): void;
	lastAdded?: CatalogAddResponse | null;
	error?: string | null;
	jobPanel?: ReactNode;
}

const FILTERS: CatalogFilter[] = ["all", "example", "bundled", "importer"];
const FILTER_KEY = { all: "filterAll", example: "filterExample", bundled: "filterBundled", importer: "filterImporter" } as const;

/** Where an installed entry lives: its suite page, else the agents or suites list. */
export function installedHref(e: CatalogEntry): string {
	const suite = e.suite_id ?? e.suites?.[0];
	if (suite) return `/suites/${encodeURIComponent(suite)}`;
	return (e.agents?.length ?? 0) > 0 ? "/agents" : "/suites";
}

/** The catalog screen without data fetching (tested directly). */
export function CatalogBody(props: CatalogBodyProps) {
	const { t } = useTranslation("catalog");
	const canWrite = canUse(useSession(), "member");
	const shown = filterEntries(props.entries, props.filter, props.query);
	const res = props.lastAdded?.result;
	return (
		<div className="flex flex-col gap-4 p-6" data-testid="catalog-view">
			<div>
				<h1 className="text-xl font-semibold">{t("title")}</h1>
				<p className="text-muted-foreground text-sm">{t("subtitle")}</p>
				{props.starterAgents.length > 0 && (
					<p className="text-muted-foreground mt-1 text-xs" data-testid="catalog-starter">
						{t("starter", { agents: props.starterAgents.join(", ") })}
					</p>
				)}
			</div>
			{!props.sourceAvailable && (
				<div className="border-chart-warning/60 rounded border p-3 text-sm" data-testid="catalog-no-source">
					{t("noSource")}{" "}
					<a className="underline" href="/upload">
						{t("noSourceAction")}
					</a>
				</div>
			)}
			<div className="flex flex-wrap items-center gap-2">
				{FILTERS.map((f) => (
					<Button
						key={f}
						size="sm"
						variant={props.filter === f ? "default" : "outline"}
						onClick={() => props.onFilter(f)}
						data-testid={`catalog-filter-${f}`}
					>
						{t(FILTER_KEY[f])}
					</Button>
				))}
				<Input
					className="w-full sm:ml-auto sm:w-64"
					placeholder={t("search")}
					value={props.query}
					onChange={(ev) => props.onQuery(ev.target.value)}
					data-testid="catalog-search"
				/>
			</div>
			{props.error && (
				<div className="text-chart-error-ink text-sm" data-testid="catalog-error">
					{t("addFailed", { message: props.error })}
				</div>
			)}
			{props.lastAdded && (
				<div className="border-chart-success/50 rounded border p-3 text-sm" data-testid="catalog-added">
					{res ? (
						<span>
							{t("resultWritten", { count: res.written?.length ?? 0 })}
							{(res.skipped?.length ?? 0) > 0 && <>; {t("resultSkipped", { count: res.skipped?.length ?? 0 })}</>}.{" "}
							<Link to="/suites" className="underline">
								{t("openSuites")}
							</Link>
						</span>
					) : (
						<span>{t("importStarted")}</span>
					)}
				</div>
			)}
			{props.jobPanel}
			{props.importFor && props.importForm && (
				<div className="border-border flex flex-col gap-2 rounded-lg border p-4" data-testid="catalog-import-form">
					<div className="font-medium">
						{t("import")}: {props.importFor.title}
					</div>
					{props.importFor.source && (
						<div className="text-muted-foreground text-xs">
							{t("source")}: <code>{props.importFor.source}</code>
						</div>
					)}
					<label className="text-sm">
						{t("fromLabel")}
						<Input
							value={props.importForm.from}
							onChange={(ev) => props.onImportForm?.({ ...props.importForm!, from: ev.target.value })}
							data-testid="catalog-import-from"
						/>
						<span className="text-muted-foreground text-xs">{t("fromHelp")}</span>
					</label>
					<div className="flex gap-2">
						<label className="flex-1 text-sm">
							{t("suiteLabel")}
							<Input
								value={props.importForm.suite}
								onChange={(ev) => props.onImportForm?.({ ...props.importForm!, suite: ev.target.value })}
								data-testid="catalog-import-suite"
							/>
						</label>
						<label className="w-32 text-sm">
							{t("limitLabel")}
							<Input
								type="number"
								min={1}
								max={500}
								value={props.importForm.limit}
								onChange={(ev) => props.onImportForm?.({ ...props.importForm!, limit: Number(ev.target.value) || 1 })}
								data-testid="catalog-import-limit"
							/>
						</label>
					</div>
					<div className="flex gap-2">
						<Button
							size="sm"
							disabled={!props.importForm.from.trim() || props.busyId === props.importFor.id}
							onClick={props.onStartImport}
							data-testid="catalog-import-start"
						>
							{t("startImport")}
						</Button>
						<Button size="sm" variant="ghost" onClick={props.onCancelImport}>
							{t("cancel")}
						</Button>
					</div>
				</div>
			)}
			{shown.length === 0 ? (
				<EmptyState icon={Library} message={t("empty")} testId="catalog-empty" action={{ label: t("filterAll"), onClick: () => props.onFilter("all") }} />
			) : (
				<div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
					{shown.map((e) => (
						<div key={e.id} className="border-border flex flex-col gap-2 rounded-lg border p-4" data-testid={`catalog-entry-${e.id}`}>
							<div className="flex items-start justify-between gap-2">
								<div className="font-medium">{e.title}</div>
								<Badge variant={e.kind === "importer" ? "outline" : e.kind === "example" ? "secondary" : "default"}>{t(`kind_${e.kind}`)}</Badge>
							</div>
							<p className="text-muted-foreground line-clamp-3 text-sm">{e.summary}</p>
							{(e.suites?.length ?? 0) > 0 && (
								<div className="text-xs">
									{t("suites")}: <span className="font-mono">{e.suites!.join(", ")}</span>
								</div>
							)}
							{(e.agents?.length ?? 0) > 0 && (
								<div className="text-xs">
									{t("agents")}: <span className="font-mono">{e.agents!.join(", ")}</span>
								</div>
							)}
							{e.suite_id && e.kind !== "example" && <div className="font-mono text-xs">{e.suite_id}</div>}
							<div className="mt-auto flex items-center justify-between gap-2 pt-2">
								<span className="text-muted-foreground text-xs">{e.track}</span>
								{e.installed ? (
									<span className="flex items-center gap-2">
										<Badge variant="success" data-testid={`catalog-installed-${e.id}`}>
											{t("installed")}
										</Badge>
										<a className="text-xs underline" href={installedHref(e)} data-testid={`catalog-open-${e.id}`}>
											{t("open")}
										</a>
									</span>
								) : !e.available ? (
									<span className="text-muted-foreground flex flex-col items-end text-right text-xs" title={e.hint} data-testid={`catalog-unavailable-${e.id}`}>
										{t("unavailable")}
										<a className="underline" href="/docs?doc=bring-your-own">
											{t("unavailableAction")}
										</a>
									</span>
								) : canWrite ? (
									<Button size="sm" disabled={props.busyId === e.id} onClick={() => props.onAdd(e)} data-testid={`catalog-add-${e.id}`} title={e.hint}>
										{props.busyId === e.id ? t("adding") : e.kind === "importer" ? t("import") : t("add")}
									</Button>
								) : (
									<span className="text-muted-foreground text-xs">{t("memberOnly")}</span>
								)}
							</div>
						</div>
					))}
				</div>
			)}
		</div>
	);
}

function errorMessage(err: unknown): string {
	const e = err as { data?: { error?: { message?: string }; message?: string }; error?: string };
	return e?.data?.error?.message ?? e?.data?.message ?? e?.error ?? "unknown error";
}

export default function CatalogView() {
	const { t } = useTranslation("catalog");
	const { data, isLoading, isError, refetch } = useGetBenchCatalogQuery();
	const [addEntry] = useAddCatalogEntryMutation();
	const [filter, setFilter] = useState<CatalogFilter>("all");
	const [query, setQuery] = useState("");
	const [busyId, setBusyId] = useState<string | null>(null);
	const [importFor, setImportFor] = useState<CatalogEntry | null>(null);
	const [importForm, setImportForm] = useState<ImportForm>({ from: "", suite: "", limit: 5 });
	const [lastAdded, setLastAdded] = useState<CatalogAddResponse | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [jobId, setJobId] = useState<string | null>(null);
	const entries = useMemo(() => data?.entries ?? [], [data]);

	if (isLoading) return <div className="text-muted-foreground p-6 text-sm">{t("loading")}</div>;
	if (isError || !data)
		return (
			<div className="p-6">
				<EmptyState message={t("loadError")} testId="catalog-load-error" action={{ label: "Reload", onClick: () => void refetch() }} />
			</div>
		);

	const run = async (e: CatalogEntry, body: { from?: string; suite?: string; limit?: number } = {}) => {
		setBusyId(e.id);
		setError(null);
		try {
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
			filter={filter}
			onFilter={setFilter}
			query={query}
			onQuery={setQuery}
			busyId={busyId}
			onAdd={(e) => {
				if (e.kind === "importer") {
					setImportFor(e);
					setImportForm({ from: e.importer ? `datasets/${e.id}` : "", suite: e.suite_id ?? "", limit: 5 });
					return;
				}
				void run(e);
			}}
			importFor={importFor}
			importForm={importForm}
			onImportForm={setImportForm}
			onStartImport={() => importFor && void run(importFor, { from: importForm.from, suite: importForm.suite, limit: importForm.limit })}
			onCancelImport={() => setImportFor(null)}
			lastAdded={lastAdded}
			error={error}
			jobPanel={jobId ? <JobLogPanel jobId={jobId} onClose={() => setJobId(null)} /> : null}
		/>
	);
}
