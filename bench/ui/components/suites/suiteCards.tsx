import { GradingBadge, TrackBadge } from "@/components/scenario/scenarioBadges";
import { suiteGrading, suiteTrack } from "@/components/scenario/scenario.utils";
import { shortHash } from "@/components/verdict/verdict.utils";
import type { SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { CircleAlert, CircleX, EyeOff, ShieldOff } from "lucide-react";
import { useTranslation } from "react-i18next";
import { LockBadge, ToneBadge, VerifierBadge } from "./suiteBadges";

export interface SuiteCardsProps {
	suites: readonly SuiteSummary[];
}

/**
 * One card per suite: lock state, how many tasks are admitted, drift, verifier format and base
 * image. Each card is a link to the suite's page, /suites/$suiteId.
 */
export function SuiteCards({ suites }: SuiteCardsProps) {
	const { t } = useTranslation("bench");
	return (
		<ul
			className="grid min-w-0 grid-cols-1 gap-3 md:grid-cols-2 2xl:grid-cols-3"
			aria-label={t("suites.pickerLabel")}
			data-testid="suites-cards"
		>
			{suites.map((suite) => (
				<li key={suite.id} className="min-w-0">
					<SuiteCard suite={suite} />
				</li>
			))}
		</ul>
	);
}

function SuiteCard({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("bench");
	const allAdmitted = suite.task_count > 0 && suite.admitted_count === suite.task_count;
	return (
		<Link
			to="/suites/$suiteId"
			params={{ suiteId: suite.id }}
			title={t("suites.card.select", { suite: suite.version ? `${suite.id}@${suite.version}` : suite.id })}
			data-testid={`suites-card-${suite.id}`}
			data-state={suite.error ? "error" : suite.lock_state}
			className={cn(
				"bg-card focus-visible:ring-ring/50 hover:border-foreground/25 flex h-full w-full min-w-0 flex-col gap-2 rounded-sm border p-3 text-left transition-colors focus-visible:ring-[3px] focus-visible:outline-none",
			)}
		>
			<span className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
				<span className="text-foreground truncate font-mono text-sm font-semibold">{suite.id}</span>
				{suite.version && <span className="text-muted-foreground font-mono text-xs">{suite.version}</span>}
			</span>
			{suite.error ? (
				<span className="flex min-w-0 flex-col gap-1">
					<ToneBadge tone="bad" icon={CircleX}>
						{t("suites.card.loadFailed")}
					</ToneBadge>
					<span className="text-muted-foreground line-clamp-3 font-mono text-[11px] break-all">{suite.error}</span>
				</span>
			) : (
				<>
					{suite.description && <span className="text-muted-foreground line-clamp-2 text-xs">{suite.description}</span>}
					<span className="flex flex-wrap items-center gap-1.5">
						<TrackBadge track={suiteTrack(suite)} testId={`suites-card-${suite.id}-track`} />
						{suiteGrading(suite) === "judged" && <GradingBadge grading="judged" testId={`suites-card-${suite.id}-judged`} />}
						<LockBadge state={suite.lock_state} testId={`suites-card-${suite.id}-lock`} />
						<ToneBadge
							tone={allAdmitted ? "good" : suite.admitted_count > 0 ? "warn" : "neutral"}
							testId={`suites-card-${suite.id}-admitted`}
						>
							{t("suites.card.admitted", { admitted: suite.admitted_count, total: suite.task_count })}
						</ToneBadge>
						{suite.drift.length > 0 && (
							<ToneBadge tone="warn" icon={CircleAlert} testId={`suites-card-${suite.id}-drift`}>
								{t("suites.card.drift", { count: suite.drift.length })}
							</ToneBadge>
						)}
						{(suite.held_out_count ?? 0) > 0 && (
							<ToneBadge tone="neutral" icon={EyeOff} testId={`suites-card-${suite.id}-held-out`}>
								{t("suiteHealth.card.heldOut", { count: suite.held_out_count })}
							</ToneBadge>
						)}
						{(suite.quarantined_count ?? 0) > 0 && (
							<ToneBadge tone="warn" icon={ShieldOff} testId={`suites-card-${suite.id}-quarantined`}>
								{t("suiteHealth.card.quarantined", { count: suite.quarantined_count })}
							</ToneBadge>
						)}
						<VerifierBadge format={suite.verifier_result} />
						<span className="text-muted-foreground text-xs">{t("suites.card.probes", { count: suite.probes.length })}</span>
					</span>
					<span className="text-muted-foreground flex min-w-0 flex-wrap items-center gap-x-2 text-[11px]">
						<span>{t("suites.card.image")}</span>
						<span className="text-foreground truncate font-mono">{suite.base_image.tag}</span>
						<span className="font-mono">{suite.base_image.digest ? shortHash(suite.base_image.digest) : t("suites.card.notPinned")}</span>
					</span>
				</>
			)}
		</Link>
	);
}