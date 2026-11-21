import ClustersTab from "@/app/inbox/views/clustersTab";
import { AgentPicker } from "@/components/agents/agentPicker";
import { PatternList } from "@/components/reportcard/reportFailures";
import { pct } from "@/components/reportcard/reportCard.utils";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { PageHeader, RouteTabs, type RouteTab } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getLocale } from "@/lib/i18n";
import { useGetAgentsQuery } from "@/lib/store";
import { patternRate, useGetAgentFailurePatternsQuery, type FailurePatternGroup } from "@/lib/store/apis/failurePatternsApi";
import "@/lib/types/agentFirstI18n";
import { Link } from "@tanstack/react-router";
import { ShieldAlert } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";

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
			<PageHeader title={t("why.title")} subtitle={t("why.subtitle")} />
			<RouteTabs tabs={tabs} defaultTab="patterns" label={t("why.tabs.label")} testId="why-tabs">
				{(active) => (active === "groups" ? <ClustersTab /> : <PatternsTab />)}
			</RouteTabs>
		</section>
	);
}

function PatternsTab() {
	const { t } = useTranslation("agentFirst");
	const locale = getLocale();
	const [agent, setAgent] = useQueryState("agent", parseAsString.withOptions({ history: "replace" }));
	const agents = useGetAgentsQuery();
	const query = useGetAgentFailurePatternsQuery(agent ?? "", { skip: !agent });
	const groups = query.data?.groups ?? [];
	const unsaved = t("why.unsaved");
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="why-patterns">
			<div className="flex flex-wrap items-end gap-3">
				<AgentPicker agents={agents.data?.agents ?? []} value={agent ?? ""} onChange={(id) => void setAgent(id || null)} testId="why-agent" />
				{agent && (
					<Button asChild size="sm" variant="outline" className="ml-auto">
						<Link to={"/failure-tests" as never} search={{ agent } as never} data-testid="why-run-failure-test">
							<ShieldAlert aria-hidden="true" />
							{t("why.runFailureTest")}
						</Link>
					</Button>
				)}
			</div>
			{!agent ? (
				<EmptyNote testId="why-pick">{t("why.pick")}</EmptyNote>
			) : query.isLoading ? (
				<BlockSkeleton rows={6} testId="why-loading" />
			) : query.isError ? (
				<LoadError title={t("why.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="why-error" />
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
