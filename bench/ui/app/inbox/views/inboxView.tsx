import { INBOX_ALL, INBOX_FILTERS, filterInboxItems, inboxCount, type InboxFilter } from "@/components/inbox/inbox.utils";
import { InboxConnectAnotherLink, InboxEmpty, InboxIngestOffNote, InboxSourcesTable } from "@/components/inbox/inboxSources";
import { InboxStateFilter } from "@/components/inbox/inboxStateFilter";
import { SessionImport } from "@/components/inbox/sessionImport";
import { InboxTable } from "@/components/inbox/inboxTable";
import { formatInboxTime } from "@/components/inbox/inboxBadges";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { useGetInboxQuery } from "@/lib/store/apis/inboxApi";
import type { InboxItem, InboxListResponse, InboxSourceStatus } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { parseAsString, parseAsStringLiteral, useQueryState } from "nuqs";
import { useEffect, useId, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { InboxPullButton, InboxPullSetup, InboxWebhook } from "./inboxConnect";
import { useGetInboxWebhookQuery, useGetPullReadinessQuery } from "@/lib/store/apis/liveinboxApi";
import { InboxItemPanel } from "./inboxItemPanel";
import { Collapsible } from "@/components/inbox/featureHero";
import { InboxBulkAccept } from "@/components/inbox/inboxBulkAccept";
import { ListChecks, Plug } from "lucide-react";
import { UnknownLinkNote } from "@/components/shell/unknownLinkNote";
import { ToolbarSearch } from "@/components/ui/toolbar";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { INBOX_SINCE, narrowInboxItems, openingInboxFilter, type InboxSince } from "./inboxDefaults";
import "./uf7aI18n";

/** The state the screen opens on: what still needs a decision. */
const DEFAULT_FILTER: InboxFilter = "new";

/**
 * The Production failures tab of /failures: ?state=&source=&item=. Production failures pulled from trace sources,
 * one row per signature. state and source go to the server; item opens one item beside the table,
 * where it can be accepted into a suite as a draft regression task, rejected or snoozed
 * (docs/failure-inbox.md).
 */
export function InboxTab() {
	const { t } = useTranslation("bench");
	const { t: u } = useTranslation("ux13");
	const { t: g } = useTranslation("inboxGuide");
	const { t: m } = useTranslation("maximFailures");
	const [urlFilter, setFilter] = useQueryState("state", parseAsStringLiteral(INBOX_FILTERS));
	const [source, setSource] = useQueryState("source", parseAsString);
	const [itemId, setItemId] = useQueryState("item", parseAsString);
	const [q, setQ] = useQueryState("q", parseAsString);
	// With no state in the URL the screen opens on New, and moves to All once the counts say nothing is new but items exist (M7-2).
	const [openOnAll, setOpenOnAll] = useState(false);
	const filter: InboxFilter = urlFilter ?? (openOnAll ? INBOX_ALL : DEFAULT_FILTER);
	const list = useGetInboxQuery({
		state: filter === INBOX_ALL ? undefined : filter,
		source: source ?? undefined,
	});
	const data = list.data;
	useEffect(() => {
		if (!urlFilter && !openOnAll && data && openingInboxFilter(data.counts) === INBOX_ALL) setOpenOnAll(true);
	}, [urlFilter, openOnAll, data]);
	const ready = useGetPullReadinessQuery().data;
	const hosted = useGetInboxWebhookQuery().data?.hosted ?? false;
	const blocked = useMemo(() => new Set((ready?.inbox?.secrets ?? []).filter((x) => !x.set && x.for).map((x) => x.for)), [ready]);
	// The bulk section stays once it was shown, so its result line survives accepting the last open items (wave 4, p13-1).
	const { t: f } = useTranslation("uf7a");
	const summary = sourcesSummary(data?.sources ?? []);
	const sourcesLine = summary
		? f("inbox.sourcesLine", { count: summary.count, last: summary.last ? formatInboxTime(summary.last) : f("inbox.never") })
		: null;
	const hasOpen = (data?.items ?? []).some((item) => item.state === "new" || item.state === "snoozed");
	const [bulkSeen, setBulkSeen] = useState(false);
	useEffect(() => {
		if (hasOpen) setBulkSeen(true);
	}, [hasOpen]);

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="inbox-screen">
			<div className="flex flex-wrap items-center gap-3">
				<p className="text-muted-foreground max-w-3xl text-sm">{t("inbox.subtitle")}</p>
				{data?.mode === "live" && <InboxPullButton check={ready?.inbox} />}
				<Link to="/maxim" className="text-primary text-sm font-medium underline underline-offset-2" data-testid="inbox-maxim-link">
					{m("showcaseLink")}
				</Link>
			</div>
			{list.isLoading ? (
				<BlockSkeleton rows={6} testId="inbox-loading" />
			) : list.error || !data ? (
				<LoadError title={t("inbox.loadError")} error={list.error} onRetry={() => void list.refetch()} testId="inbox-error" />
			) : (
				<InboxBody
					// Hosted takes pushes per org behind its ingest tokens (Push failures in, below), so the local "push is off" note does not apply.
					data={hosted ? { ...data, ingest_enabled: true } : data}
					filter={filter}
					onFilter={(next) => void setFilter(next)}
					openedOnAll={!urlFilter && openOnAll}
					blockedSources={blocked}
					q={q ?? ""}
					onQ={(next) => void setQ(next)}
					source={source}
					onSource={(next) => void setSource(next)}
					itemId={itemId}
					onItem={(next) => void setItemId(next)}
					manageSources={data.mode === "live"}
					onSourcesChanged={() => void list.refetch()}
					// Live: the sources table sits in the folded Integrations block below (M7-5).
					showSources={data.mode !== "live"}
					panel={
						itemId ? <InboxItemPanel key={itemId} itemId={itemId} suites={data.suites ?? []} onClose={() => void setItemId(null)} /> : null
					}
				/>
			)}
			{/* While one item is open its Turn into task form is the one submit; the bulk form waits until it closes (R7-4). */}
			{data?.mode === "live" && (hasOpen || bulkSeen) && !itemId && (
				<Collapsible icon={ListChecks} title={g("bulk.title")} hint={g("bulk.hint")} testId="inbox-bulk">
					<InboxBulkAccept items={data.items ?? []} suites={data.suites ?? []} />
				</Collapsible>
			)}
			{data?.mode === "live" && (
				<Collapsible
					icon={Plug}
					title={u("inbox.integrations")}
					hint={sourcesLine ?? u("inbox.integrationsHint")}
					// Open only while no source exists; with sources the list comes first and one line sums them up (M7-5).
					defaultOpen={(data.sources ?? []).length === 0}
					testId="inbox-integrations"
				>
					<Section title={t("inbox.sources.title")} description={t("inbox.sources.description")} testId="inbox-sources">
						<InboxSourcesTable sources={data.sources ?? []} manage onChanged={() => void list.refetch()} blocked={blocked} />
						{(data.sources ?? []).length > 0 && <InboxConnectAnotherLink />}
					</Section>
					<InboxPullSetup check={ready?.inbox} hosted={ready?.hosted} />
					<InboxWebhook />
				</Collapsible>
			)}
		</section>
	);
}

export interface InboxBodyProps {
	data: InboxListResponse;
	filter: InboxFilter;
	onFilter: (filter: InboxFilter) => void;
	source: string | null;
	onSource: (source: string | null) => void;
	itemId: string | null;
	onItem: (itemId: string | null) => void;
	/** The open item's panel; null when no item is open. */
	panel: ReactNode;
	/** Live mode: each source row can be removed, and a link leads to the add form (wave 4, p13-2). */
	manageSources?: boolean;
	/** Called after a source was removed, to reload the list. */
	onSourcesChanged?: () => void;
	/** The screen moved to All by itself because nothing is new (M7-2): a line says so. */
	openedOnAll?: boolean;
	/** Sources whose read key the pull check lists as not stored (M7-7). */
	blockedSources?: ReadonlySet<string>;
	/** The search text (?q=) and its setter; the list narrows to items holding it (M7-13). */
	q?: string;
	onQ?: (q: string | null) => void;
	/** Show the "Where failures come from" table under the list; off when the page folds it into Integrations (M7-5). Default true. */
	showSources?: boolean;
}

/** The benchmark that holds most of the test tasks made from the inbox, for the count's link (M7-15); null with none. Exported for tests. */
export function regressionSuite(items: readonly InboxItem[] | null | undefined): string | null {
	const counts = new Map<string, number>();
	for (const it of items ?? []) if (it.task?.suite) counts.set(it.task.suite, (counts.get(it.task.suite) ?? 0) + 1);
	let best: string | null = null;
	for (const [suite, n] of counts) if (best === null || n > (counts.get(best) ?? 0)) best = suite;
	return best;
}

/** How many sources and the newest pull, for the one line of the folded setup; null with no source. Exported for tests. */
export function sourcesSummary(sources: readonly InboxSourceStatus[]): { count: number; last: string | null } | null {
	if (sources.length === 0) return null;
	const last = sources
		.map((s) => s.last_pull ?? "")
		.filter(Boolean)
		.sort()
		.at(-1);
	return { count: sources.length, last: last ?? null };
}

/** The names the "Comes from" filter offers: the configured sources and the source of every item, sorted and unique (wave 4, p12-6). */
export function sourceNames(sources: readonly InboxSourceStatus[], items: readonly InboxItem[] | null | undefined): string[] {
	const names = new Set<string>();
	for (const s of sources) if (s.name) names.add(s.name);
	for (const it of items ?? []) if (it.source) names.add(it.source);
	return [...names].sort();
}

/** The loaded inbox: filters, the table or the empty state, the open item and the sources. Exported for tests. */
export function InboxBody({
	data,
	filter,
	onFilter,
	source,
	onSource,
	itemId,
	onItem,
	panel,
	manageSources,
	onSourcesChanged,
	openedOnAll,
	blockedSources,
	q = "",
	onQ = () => undefined,
	showSources = true,
}: InboxBodyProps) {
	const { t } = useTranslation("bench");
	const { t: w } = useTranslation("w4m7");
	const { t: f } = useTranslation("uf7a");
	const [since, setSince] = useState<InboxSince>("");
	const stateItems = useMemo(() => filterInboxItems(data.items, filter, source), [data.items, filter, source]);
	const items = useMemo(() => narrowInboxItems(stateItems, q, since), [stateItems, q, since]);
	const sources = data.sources ?? [];
	const total = inboxCount(data.counts, INBOX_ALL);
	const empty = total === 0 && (data.items ?? []).length === 0;
	const narrowed = Boolean(source || q.trim() || since);

	return (
		<div className="flex min-w-0 flex-col gap-6">
			{!data.ingest_enabled && <InboxIngestOffNote />}
			{empty ? (
				<>
					{itemId && (
						<UnknownLinkNote
							text={w("unknown.failure", { name: itemId })}
							showAllLabel={w("unknown.showAll")}
							onShowAll={() => onItem(null)}
							testId="unknown-link-failure"
						/>
					)}
					<InboxEmpty />
				</>
			) : (
				<div className={cn("grid min-w-0 grid-cols-1 items-start gap-6", panel && "2xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]")} data-testid="inbox-layout">
					<Section
						title={t("inbox.table.title")}
						description={t("inbox.table.description")}
						testId="inbox-items"
						actions={
							<div className="flex flex-wrap items-center gap-3">
								<RegressionTasksCount count={data.regression_tasks ?? 0} suite={regressionSuite(data.items)} />
								<SessionImport
									onImported={(id) => {
										onFilter("new");
										onItem(id);
									}}
								/>
							</div>
						}
					>
						<div className="flex flex-col gap-4">
							<div className="flex flex-wrap items-center gap-3" data-testid="inbox-filter-row">
								<InboxStateFilter counts={data.counts} value={filter} onChange={onFilter} />
								<SourceFilter names={sourceNames(sources, data.items)} value={source} onChange={onSource} />
								<SinceFilter value={since} onChange={setSince} />
								<ToolbarSearch
									value={q}
									onChange={(next) => onQ(next || null)}
									label={f("inbox.search")}
									placeholder={f("inbox.searchPlaceholder")}
									testId="inbox-search"
								/>
							</div>
							{openedOnAll && (
								<p className="text-muted-foreground text-sm" role="note" data-testid="inbox-opened-on-all">
									{f("inbox.openedOnAll", { count: total })}
								</p>
							)}
							{items.length === 0 ? (
								<EmptyNote testId="inbox-empty-filtered">
									<span className="flex flex-wrap items-center gap-3">
										<span>
											{narrowed
												? t("inbox.empty.filteredSource")
												: f("inbox.emptyState", {
														state: t(`inbox.state.${filter}`),
													})}
										</span>
										{(filter !== INBOX_ALL || narrowed) && total > 0 && (
											<Button
												size="sm"
												variant="outline"
												onClick={() => {
													onFilter(INBOX_ALL);
													onSource(null);
													setSince("");
													onQ(null);
												}}
												data-testid="inbox-show-all"
											>
												{f("inbox.showAll", { count: total })}
											</Button>
										)}
									</span>
								</EmptyNote>
							) : (
								<div className="min-w-0 overflow-x-auto rounded-sm border">
									<InboxTable items={items} selectedId={itemId} onSelect={(item) => onItem(item.id)} compact={Boolean(panel)} />
								</div>
							)}
						</div>
					</Section>
					{panel ?? (items.length > 0 ? <EmptyNote testId="inbox-select-item">{t("inbox.detail.select")}</EmptyNote> : null)}
				</div>
			)}
			{showSources && (
				<Section title={t("inbox.sources.title")} description={t("inbox.sources.description")} testId="inbox-sources">
					<InboxSourcesTable sources={sources} manage={manageSources} onChanged={onSourcesChanged} blocked={blockedSources} />
					{manageSources && sources.length > 0 && <InboxConnectAnotherLink />}
				</Section>
			)}
		</div>
	);
}

/** "5 new test tasks made from the inbox", a link to the benchmark that holds them when one is known (M7-15). */
function RegressionTasksCount({ count, suite }: { count: number; suite: string | null }) {
	const { t } = useTranslation("bench");
	const text = t("inbox.regressionTasks", { count });
	if (count === 0 || !suite) {
		return (
			<span className="text-muted-foreground text-sm" data-testid="inbox-regression-tasks">
				{text}
			</span>
		);
	}
	return (
		<Link
			to="/suites/$suiteId"
			params={{ suiteId: suite }}
			className="text-primary text-sm font-medium underline underline-offset-2"
			data-testid="inbox-regression-tasks"
		>
			{text}
		</Link>
	);
}

/** "Seen in": the last day, week or month, read against each item's last sighting (M7-12). */
function SinceFilter({ value, onChange }: { value: InboxSince; onChange: (next: InboxSince) => void }) {
	const { t } = useTranslation("uf7a");
	const id = useId();
	return (
		<div className="flex items-center gap-2">
			<label htmlFor={id} className="text-muted-foreground text-sm">
				{t("inbox.since")}
			</label>
			<select
				id={id}
				className="border-input bg-card dark:bg-input/20 h-8 rounded-md border px-2 text-sm shadow-xs outline-none"
				value={value}
				onChange={(e) => onChange(e.target.value as InboxSince)}
				data-testid="inbox-since-filter"
			>
				{INBOX_SINCE.map((s) => (
					<option key={s} value={s}>
						{t(`inbox.sinceOptions.${s || "any"}`)}
					</option>
				))}
			</select>
		</div>
	);
}

function SourceFilter({
	names,
	value,
	onChange,
}: {
	names: readonly string[];
	value: string | null;
	onChange: (source: string | null) => void;
}) {
	const { t } = useTranslation("bench");
	const id = useId();
	// A source in the URL that the server no longer lists stays selectable, so the filter never lies.
	const options = value && !names.includes(value) ? [...names, value] : names;
	return (
		<div className="flex items-center gap-2">
			<label htmlFor={id} className="text-muted-foreground text-sm">
				{t("inbox.filters.source")}
			</label>
			<select
				id={id}
				className="border-input bg-card dark:bg-input/20 h-8 min-w-40 rounded-md border px-2 text-sm shadow-xs outline-none"
				value={value ?? ""}
				onChange={(e) => onChange(e.target.value || null)}
				data-testid="inbox-source-filter"
			>
				<option value="">{t("inbox.filters.allSources")}</option>
				{options.map((name) => (
					<option key={name} value={name}>
						{name}
					</option>
				))}
			</select>
		</div>
	);
}