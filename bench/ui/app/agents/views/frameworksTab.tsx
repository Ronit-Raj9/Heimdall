import { frameworkRows, type FrameworkRow } from "@/components/agents/frameworks.utils";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState, EntityLink } from "@/components/shell";
import { ToneBadge } from "@/components/suites/suiteBadges";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { Bot, Check, Minus } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import "@/components/home/uf2aI18n";
import { splitAgents, type AgentsQuery } from "./agentConfigsTab";

/**
 * Frameworks tab: every config side by side by how it is built and what evidence its runs carry,
 * read from the configs themselves. Pinned versions and conformance results stay in
 * docs/harness-matrix.md, which is written from runs, not from configuration.
 */
export function FrameworksTab({ query }: { query: AgentsQuery }) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("agentsUx");
	const { t: tf } = useTranslation("uf2a");
	const [showTests, setShowTests] = useState(false);
	// The same test agents the Configs tab hides are hidden here too, until "Show them".
	const tests = useMemo(() => splitAgents(query.data?.agents ?? []).tests, [query.data]);
	const rows = useMemo(() => {
		const agents = query.data?.agents ?? [];
		return frameworkRows(showTests ? agents : agents.filter((agent) => !tests.includes(agent)));
	}, [query.data, showTests, tests]);
	if (query.isLoading) return <BlockSkeleton rows={6} testId="frameworks-loading" />;
	if (query.isError || !query.data) {
		return <LoadError title={t("agents.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="frameworks-error" />;
	}
	const hiddenTests = tests.filter((agent) => !agent.error && agent.id).length;
	const testsToggle =
		hiddenTests > 0 ? (
			<span className="text-muted-foreground inline-flex items-center gap-2 text-xs" data-testid="frameworks-tests-hidden">
				{showTests ? null : tu("list.testsHidden", { count: hiddenTests })}
				<Button
					type="button"
					size="sm"
					variant="ghost"
					className="h-6 px-2 text-xs"
					onClick={() => setShowTests((v) => !v)}
					data-testid="frameworks-tests-toggle"
				>
					{showTests ? tu("list.hideTests") : tu("list.showTests")}
				</Button>
			</span>
		) : null;
	if (rows.length === 0 && !testsToggle)
		return (
			<EmptyState
				icon={Bot}
				title={tf("agents.emptyTitle")}
				body={tf("agents.emptyBody")}
				action={{ label: tf("agents.fromTemplate"), to: "/agents/new" }}
				secondary={{ label: tf("agents.connect"), to: "/connect" }}
				testId="frameworks-empty"
			/>
		);
	const native = rows.filter((row) => row.nativeTrace).length;
	const scripted = rows.filter((row) => row.scripted).length;
	// "Reports steps" is hidden while no config reports its own steps (rule 5: no column empty in every row).
	const anySelf = rows.some((row) => row.selfReported);
	return (
		<div className="flex min-w-0 flex-col gap-4" data-testid="frameworks-tab">
			<div className="flex flex-wrap items-center gap-2 text-xs" data-testid="frameworks-summary">
				<ToneBadge tone="neutral">{t("agents.frameworks.configs", { count: rows.length })}</ToneBadge>
				<ToneBadge tone="good">{t("agents.frameworks.nativeCount", { count: native })}</ToneBadge>
				{scripted > 0 && <ToneBadge tone="warn">{t("agents.frameworks.scriptedCount", { count: scripted })}</ToneBadge>}
				{testsToggle}
			</div>
			<div className="bg-card min-w-0 overflow-hidden rounded-xl border">
				<Table aria-label={t("agents.frameworks.label")} stickyColumns={1} data-testid="frameworks-table">
					<TableHeader>
						<TableRow>
							<TableHead>{t("agents.table.config")}</TableHead>
							<TableHead>{t("agents.table.adapter")}</TableHead>
							<TableHead>{t("agents.frameworks.nativeTrace")}</TableHead>
							{anySelf && <TableHead>{t("agents.frameworks.selfReported")}</TableHead>}
							<TableHead>{t("agents.frameworks.provider")}</TableHead>
							<TableHead>{t("agents.frameworks.build")}</TableHead>
							<TableHead>{t("agents.frameworks.kind")}</TableHead>
							<TableHead className="text-right">{t("agents.table.runs")}</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{rows.map((row) => (
							<FrameworkTableRow key={row.id} row={row} selfCol={anySelf} />
						))}
					</TableBody>
				</Table>
			</div>
			<p className="text-muted-foreground text-xs" role="note" data-testid="frameworks-note">
				{t("agents.frameworks.note")}
			</p>
		</div>
	);
}

function Mark({ on, label }: { on: boolean; label?: string }) {
	const { t } = useTranslation("bench");
	return on ? (
		<span className="text-chart-success-ink inline-flex items-center gap-1">
			<Check className="size-3.5" aria-hidden="true" />
			{label ?? <span className="sr-only">{t("agents.frameworks.yes")}</span>}
		</span>
	) : (
		<span className="text-muted-foreground inline-flex items-center">
			<Minus className="size-3.5" aria-hidden="true" />
			<span className="sr-only">{t("agents.frameworks.no")}</span>
		</span>
	);
}

function FrameworkTableRow({ row, selfCol }: { row: FrameworkRow; selfCol: boolean }) {
	const { t } = useTranslation("bench");
	return (
		<TableRow data-testid={`frameworks-row-${row.id.replace(/[^A-Za-z0-9_-]/g, "-")}`}>
			<TableCell className="font-medium">
				<EntityLink kind="agent" id={row.id} />
			</TableCell>
			<TableCell>{row.adapter}</TableCell>
			<TableCell>
				<Mark on={row.nativeTrace} label={row.parser || undefined} />
			</TableCell>
			{selfCol && (
				<TableCell>
					<Mark on={row.selfReported} />
				</TableCell>
			)}
			<TableCell className="text-xs">
				{row.provider}
				{row.fallbacks > 0 && <span className="text-muted-foreground"> {t("agents.frameworks.fallbacks", { count: row.fallbacks })}</span>}
			</TableCell>
			<TableCell className="text-xs">{t(`agents.frameworks.buildKind.${row.build}`)}</TableCell>
			<TableCell>
				{row.scripted ? (
					<ToneBadge tone="warn">{t("agents.scripted", { kind: row.scripted })}</ToneBadge>
				) : (
					t("agents.frameworks.agentKind")
				)}
			</TableCell>
			<TableCell className="text-right tabular-nums">{row.sealedRuns ?? t("agents.runsUnknown")}</TableCell>
		</TableRow>
	);
}