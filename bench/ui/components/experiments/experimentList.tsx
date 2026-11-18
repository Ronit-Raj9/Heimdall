import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { EntityLink } from "@/components/shell/entityLink";
import { StatusBadge } from "@/components/shell/statusBadge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ValueCell } from "@/components/valueCell";
import { formatDateTime, sortExperimentsNewestFirst } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import { useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import type { ExperimentState, ExperimentStatus } from "@/lib/types/bench";
import { FlaskConical, Search } from "lucide-react";
import { parseAsString, useQueryStates } from "nuqs";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

const ALL = "__all";

const STATES: readonly ExperimentState[] = ["running", "analyzing", "created", "done", "failed", "cancelled"];

/** The filters of the list, as they sit in the URL. */
export interface ExperimentFilters {
	q?: string | null;
	state?: string | null;
	suite?: string | null;
}

/** Experiments that pass the filters, newest first. q matches the id or the name, ignoring case. Pure. */
export function filterExperiments(experiments: readonly ExperimentStatus[], filters: ExperimentFilters): ExperimentStatus[] {
	const q = filters.q?.trim().toLowerCase() ?? "";
	return sortExperimentsNewestFirst([...experiments]).filter(
		(e) =>
			(!q || e.exp_id.toLowerCase().includes(q) || e.name.toLowerCase().includes(q)) &&
			(!filters.state || e.state === filters.state) &&
			(!filters.suite || e.suite === filters.suite),
	);
}

/**
 * Every experiment on the server (GET /experiments): id, name, suite, arms, status, verdict,
 * started and spend. The search and the two menus live in the URL.
 */
export function ExperimentList() {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const list = useListExperimentsQuery();
	const [filters, setFilters] = useQueryStates(
		{ q: parseAsString, state: parseAsString, suite: parseAsString },
		{ history: "replace", throttleMs: 200 },
	);
	const all = useMemo(() => list.data?.experiments ?? [], [list.data]);
	const shown = useMemo(() => filterExperiments(all, filters), [all, filters]);
	const suites = useMemo(() => [...new Set(all.map((e) => e.suite).filter((s): s is string => Boolean(s)))].sort(), [all]);
	const active = Boolean(filters.q || filters.state || filters.suite);
	const clear = () => void setFilters({ q: null, state: null, suite: null });

	if (list.isLoading) return <BlockSkeleton rows={6} testId="experiment-list-loading" />;
	if (list.error || !list.data) {
		return (
			<LoadError
				title={t("experiments.list.loadError")}
				error={list.error}
				onRetry={() => void list.refetch()}
				testId="experiment-list-error"
			/>
		);
	}
	if (all.length === 0) {
		return (
			<EmptyState
				icon={FlaskConical}
				message={t("experiments.list.empty")}
				action={{ label: t("experiments.list.getStarted"), to: "/quickstart" }}
				testId="experiment-list-empty"
			/>
		);
	}

	return (
		<div className="flex min-w-0 flex-col gap-4" data-testid="experiment-list">
			<div className="flex flex-wrap items-end gap-2" role="search" aria-label={t("experiments.list.filtersLabel")}>
				<div className="flex min-w-0 flex-col gap-1">
					<label htmlFor="experiment-search" className="text-muted-foreground text-xs">
						{t("experiments.list.search")}
					</label>
					<div className="relative">
						<Search
							className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2"
							aria-hidden="true"
						/>
						<Input
							id="experiment-search"
							type="search"
							value={filters.q ?? ""}
							onChange={(e) => void setFilters({ q: e.target.value || null })}
							placeholder={t("experiments.list.searchPlaceholder")}
							className="h-8 w-64 max-w-full pl-8 text-sm"
							data-testid="experiment-search"
						/>
					</div>
				</div>
				<MenuFilter
					id="experiment-filter-state"
					label={t("experiments.list.col.status")}
					value={filters.state}
					options={STATES.map((s) => ({ value: s, label: t(`shell.status.state.${s}`) }))}
					onChange={(state) => void setFilters({ state })}
				/>
				{suites.length > 1 && (
					<MenuFilter
						id="experiment-filter-suite"
						label={t("experiments.list.col.suite")}
						value={filters.suite}
						options={suites.map((s) => ({ value: s, label: s }))}
						onChange={(suite) => void setFilters({ suite })}
						mono
					/>
				)}
				{active && (
					<Button variant="ghost" size="sm" onClick={clear} dataTestId="experiment-list-clear">
						{t("runs.list.clear")}
					</Button>
				)}
			</div>

			{shown.length === 0 ? (
				<EmptyState
					icon={Search}
					message={t("experiments.list.noMatch")}
					action={{ label: t("runs.list.clear"), onClick: clear }}
					compact
					testId="experiment-list-no-match"
				/>
			) : (
				<>
					<ExperimentCards experiments={shown} />
					<Card className="hidden py-0 shadow-none md:flex">
						<Table stickyColumns={1} data-testid="experiment-table" aria-label={t("experiments.list.tableLabel")}>
							<TableHeader>
								<TableRow>
									<TableHead scope="col">{t("experiments.list.col.experiment")}</TableHead>
									<TableHead scope="col">{t("experiments.list.col.suite")}</TableHead>
									<TableHead scope="col">{t("experiments.list.col.arms")}</TableHead>
									<TableHead scope="col">{t("experiments.list.col.status")}</TableHead>
									<TableHead scope="col">{t("experiments.list.col.verdict")}</TableHead>
									<TableHead scope="col">{t("experiments.list.col.started")}</TableHead>
									<TableHead scope="col" className="text-right">
										{t("experiments.list.col.cost")}
									</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{shown.map((e) => (
									<TableRow key={e.exp_id} data-testid={`experiment-row-${e.exp_id}`}>
										<TableCell className="max-w-80 whitespace-normal">
											<span className="flex min-w-0 flex-col gap-0.5">
												<EntityLink
													kind="experiment"
													id={e.exp_id}
													className="text-foreground font-sans text-sm font-medium"
													testId={`experiment-row-${e.exp_id}-name`}
												>
													{e.name || e.exp_id}
												</EntityLink>
												<span className="text-muted-foreground font-mono text-[11px] break-all">{e.exp_id}</span>
											</span>
										</TableCell>
										<TableCell className="text-xs">{e.suite ? <EntityLink kind="suite" id={e.suite} /> : null}</TableCell>
										<TableCell className="font-mono text-xs">{e.arms?.join(" ") ?? ""}</TableCell>
										<TableCell>
											<span className="flex flex-col items-start gap-0.5">
												<StatusBadge state={e.state} testId={`experiment-row-${e.exp_id}-state`} />
												{(e.state === "running" || e.state === "analyzing") && (
													<span className="text-muted-foreground text-[11px] tabular-nums">
														{t("experiments.list.progress", { done: e.slots.done + e.slots.failed, total: e.slots.total })}
													</span>
												)}
											</span>
										</TableCell>
										<TableCell>
											{e.decision ? (
												<StatusBadge decision={e.decision} testId={`experiment-row-${e.exp_id}-decision`} />
											) : (
												<span className="text-muted-foreground text-xs">{t("experiments.list.noVerdict")}</span>
											)}
										</TableCell>
										<TableCell className="text-muted-foreground text-xs whitespace-nowrap tabular-nums">
											{formatDateTime(e.started_at ?? e.frozen_at, locale) ?? ""}
										</TableCell>
										<TableCell className="text-right">
											<ValueCell value={e.spent_usd} kind="usd" />
										</TableCell>
									</TableRow>
								))}
							</TableBody>
						</Table>
					</Card>
				</>
			)}
			<p className="text-muted-foreground text-xs tabular-nums" data-testid="experiment-list-count">
				{t("experiments.list.count", { shown: shown.length, total: all.length })}
			</p>
		</div>
	);
}

/** The experiment list on a phone: one card per experiment, in place of the wide table. */
function ExperimentCards({ experiments }: { experiments: readonly ExperimentStatus[] }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	return (
		<ul className="flex flex-col gap-2 md:hidden" data-testid="experiment-cards" aria-label={t("experiments.list.tableLabel")}>
			{experiments.map((e) => (
				<li
					key={e.exp_id}
					className="bg-card flex flex-col gap-1.5 rounded-lg border px-3 py-2.5"
					data-testid={`experiment-card-${e.exp_id}`}
				>
					<div className="flex items-start justify-between gap-2">
						<span className="flex min-w-0 flex-col">
							<EntityLink kind="experiment" id={e.exp_id} className="text-foreground font-sans text-sm font-medium">
								{e.name || e.exp_id}
							</EntityLink>
							<span className="text-muted-foreground font-mono text-[11px] break-all">{e.exp_id}</span>
						</span>
						{e.decision ? <StatusBadge decision={e.decision} /> : <StatusBadge state={e.state} />}
					</div>
					<div className="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
						{e.suite && <EntityLink kind="suite" id={e.suite} />}
						{e.arms && <span className="font-mono">{e.arms.join(" ")}</span>}
						<span className="tabular-nums">{formatDateTime(e.started_at ?? e.frozen_at, locale) ?? ""}</span>
						<span className="ml-auto">
							<ValueCell value={e.spent_usd} kind="usd" />
						</span>
					</div>
				</li>
			))}
		</ul>
	);
}

function MenuFilter({
	id,
	label,
	value,
	options,
	onChange,
	mono,
}: {
	id: string;
	label: string;
	value: string | null | undefined;
	options: readonly { value: string; label: string }[];
	onChange: (value: string | null) => void;
	mono?: boolean;
}) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<label htmlFor={id} className="text-muted-foreground text-xs">
				{label}
			</label>
			<Select value={value ?? ALL} onValueChange={(next) => onChange(next === ALL ? null : next)}>
				<SelectTrigger id={id} size="sm" className={mono ? "w-44 font-mono text-xs" : "w-36"} data-testid={id}>
					<SelectValue />
				</SelectTrigger>
				<SelectContent>
					<SelectItem value={ALL}>{t("runs.list.all")}</SelectItem>
					{options.map((option) => (
						<SelectItem key={option.value} value={option.value}>
							{option.label}
						</SelectItem>
					))}
				</SelectContent>
			</Select>
		</div>
	);
}