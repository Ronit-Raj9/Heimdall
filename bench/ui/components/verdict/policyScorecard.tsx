import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ValueCell } from "@/components/valueCell";
import { getLocale } from "@/lib/i18n";
import {
	POLICY_RULE_THRESHOLDS,
	type MetricDescriptor,
	type PolicyCheckResult,
	type PolicyDecision,
	type PolicyResult,
	type PolicyStatus,
	type ValueKind,
} from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import type { MouseEvent } from "react";
import { useTranslation } from "react-i18next";
import { TONE_CLASS, formatSignedPp, shortHash, type Tone } from "./verdict.utils";

/** ALLOW green, WARN amber, BLOCK red. */
export const POLICY_DECISION_TONE = {
	allow: "good",
	warn: "warn",
	block: "bad",
} as const satisfies Record<PolicyDecision, Tone>;

/** Unknown is neutral and hatched: never drawn as a pass. */
export const POLICY_STATUS_TONE = {
	pass: "good",
	fail: "bad",
	unknown: "neutral",
} as const satisfies Record<PolicyStatus, Tone>;

/** Aggregates have no catalog descriptor; their units are fixed (bench/policy). */
const AGGREGATE_KIND: Record<string, ValueKind> = {
	integrity_violations: "count",
	cost_per_verified_success: "usd",
	// rule timeout_parity: the arm's timeout rate.
	timeout_rate: "rate",
};

/** Run ids shown per check before "+n more". */
const EVIDENCE_SHOWN = 5;

export interface PolicyScorecardProps {
	/** The verdict payload's policy; absent when the experiment has no policy. */
	policy: PolicyResult | null | undefined;
	expId: string;
	/** Catalog metrics by name, for the observed value's unit. */
	metrics?: Record<string, MetricDescriptor>;
	/** run id -> pair id, from the task grid, so evidence opens the matched pair. */
	runPairs?: Record<string, string>;
	/** In-app navigation for an evidence link; without it the link is a plain href. */
	onOpenPair?: (pairId: string) => void;
	className?: string;
}

/** The release policy under the verdict card: the overall decision, then one row per check. */
export function PolicyScorecard({ policy, expId, metrics, runPairs, onOpenPair, className }: PolicyScorecardProps) {
	const { t } = useTranslation("bench");

	if (!policy) {
		return (
			<p className={cn("text-muted-foreground text-sm", className)} data-testid="policy-scorecard-none">
				{t("policy.none")}
			</p>
		);
	}

	const tone = POLICY_DECISION_TONE[policy.decision] ?? "neutral";
	const counts = { pass: 0, fail: 0, unknown: 0 };
	for (const check of policy.checks) counts[check.status] = (counts[check.status] ?? 0) + 1;

	return (
		<div className={cn("flex flex-col gap-4", className)} data-testid="policy-scorecard" data-decision={policy.decision}>
			<div className="flex flex-wrap items-center gap-3">
				<span
					className={cn("rounded-sm border px-3 py-1 font-mono text-sm font-semibold tracking-wide", TONE_CLASS[tone])}
					data-testid="policy-decision"
					data-tone={tone}
					aria-label={t("policy.decisionLabel")}
				>
					{t(`policy.decision.${policy.decision}`)}
				</span>
				<span className="text-muted-foreground text-sm">{t(`policy.decisionHint.${policy.decision}`)}</span>
			</div>
			<div className="text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs" data-testid="policy-meta">
				<span className="font-mono" title={policy.policy_hash}>
					{t("policy.meta", { name: policy.policy_name, hash: shortHash(policy.policy_hash) })}
				</span>
				<span>{t("policy.counts", counts)}</span>
			</div>
			<Table data-testid="policy-checks-table">
				<TableHeader>
					<TableRow>
						<TableHead>{t("policy.columns.status")}</TableHead>
						<TableHead>{t("policy.columns.check")}</TableHead>
						<TableHead>{t("policy.columns.target")}</TableHead>
						<TableHead>{t("policy.columns.observed")}</TableHead>
						<TableHead>{t("policy.columns.threshold")}</TableHead>
						<TableHead>{t("policy.columns.n")}</TableHead>
						<TableHead>{t("policy.columns.reason")}</TableHead>
						<TableHead>{t("policy.columns.evidence")}</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{policy.checks.map((check) => (
						<CheckRow key={check.id} check={check} expId={expId} metrics={metrics} runPairs={runPairs} onOpenPair={onOpenPair} />
					))}
				</TableBody>
			</Table>
		</div>
	);
}

function CheckRow({
	check,
	expId,
	metrics,
	runPairs,
	onOpenPair,
}: {
	check: PolicyCheckResult;
	expId: string;
	metrics?: Record<string, MetricDescriptor>;
	runPairs?: Record<string, string>;
	onOpenPair?: (pairId: string) => void;
}) {
	const { t } = useTranslation("bench");
	const tone = POLICY_STATUS_TONE[check.status] ?? "neutral";
	const condition = check.condition === "all" ? t("policy.allConditions") : check.condition;
	return (
		<TableRow data-testid={`policy-check-row-${check.id}`} data-status={check.status} data-blocking={check.blocking}>
			<TableCell>
				<div className="flex items-center gap-1.5">
					<Badge
						variant="outline"
						className={cn("font-mono text-[10px]", TONE_CLASS[tone], check.status === "unknown" && "bench-hatched border-dashed")}
						data-testid={`policy-check-status-${check.id}`}
					>
						{t(`policy.status.${check.status}`)}
					</Badge>
					{check.blocking && (
						<Badge variant="outline" className={cn("text-[10px]", TONE_CLASS.bad)} data-testid={`policy-check-blocking-${check.id}`}>
							{t("policy.blocking")}
						</Badge>
					)}
				</div>
			</TableCell>
			<TableCell>
				<div className="flex flex-col">
					<span className="font-mono text-xs">{check.id}</span>
					<span className="text-muted-foreground font-mono text-[10px]">{check.metric}</span>
				</div>
			</TableCell>
			<TableCell className="text-xs">{t("policy.target", { condition, arm: check.arm })}</TableCell>
			<TableCell>
				<Observed check={check} metrics={metrics} />
			</TableCell>
			<TableCell className="font-mono text-xs">{check.threshold}</TableCell>
			<TableCell className="tabular-nums">{check.n}</TableCell>
			<TableCell className="text-muted-foreground max-w-md text-xs whitespace-normal">{check.reason}</TableCell>
			<TableCell>
				<Evidence check={check} expId={expId} runPairs={runPairs} onOpenPair={onOpenPair} />
			</TableCell>
		</TableRow>
	);
}

/** A rule's observed value is the release effect d_bar, shown in signed pp; anything else goes through ValueCell. */
function Observed({ check, metrics }: { check: PolicyCheckResult; metrics?: Record<string, MetricDescriptor> }) {
	const { t } = useTranslation("bench");
	const value = check.observed;
	if (POLICY_RULE_THRESHOLDS.includes(check.threshold) && value?.state === "observed" && typeof value.v === "number") {
		return (
			<span className="tabular-nums" title={t("policy.dbarHint", { arm: check.arm })} data-testid={`policy-check-observed-${check.id}`}>
				{t("policy.dbar", { value: formatSignedPp(value.v, getLocale()) })}
			</span>
		);
	}
	const kind = AGGREGATE_KIND[check.metric] ?? metrics?.[check.metric]?.kind ?? "count";
	return <ValueCell value={value} kind={kind} testId={`policy-check-observed-${check.id}`} />;
}

/** Run ids behind a result. A run with a known pair links to the matched-run inspector. */
function Evidence({
	check,
	expId,
	runPairs,
	onOpenPair,
}: {
	check: PolicyCheckResult;
	expId: string;
	runPairs?: Record<string, string>;
	onOpenPair?: (pairId: string) => void;
}) {
	const { t } = useTranslation("bench");
	const ids = check.evidence ?? [];
	if (ids.length === 0) return <span className="text-muted-foreground">-</span>;
	const shown = ids.slice(0, EVIDENCE_SHOWN);
	return (
		<div className="flex max-w-xs flex-wrap gap-1" data-testid={`policy-check-evidence-${check.id}`}>
			{shown.map((run) => {
				const pair = runPairs?.[run];
				if (!pair) {
					return (
						<code key={run} className="text-muted-foreground font-mono text-[10px]">
							{run}
						</code>
					);
				}
				const href = `/runs?pair=${encodeURIComponent(pair)}&exp=${encodeURIComponent(expId)}`;
				const open = (event: MouseEvent<HTMLAnchorElement>) => {
					if (!onOpenPair || event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
					event.preventDefault();
					onOpenPair(pair);
				};
				return (
					<a
						key={run}
						href={href}
						onClick={open}
						className="text-primary font-mono text-[10px] underline-offset-2 hover:underline"
						aria-label={t("policy.openRun", { run })}
						data-testid={`policy-evidence-link-${run}`}
					>
						{run}
					</a>
				);
			})}
			{ids.length > shown.length && (
				<span className="text-muted-foreground text-[10px]">{t("policy.evidenceMore", { count: ids.length - shown.length })}</span>
			)}
		</div>
	);
}