import { ToneBadge } from "@/components/suites/suiteBadges";
import type { AgentSummary } from "@/lib/types/bench";
import { CircleX } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * One agent config: how its image is built, what evidence its runs get, its budgets and gateway,
 * and the names (never the values) of its environment variables.
 */
export function AgentCard({ agent }: { agent: AgentSummary }) {
	const { t } = useTranslation("bench");
	const testId = `agents-card-${agent.id || agent.path.replace(/[^A-Za-z0-9_-]/g, "-")}`;
	if (agent.error) {
		return (
			<article className="bg-card flex min-w-0 flex-col gap-2 rounded-sm border p-3" data-testid={testId} data-state="error">
				<h2 className="text-foreground truncate font-mono text-sm font-semibold">{agent.path}</h2>
				<ToneBadge tone="bad" icon={CircleX}>
					{t("agents.loadFailed")}
				</ToneBadge>
				<pre className="text-muted-foreground min-w-0 font-mono text-[11px] break-all whitespace-pre-wrap">{agent.error}</pre>
			</article>
		);
	}
	const budgets = [
		t("agents.budgetsValue", { wall: agent.budgets.wall_s, cpus: agent.budgets.cpus, mem: agent.budgets.mem_gb }),
		...(agent.budgets.turns ? [t("agents.turns", { count: agent.budgets.turns })] : []),
	].join(", ");
	const rows: { label: string; value: React.ReactNode; hint?: string; key: string }[] = [
		{ key: "image", label: t("agents.image"), value: <code className="break-all">{agent.image}</code> },
		{
			key: "build",
			label: t("agents.build"),
			value: agent.per_suite_build
				? t("agents.perSuite", { arg: agent.base_arg })
				: agent.dockerfile
					? t("agents.oneImage")
					: t("agents.pulled"),
			hint: agent.per_suite_build ? t("agents.perSuiteHint") : undefined,
		},
		{
			key: "evidence",
			label: t("agents.evidence"),
			value: agent.parser ? t("agents.evidenceNative", { parser: agent.parser }) : t("agents.evidenceGateway"),
			hint: agent.parser ? undefined : t("agents.evidenceGatewayHint"),
		},
		{ key: "model", label: t("agents.model"), value: <code>{agent.model}</code>, hint: t("agents.modelHint") },
		{ key: "notes", label: t("agents.notes"), value: <code>{agent.notes_project_file}</code> },
		{ key: "budgets", label: t("agents.budgets"), value: budgets },
		{
			key: "gateway",
			label: t("agents.gateway"),
			value: [
				agent.gateway.provider,
				...(agent.gateway.fallbacks?.length ? [t("agents.fallbacks", { list: agent.gateway.fallbacks.join(", ") })] : []),
			].join(", "),
		},
		{
			key: "runs",
			label: t("agents.runs"),
			value: agent.sealed_runs === null ? t("agents.runsUnknown") : <span className="tabular-nums">{agent.sealed_runs}</span>,
			hint: t("agents.runsHint"),
		},
	];
	return (
		<article className="bg-card flex min-w-0 flex-col gap-3 rounded-sm border p-3" data-testid={testId}>
			<header className="flex min-w-0 flex-wrap items-center gap-2">
				<h2 className="text-foreground truncate font-mono text-sm font-semibold">{agent.id}</h2>
				<ToneBadge tone="neutral">{agent.adapter}</ToneBadge>
				{agent.scripted_kind && (
					<ToneBadge tone="warn" hint={t("agents.scriptedHint")}>
						{t("agents.scripted", { kind: agent.scripted_kind })}
					</ToneBadge>
				)}
				{agent.per_suite_build && <ToneBadge tone="good">{t("agents.perSuite", { arg: agent.base_arg })}</ToneBadge>}
				<span className="text-muted-foreground ml-auto font-mono text-[11px]">{agent.path}</span>
			</header>
			<dl className="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">
				{rows.map((row) => (
					<div key={row.key} className="contents" data-testid={`${testId}-${row.key}`}>
						<dt className="text-muted-foreground" title={row.hint}>
							{row.label}
						</dt>
						<dd className="text-foreground min-w-0 break-words">
							{row.value}
							{row.hint && <span className="sr-only">. {row.hint}</span>}
						</dd>
					</div>
				))}
			</dl>
			<div className="flex min-w-0 flex-col gap-1">
				<span className="text-muted-foreground text-xs">{t("agents.env")}</span>
				{agent.env_names.length === 0 ? (
					<span className="text-muted-foreground text-xs">{t("agents.envNone")}</span>
				) : (
					<ul className="flex flex-wrap gap-1" data-testid={`${testId}-env`}>
						{agent.env_names.map((name) => (
							<li key={name} className="bg-muted text-foreground rounded-sm px-1.5 py-0.5 font-mono text-[11px] break-all">
								{name}
							</li>
						))}
					</ul>
				)}
			</div>
			{agent.file_paths.length > 0 && (
				<div className="flex min-w-0 flex-col gap-1">
					<span className="text-muted-foreground text-xs">{t("agents.files")}</span>
					<ul className="flex flex-col gap-0.5">
						{agent.file_paths.map((path) => (
							<li key={path} className="text-foreground font-mono text-[11px] break-all">
								{path}
							</li>
						))}
					</ul>
				</div>
			)}
		</article>
	);
}