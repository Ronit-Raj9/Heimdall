import { ToneBadge } from "@/components/suites/suiteBadges";
import { useGetExperimentRoutingQuery, useGetRunRoutingQuery } from "@/lib/store/apis/routingApi";
import type { RoutingArm, RoutingSummary, RoutingTargetUse, RunRouting } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";

/**
 * W9 routing policy under test (docs/routing-experiments.md). The verdict page's Gateway policy
 * card, fetched by experiment id. Renders nothing while loading, on an error, or when no arm
 * declares a routing policy: an experiment of fixed keys has nothing to say here.
 */
export function ExperimentGatewayPolicy({ expId }: { expId: string }) {
	const q = useGetExperimentRoutingQuery(expId);
	if (!q.data?.routed) return null;
	return <GatewayPolicyCard summary={q.data} />;
}

/** The run page's Gateway policy card, fetched by run id; nothing for an unrouted run. */
export function RunGatewayPolicy({ runId }: { runId: string }) {
	const q = useGetRunRoutingQuery(runId);
	if (!q.data?.policy) return null;
	return <RunGatewayPolicyCard routing={q.data} />;
}

/** Every arm's policy, how the arms differ, and per condition fallbacks, switches and targets. */
export function GatewayPolicyCard({ summary }: { summary: RoutingSummary }) {
	const { t } = useTranslation("bench");
	return (
		<section
			className="flex min-w-0 flex-col gap-3 rounded-md border p-3 text-xs"
			data-testid="gateway-policy-card"
			aria-label={t("routing.title")}
		>
			<div>
				<p className="text-sm font-medium">{t("routing.title")}</p>
				<p className="text-muted-foreground">{t("routing.description")}</p>
			</div>
			{summary.differences.length > 0 ? (
				<div data-testid="gateway-policy-differences">
					<p className="font-medium">{t("routing.differences")}</p>
					<ul className="list-disc pl-4">
						{summary.differences.map((d) => (
							<li key={d}>{d}</li>
						))}
					</ul>
				</div>
			) : (
				<p className="text-muted-foreground">{t("routing.noDifference")}</p>
			)}
			<div className="grid gap-3 md:grid-cols-2">
				{summary.arms.map((arm) => (
					<ArmPolicy key={arm.arm} arm={arm} />
				))}
			</div>
		</section>
	);
}

function ArmPolicy({ arm }: { arm: RoutingArm }) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex min-w-0 flex-col gap-2 rounded border p-2" data-testid={`gateway-policy-arm-${arm.arm}`}>
			<p className="font-medium">
				{arm.arm} <span className="text-muted-foreground">{arm.label}</span>
			</p>
			<p className="font-mono break-words" data-testid={`gateway-policy-summary-${arm.arm}`}>
				{arm.policy ? arm.summary : t("routing.fixed")}
			</p>
			{arm.policy_hash ? <p className="text-muted-foreground">{t("routing.policyHash", { hash: arm.policy_hash.slice(0, 12) })}</p> : null}
			{arm.conditions.length === 0 ? <p className="text-muted-foreground">{t("routing.noRuns")}</p> : null}
			{arm.conditions.map((c) => (
				<div key={c.condition} className="flex flex-col gap-1" data-testid={`gateway-policy-${arm.arm}-${c.condition}`}>
					<div className="flex flex-wrap items-center gap-2">
						<span className="font-medium">{t("routing.condition", { condition: c.condition, runs: c.runs })}</span>
						<ToneBadge tone={c.fallback_runs > 0 ? "warn" : "neutral"}>
							{t("routing.fallbacks", { fired: c.fallback_runs, known: c.fallback_known })}
						</ToneBadge>
						<ToneBadge tone={c.switches > 0 ? "warn" : "neutral"}>{t("routing.switches", { count: c.switches })}</ToneBadge>
					</div>
					<TargetTable targets={c.targets} />
				</div>
			))}
		</div>
	);
}

/** Targets by share of answered turns, with cost and latency; an unknown value says so, never 0. */
export function TargetTable({ targets }: { targets: RoutingTargetUse[] }) {
	const { t } = useTranslation("bench");
	if (targets.length === 0) return null;
	return (
		<table className="w-full table-fixed text-left">
			<thead className="text-muted-foreground">
				<tr>
					<th className="w-1/2 font-normal">{t("routing.target")}</th>
					<th className="font-normal">{t("routing.turns")}</th>
					<th className="font-normal">{t("routing.cost")}</th>
					<th className="font-normal">{t("routing.latency")}</th>
				</tr>
			</thead>
			<tbody>
				{targets.map((tg) => (
					<tr key={tg.target}>
						<td className="truncate font-mono" title={tg.target}>
							{tg.target}
						</td>
						<td>{formatShare(tg.turn_share)}</td>
						<td>{tg.cost_usd !== undefined ? `$${tg.cost_usd.toFixed(4)}` : t("routing.unknown")}</td>
						<td>{tg.latency_ms !== undefined ? `${Math.round(tg.latency_ms)} ms` : t("routing.unknown")}</td>
					</tr>
				))}
			</tbody>
		</table>
	);
}

/** One run's policy, the Bifrost rows that show which target served each request, and the gate. */
export function RunGatewayPolicyCard({ routing }: { routing: RunRouting }) {
	const { t } = useTranslation("bench");
	const fidelity = routing.fidelity;
	return (
		<section className="flex min-w-0 flex-col gap-2 rounded-md border p-3 text-xs" data-testid={`run-gateway-policy-${routing.arm}`}>
			<p className="text-sm font-medium">{t("routing.run.title")}</p>
			<p className="font-mono break-words">{routing.summary}</p>
			<div className="flex flex-wrap gap-2">
				{routing.fallback_used !== undefined ? (
					<ToneBadge tone={routing.fallback_used ? "warn" : "neutral"}>
						{routing.fallback_used ? t("routing.run.fallbackUsed") : t("routing.run.fallbackUnused")}
					</ToneBadge>
				) : null}
				{routing.switches !== undefined ? (
					<ToneBadge tone={routing.switches > 0 ? "warn" : "neutral"}>{t("routing.switches", { count: routing.switches })}</ToneBadge>
				) : null}
			</div>
			{fidelity?.detail ? (
				<p data-testid="run-gateway-policy-fidelity" className={fidelity.fired ? "text-destructive" : "text-muted-foreground"}>
					{fidelity.fired ? t("routing.run.fired", { detail: fidelity.detail }) : t("routing.run.expected", { detail: fidelity.detail })}
				</p>
			) : null}
			<TargetTable targets={routing.targets} />
			{routing.rows.length > 0 ? (
				<div>
					<p className="font-medium">{t("routing.run.rows")}</p>
					<ol className="flex flex-col gap-1">
						{routing.rows.map((r) => (
							<li key={r.id} className="flex flex-wrap items-center gap-2 font-mono" data-testid={`run-gateway-row-${r.id}`}>
								<span>
									{t("routing.run.row", {
										provider: r.provider,
										model: r.model_served,
										status: r.error_status ? `${r.status} ${r.error_status}` : r.status,
									})}
								</span>
								{r.fallback_index > 0 ? (
									<ToneBadge tone="warn">{t("routing.run.fallbackRow", { index: r.fallback_index })}</ToneBadge>
								) : null}
								{r.routing_rule_name ? <span className="text-muted-foreground">{r.routing_rule_name}</span> : null}
							</li>
						))}
					</ol>
				</div>
			) : null}
		</section>
	);
}

/** A turn share as a whole percent. */
export function formatShare(share: number): string {
	return `${Math.round(share * 100)}%`;
}