import { outcomeTone } from "@/components/runs/runs.utils";
import { BlockSkeleton } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { apiErrorMessage } from "@/lib/store/apis/integrationsApi";
import { useSearchTracesQuery } from "@/lib/store/apis/traceToolsApi";
import type { SearchHit, SearchMatch, SearchResponse } from "@/lib/types/traceTools";
import { cn } from "@/lib/utils";
import { useNavigate } from "@tanstack/react-router";
import { FileSearch, SearchX } from "lucide-react";
import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { benchmarkParam, hitAgentLabel, splitHighlights } from "./traceMatches.utils";

const PILL = {
	pass: "bg-chart-success/12 text-chart-success-ink",
	zero: "bg-chart-error/12 text-chart-error-ink",
	unknown: "bg-muted text-muted-foreground",
} as const;

const DOT = { pass: "bg-chart-success", zero: "bg-chart-error", unknown: "bg-muted-foreground/60" } as const;

const MAX_SNIPPETS = 3;

/** One match: field label and the snippet with its highlighted spans. */
export function MatchSnippet({ match }: { match: SearchMatch }) {
	const { t } = useTranslation("traceTools");
	const field = t(`search.field.${match.field}` as "search.field.step", { defaultValue: match.field });
	return (
		<li className="flex min-w-0 items-baseline gap-2 text-xs" data-testid="trace-match">
			<span className="text-muted-foreground w-16 shrink-0 font-medium">
				{field}
				{match.seq !== undefined && <span className="ml-1 tabular-nums">{t("search.seq", { seq: match.seq })}</span>}
			</span>
			<span className="text-foreground/90 min-w-0 break-words">
				{match.label && <span className="text-muted-foreground mr-1.5 font-mono">{match.label}</span>}
				{splitHighlights(match.text, match.highlights).map((s, i) =>
					s.mark ? (
						<mark key={i} className="bg-chart-warning/25 text-foreground rounded-sm px-0.5">
							{s.text}
						</mark>
					) : (
						<span key={i}>{s.text}</span>
					),
				)}
			</span>
		</li>
	);
}

export function TraceHitRow({ hit, onOpen }: { hit: SearchHit; onOpen(hit: SearchHit): void }) {
	const { t } = useTranslation("traceTools");
	const { t: tb } = useTranslation("bench");
	const tone = outcomeTone(hit.outcome as Parameters<typeof outcomeTone>[0]);
	const extra = hit.matches.length - MAX_SNIPPETS;
	const onKey = (e: KeyboardEvent) => {
		if (e.key === "Enter" || e.key === " ") {
			e.preventDefault();
			onOpen(hit);
		}
	};
	return (
		<li
			role="link"
			tabIndex={0}
			onClick={() => onOpen(hit)}
			onKeyDown={onKey}
			className="hover:bg-muted/40 focus-visible:ring-ring/50 flex cursor-pointer flex-col gap-2 px-4 py-3 focus-visible:ring-2 focus-visible:outline-none"
			data-testid={`trace-hit-${hit.run_id}`}
		>
			<div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-sm">
				<span className="text-foreground min-w-0 truncate font-medium">{hit.task}</span>
				<span className="text-muted-foreground">{hitAgentLabel(hit)}</span>
				<span className={cn("inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap", PILL[tone])}>
					<span className={cn("size-1.5 rounded-full", DOT[tone])} aria-hidden="true" />
					{tb(`runs.outcome.${hit.outcome}`, { defaultValue: hit.outcome })}
				</span>
				<span className="text-muted-foreground ml-auto text-xs">{hit.benchmark}</span>
			</div>
			{hit.matches.length > 0 && (
				<ul className="flex flex-col gap-1">
					{hit.matches.slice(0, MAX_SNIPPETS).map((m, i) => (
						<MatchSnippet key={i} match={m} />
					))}
					{extra > 0 && <li className="text-muted-foreground text-xs">{t("search.more", { count: extra })}</li>}
				</ul>
			)}
		</li>
	);
}

/** The result list for a trace search response; pure so it renders in tests. */
export function TraceMatchList({ data, onOpen }: { data: SearchResponse; onOpen(hit: SearchHit): void }) {
	const { t } = useTranslation("traceTools");
	return (
		<section className="bg-card flex flex-col rounded-xl border" aria-label={t("search.heading")} data-testid="trace-matches">
			<header className="flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b px-4 py-2.5">
				<h2 className="text-sm font-semibold">{t("search.heading")}</h2>
				<span className="text-muted-foreground text-xs tabular-nums" data-testid="trace-matches-total">
					{t("search.total", { count: data.total })}
					{data.truncated && <> · {t("search.truncated", { count: data.hits.length })}</>}
				</span>
				<span className="text-muted-foreground ml-auto text-xs tabular-nums">{t("search.indexed", { count: data.indexed_runs })}</span>
			</header>
			<p className="text-muted-foreground border-b px-4 py-1.5 text-xs" data-testid="trace-matches-help">
				{t("search.help")}
			</p>
			{data.hits.length === 0 ? (
				<EmptyState compact icon={SearchX} message={t("search.empty")} testId="trace-matches-empty" />
			) : (
				<ul className="divide-border/80 divide-y">
					{data.hits.map((h) => (
						<TraceHitRow key={h.run_id} hit={h} onOpen={onOpen} />
					))}
				</ul>
			)}
		</section>
	);
}

export interface TraceMatchesProps {
	query: string;
	agent?: string | null;
	outcome?: string | null;
	suite?: string | null;
}

/** "Trace matches" panel shown above the runs table while a trace search is set. */
export function TraceMatches({ query, agent, outcome, suite }: TraceMatchesProps) {
	const { t } = useTranslation("traceTools");
	const navigate = useNavigate();
	const res = useSearchTracesQuery({
		q: query,
		agent: agent || undefined,
		outcome: outcome || undefined,
		benchmark: benchmarkParam(suite),
		limit: 50,
	});
	if (res.isLoading || (res.isFetching && !res.data)) return <BlockSkeleton rows={3} testId="trace-matches-loading" />;
	if (res.error) {
		return (
			<p className="text-chart-error-ink rounded-xl border px-4 py-3 text-sm" role="alert" data-testid="trace-matches-error">
				{apiErrorMessage(res.error, t("search.error"))}
			</p>
		);
	}
	if (!res.data) return null;
	return (
		<div className={cn(res.isFetching && "opacity-70 transition-opacity")}>
			<TraceMatchList data={res.data} onOpen={(h) => void navigate({ to: "/runs/$runId", params: { runId: h.run_id } } as never)} />
		</div>
	);
}

/**
 * Second search box on /runs: searches inside every run's trace on the server. Typing is
 * debounced (300 ms) before the URL param changes, so each keystroke is not a request.
 */
export function TraceSearchInput({ value, onChange, delay = 300 }: { value: string; onChange(next: string): void; delay?: number }) {
	const { t } = useTranslation("traceTools");
	const [draft, setDraft] = useState(value);
	const last = useRef(value);
	useEffect(() => {
		// The URL changed from elsewhere (clear, back button): follow it.
		if (value !== last.current) {
			last.current = value;
			setDraft(value);
		}
	}, [value]);
	useEffect(() => {
		if (draft === last.current) return;
		const id = setTimeout(() => {
			last.current = draft;
			onChange(draft);
		}, delay);
		return () => clearTimeout(id);
	}, [draft, delay, onChange]);
	return (
		<label className="relative min-w-48 flex-1 sm:max-w-sm" title={t("search.help")}>
			<span className="sr-only">{t("search.label")}</span>
			<FileSearch className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2" aria-hidden="true" />
			<input
				type="search"
				value={draft}
				onChange={(e) => setDraft(e.target.value)}
				placeholder={t("search.placeholder")}
				aria-label={t("search.label")}
				aria-describedby="trace-search-help"
				data-testid="runs-trace-search"
				className="border-input bg-card placeholder:text-muted-foreground focus-visible:ring-ring/40 h-8 w-full rounded-md border pr-2.5 pl-8 text-sm shadow-xs focus-visible:ring-2 focus-visible:outline-none"
			/>
			<span id="trace-search-help" className="sr-only">
				{t("search.help")}
			</span>
		</label>
	);
}
