import ClustersTab from "@/app/inbox/views/clustersTab";
import { AgentPicker } from "@/components/agents/agentPicker";
import { PatternList } from "@/components/reportcard/reportFailures";
import { pct } from "@/components/reportcard/reportCard.utils";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { PageHeader, RouteTabs, type RouteTab } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getLocale } from "@/lib/i18n";
import { useGetAgentsQuery } from "@/lib/store";
import {
	patternRate,
	useGetAgentFailurePatternsQuery,
	type AgentFailurePatternsArgs,
	type FailurePatternGroup,
} from "@/lib/store/apis/failurePatternsApi";
import "@/lib/types/agentFirstI18n";
import { Link } from "@tanstack/react-router";
import { ShieldAlert } from "lucide-react";
import { parseAsBoolean, parseAsInteger, parseAsString, useQueryState } from "nuqs";
import { useId, useMemo } from "react";
import { useTranslation } from "react-i18next";

/** The value of a filter select that means "every one". Never a version or a benchmark id. */
export const ALL_FILTER = "~all";

/** The failure patterns request of the page's URL state: filters only when set. */
export function whyFilterArgs(
	agent: string,
	version: number | null,
	benchmark: string | null,
	practice: boolean | null,
): AgentFailurePatternsArgs {
	return {
		agent,
		...(version && version > 0 ? { version } : {}),
		...(benchmark ? { benchmark } : {}),
		...(practice ? { includePractice: true } : {}),
	};
}

/** Filter choices from the server, with a URL value the list lacks kept so the select still shows it. */
export function withSelected<T>(options: readonly T[] | undefined, selected: T | null | undefined): T[] {
	const list = [...(options ?? [])];
	if (selected !== null && selected !== undefined && !list.includes(selected)) list.unshift(selected);
	return list;
}

export interface WhyFiltersProps {
	version: number | null;
	versions: readonly number[];
	benchmark: string | null;
	benchmarks: readonly string[];
	practice: boolean;
	onVersion: (v: number | null) => void;
	onBenchmark: (b: string | null) => void;
	onPractice: (on: boolean) => void;
	disabled?: boolean;
}

/** Version and benchmark selects and the "Include practice runs" box of the failure patterns tab. */
export function WhyFilters(props: WhyFiltersProps) {
	const { t } = useTranslation("agentBoard");
	const versionId = useId();
	const benchId = useId();
	return (
		<div className="flex flex-wrap items-center gap-x-4 gap-y-2" data-testid="why-filters">
			<div className="flex items-center gap-2">
				<label htmlFor={versionId} className="text-muted-foreground text-xs">
					{t("why.version")}
				</label>
				<Select
					value={props.version ? String(props.version) : ALL_FILTER}
					onValueChange={(v) => props.onVersion(v === ALL_FILTER ? null : Number(v))}
					disabled={props.disabled}
				>
					<SelectTrigger id={versionId} size="sm" className="min-w-24 text-xs" data-testid="why-version">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value={ALL_FILTER} className="text-xs">
							{t("why.all")}
						</SelectItem>
						{props.versions.map((v) => (
							<SelectItem key={v} value={String(v)} className="text-xs" data-testid={`why-version-option-${v}`}>
								{`v${v}`}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
			</div>
			<div className="flex items-center gap-2">
				<label htmlFor={benchId} className="text-muted-foreground text-xs">
					{t("why.benchmark")}
				</label>
				<Select
					value={props.benchmark ?? ALL_FILTER}
					onValueChange={(v) => props.onBenchmark(v === ALL_FILTER ? null : v)}
					disabled={props.disabled}
				>
					<SelectTrigger id={benchId} size="sm" className="min-w-32 font-mono text-xs" data-testid="why-benchmark">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value={ALL_FILTER} className="text-xs">
							{t("why.all")}
						</SelectItem>
						{props.benchmarks.map((b) => (
							<SelectItem key={b} value={b} className="font-mono text-xs" data-testid={`why-benchmark-option-${b}`}>
								{b}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
			</div>
			<label className="inline-flex items-center gap-2 text-xs" title={t("why.includePracticeHint")}>
				<input
					type="checkbox"
					checked={props.practice}
					onChange={(e) => props.onPractice(e.target.checked)}
					disabled={props.disabled}
					data-testid="why-include-practice"
				/>
				{t("why.includePractice")}
			</label>
		</div>
	);
}

/** The pattern ids every group carries, in the server's order. */
export function patternIds(groups: readonly FailurePatternGroup[]): { pattern: string; label: string }[] {
	const seen = new Map<string, string>();
	for (const g of groups) for (const p of g.patterns) if (!seen.has(p.pattern)) seen.set(p.pattern, p.label);
	return [...seen].map(([pattern, label]) => ({ pattern, label }));
}

/** A group's display name: "v3", or "Unsaved config" for a config never saved as a version. */
export function groupName(g: Pick<FailurePatternGroup, "version" | "label">, unsaved: string): string {
	return g.version > 0 ? `v${g.version}` : `${unsaved} ${g.label.replace(/^unsaved config /, "")}`;
}

/** The page header link to the failures pulled from Maxim (/maxim), which no menu leads to (N77). */
export function MaximFailuresLink() {
	const { t } = useTranslation("gw06");
	return (
		<Button variant="outline" size="sm" asChild>
			<Link to="/maxim" data-testid="why-maxim-link">
				{t("why.maxim")}
			</Link>
		</Button>
	);
}

/**
 * Why agents fail (/why-agents-fail?agent=&tab=): per agent version, how often it loops, claims
 * done without checking, or ignores an error, with example runs; and the failure groups across
 * every benchmark run (the clusters view).
 */
export default function WhyAgentsFailView() {
	const { t } = useTranslation("agentFirst");
	const tabs: RouteTab[] = [
		{ id: "patterns", label: t("why.tabs.patterns") },
		{ id: "groups", label: t("why.tabs.groups") },
	];
	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-6" data-testid="why-agents-fail-screen">
			<PageHeader title={t("why.title")} subtitle={t("why.subtitle")} actions={<MaximFailuresLink />} />
			<RouteTabs tabs={tabs} defaultTab="patterns" label={t("why.tabs.label")} testId="why-tabs">
				{(active) => (active === "groups" ? <ClustersTab /> : <PatternsTab />)}
			</RouteTabs>
		</section>
	);
}

function PatternsTab() {
	const { t } = useTranslation("agentFirst");
	const locale = getLocale();
	const { t: tb } = useTranslation("agentBoard");
	const [agent, setAgent] = useQueryState("agent", parseAsString.withOptions({ history: "replace" }));
	const [version, setVersion] = useQueryState("version", parseAsInteger.withOptions({ history: "replace" }));
	const [benchmark, setBenchmark] = useQueryState("benchmark", parseAsString.withOptions({ history: "replace" }));
	const [practice, setPractice] = useQueryState("practice", parseAsBoolean.withOptions({ history: "replace" }));
	const agents = useGetAgentsQuery();
	const args = useMemo(() => whyFilterArgs(agent ?? "", version, benchmark, practice), [agent, version, benchmark, practice]);
	const query = useGetAgentFailurePatternsQuery(args, { skip: !agent });
	const groups = query.data?.groups ?? [];
	const unsaved = t("why.unsaved");
	const filtered = Boolean(args.version || args.benchmark);
	const clearFilters = () => {
		void setVersion(null);
		void setBenchmark(null);
	};
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="why-patterns">
			<div className="flex flex-wrap items-end gap-3">
				<AgentPicker
					agents={agents.data?.agents ?? []}
					value={agent ?? ""}
					onChange={(id) => void setAgent(id || null)}
					testId="why-agent"
				/>
				{agent && (
					<Button asChild size="sm" variant="outline" className="ml-auto">
						<Link to={"/failure-tests" as never} search={{ agent } as never} data-testid="why-run-failure-test">
							<ShieldAlert aria-hidden="true" />
							{t("why.runFailureTest")}
						</Link>
					</Button>
				)}
			</div>
			{agent && (
				<WhyFilters
					version={version}
					versions={withSelected(query.data?.versions, version && version > 0 ? version : null)}
					benchmark={benchmark}
					benchmarks={withSelected(query.data?.benchmarks, benchmark || null)}
					practice={Boolean(practice)}
					onVersion={(v) => void setVersion(v)}
					onBenchmark={(b) => void setBenchmark(b)}
					onPractice={(on) => void setPractice(on ? true : null)}
					disabled={query.isLoading}
				/>
			)}
			{!agent ? (
				<EmptyNote testId="why-pick">{t("why.pick")}</EmptyNote>
			) : query.isLoading ? (
				<BlockSkeleton rows={6} testId="why-loading" />
			) : query.isError ? (
				<LoadError title={t("why.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="why-error" />
			) : groups.length === 0 && filtered ? (
				<EmptyNote testId="why-empty-filtered">
					<span className="flex flex-col items-center gap-3">
						<span>{tb("why.emptyFiltered", { agent })}</span>
						<Button size="sm" variant="outline" onClick={clearFilters} dataTestId="why-clear-filters">
							{tb("why.clearFilters")}
						</Button>
					</span>
				</EmptyNote>
			) : groups.length === 0 ? (
				<EmptyNote testId="why-empty">
					<span className="flex flex-col items-center gap-3">
						<span>{t("why.empty", { agent })}</span>
						<Button asChild size="sm">
							<Link to={"/evaluate" as never} search={{ agent } as never}>
								{t("why.evaluate")}
							</Link>
						</Button>
					</span>
				</EmptyNote>
			) : (
				<>
					{groups.length > 1 && (
						<div className="min-w-0 overflow-x-auto rounded-lg border">
							<Table data-testid="why-compare">
								<TableHeader>
									<TableRow>
										<TableHead>{t("why.pattern")}</TableHead>
										{groups.map((g) => (
											<TableHead key={g.key} className="text-right">
												{groupName(g, unsaved)}
											</TableHead>
										))}
									</TableRow>
								</TableHeader>
								<TableBody>
									{patternIds(groups).map(({ pattern, label }) => (
										<TableRow key={pattern}>
											<TableCell className="text-sm">{t(`failures.pattern.${pattern}`, { defaultValue: label })}</TableCell>
											{groups.map((g) => {
												const p = g.patterns.find((x) => x.pattern === pattern);
												return (
													<TableCell key={g.key} className="text-right text-sm tabular-nums">
														{p ? pct(patternRate(p)) : "-"}
													</TableCell>
												);
											})}
										</TableRow>
									))}
								</TableBody>
							</Table>
						</div>
					)}
					{groups.map((g) => (
						<section key={g.key} className="flex min-w-0 flex-col gap-3" data-testid={`why-group-${g.key}`}>
							<div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
								<h2 className="text-base font-semibold tracking-tight">
									{agent} {groupName(g, unsaved)}
								</h2>
								<span className="text-muted-foreground text-xs">
									{t("why.groupMeta", {
										runs: g.runs,
										failed: g.failed,
										models: g.models.join(", ") || "-",
										when: g.last_at ? new Date(g.last_at).toLocaleDateString(locale, { dateStyle: "medium" }) : "-",
									})}
								</span>
							</div>
							<PatternList group={g} testId={`why-patterns-${g.key}`} />
						</section>
					))}
				</>
			)}
		</div>
	);
}