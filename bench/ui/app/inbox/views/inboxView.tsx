import { INBOX_ALL, INBOX_FILTERS, filterInboxItems, inboxCount, type InboxFilter } from "@/components/inbox/inbox.utils";
import { InboxEmpty, InboxIngestOffNote, InboxSourcesTable } from "@/components/inbox/inboxSources";
import { InboxStateFilter } from "@/components/inbox/inboxStateFilter";
import { InboxTable } from "@/components/inbox/inboxTable";
import { ModeBadge } from "@/components/modeBadge";
import { PageHeader, RouteTabs, type RouteTab } from "@/components/shell";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { useGetInboxQuery } from "@/lib/store/apis/inboxApi";
import type { InboxListResponse } from "@/lib/types/bench";
import { parseAsString, parseAsStringLiteral, useQueryState } from "nuqs";
import { useId, useMemo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import ClustersTab from "./clustersTab";
import { InboxPullButton, InboxPullSetup, InboxWebhook } from "./inboxConnect";
import { useGetPullReadinessQuery } from "@/lib/store/apis/liveinboxApi";
import { InboxItemPanel } from "./inboxItemPanel";
import TriageTab from "./triageTab";

/** The state the screen opens on: what still needs a decision. */
const DEFAULT_FILTER: InboxFilter = "new";

/**
 * Failure inbox: /inbox?tab=. Inbox (default), Clusters (the old /clusters) and Triage (the old
 * /triage): production failures, failed benchmark runs grouped by signature, and failure patterns
 * per arm of one experiment.
 */
export default function InboxView() {
	const { t } = useTranslation("bench");
	const tabs: RouteTab[] = [
		{ id: "inbox", label: t("inbox.tabs.inbox") },
		{ id: "clusters", label: t("inbox.tabs.clusters") },
		{ id: "triage", label: t("inbox.tabs.triage") },
	];
	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="inbox-page">
			<PageHeader title={t("inbox.title")} subtitle={t("inbox.pageSubtitle")} />
			<RouteTabs tabs={tabs} defaultTab="inbox" label={t("inbox.tabs.label")} testId="inbox-tabs">
				{(active) => (active === "clusters" ? <ClustersTab /> : active === "triage" ? <TriageTab /> : <InboxTab />)}
			</RouteTabs>
		</section>
	);
}

/**
 * The Inbox tab: ?state=&source=&item=. Production failures pulled from trace sources,
 * one row per signature. state and source go to the server; item opens one item beside the table,
 * where it can be accepted into a suite as a draft regression task, rejected or snoozed
 * (docs/failure-inbox.md).
 */
export function InboxTab() {
	const { t } = useTranslation("bench");
	const [filter, setFilter] = useQueryState("state", parseAsStringLiteral(INBOX_FILTERS).withDefault(DEFAULT_FILTER));
	const [source, setSource] = useQueryState("source", parseAsString);
	const [itemId, setItemId] = useQueryState("item", parseAsString);
	const list = useGetInboxQuery({ state: filter === INBOX_ALL ? undefined : filter, source: source ?? undefined });
	const data = list.data;
	const ready = useGetPullReadinessQuery().data;

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="inbox-screen">
			<div className="flex flex-wrap items-center gap-3">
				<p className="text-muted-foreground max-w-3xl text-sm">{t("inbox.subtitle")}</p>
				<ModeBadge mode={data?.mode} isLoading={list.isLoading} isError={list.isError && !data} />
				{data?.mode === "live" && <InboxPullButton check={ready?.inbox} />}
			</div>
			{list.isLoading ? (
				<BlockSkeleton rows={6} testId="inbox-loading" />
			) : list.error || !data ? (
				<LoadError title={t("inbox.loadError")} error={list.error} onRetry={() => void list.refetch()} testId="inbox-error" />
			) : (
				<InboxBody
					data={data}
					filter={filter}
					onFilter={(next) => void setFilter(next === DEFAULT_FILTER ? null : next)}
					source={source}
					onSource={(next) => void setSource(next)}
					itemId={itemId}
					onItem={(next) => void setItemId(next)}
					panel={
						itemId ? <InboxItemPanel key={itemId} itemId={itemId} suites={data.suites ?? []} onClose={() => void setItemId(null)} /> : null
					}
				/>
			)}
			{data?.mode === "live" && <InboxPullSetup check={ready?.inbox} hosted={ready?.hosted} />}
			{data?.mode === "live" && <InboxWebhook />}
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
}

/** The loaded inbox: filters, the table or the empty state, the open item and the sources. Exported for tests. */
export function InboxBody({ data, filter, onFilter, source, onSource, itemId, onItem, panel }: InboxBodyProps) {
	const { t } = useTranslation("bench");
	const items = useMemo(() => filterInboxItems(data.items, filter, source), [data.items, filter, source]);
	const sources = data.sources ?? [];
	const total = inboxCount(data.counts, INBOX_ALL);
	const empty = total === 0 && (data.items ?? []).length === 0;

	return (
		<div className="flex min-w-0 flex-col gap-6">
			{!data.ingest_enabled && <InboxIngestOffNote />}
			{empty ? (
				<InboxEmpty />
			) : (
				<div className="grid min-w-0 grid-cols-1 items-start gap-6 2xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
					<Section
						title={t("inbox.table.title")}
						description={t("inbox.table.description")}
						testId="inbox-items"
						actions={
							<div className="flex flex-wrap items-center gap-3">
								<span className="text-muted-foreground text-xs" data-testid="inbox-regression-tasks">
									{t("inbox.regressionTasks", { count: data.regression_tasks ?? 0 })}
								</span>
								<ModeBadge mode={data.mode} />
							</div>
						}
					>
						<div className="flex flex-col gap-4">
							<div className="flex flex-wrap items-end justify-between gap-3">
								<InboxStateFilter counts={data.counts} value={filter} onChange={onFilter} />
								<SourceFilter names={sources.map((s) => s.name)} value={source} onChange={onSource} />
							</div>
							{items.length === 0 ? (
								<EmptyNote testId="inbox-empty-filtered">{source ? t("inbox.empty.filteredSource") : t("inbox.empty.filtered")}</EmptyNote>
							) : (
								<div className="min-w-0 overflow-x-auto rounded-sm border">
									<InboxTable items={items} selectedId={itemId} onSelect={(item) => onItem(item.id)} />
								</div>
							)}
						</div>
					</Section>
					{panel ?? <EmptyNote testId="inbox-select-item">{t("inbox.detail.select")}</EmptyNote>}
				</div>
			)}
			<Section title={t("inbox.sources.title")} description={t("inbox.sources.description")} testId="inbox-sources">
				<InboxSourcesTable sources={sources} />
			</Section>
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
			<label htmlFor={id} className="text-muted-foreground text-xs">
				{t("inbox.filters.source")}
			</label>
			<select
				id={id}
				className="border-input bg-card dark:bg-input/20 h-8 min-w-40 rounded-md border px-2 text-xs shadow-xs outline-none"
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