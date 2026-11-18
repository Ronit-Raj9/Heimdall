import { EntityLink } from "@/components/shell";
import type { SuiteLockState, SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { CircleAlert, Lock, LockOpen, TriangleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";
import { suiteAdmission } from "./home.utils";

const LOCK_CLASS = {
	locked: "border-chart-success/50 bg-chart-success/10 text-chart-success-ink",
	stale: "border-chart-warning/60 bg-chart-warning/10 text-chart-warning-ink",
	unlocked: "border-border text-muted-foreground",
	unknown: "border-border text-muted-foreground",
} as const satisfies Record<SuiteLockState, string>;

const LOCK_ICON = { locked: Lock, stale: TriangleAlert, unlocked: LockOpen, unknown: LockOpen } as const;

const BAR_CLASS = { good: "bg-chart-success", warn: "bg-chart-warning", neutral: "bg-chart-info", bad: "bg-chart-error" } as const;

/** Each suite with its admission state: tasks admitted out of all, and whether the lock holds. */
export function SuitesPanel({ suites, limit = 8 }: { suites: readonly SuiteSummary[]; limit?: number }) {
	return (
		<ul className="divide-border/70 flex flex-col divide-y" data-testid="home-suites-list">
			{suites.slice(0, limit).map((suite) => (
				<SuiteRow key={suite.id} suite={suite} />
			))}
		</ul>
	);
}

function SuiteRow({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("bench");
	const admission = suiteAdmission(suite);
	const lock = suite.lock_state in LOCK_CLASS ? suite.lock_state : "unknown";
	const LockIcon = LOCK_ICON[lock];
	return (
		<li className="flex min-w-0 flex-col gap-1.5 px-3 py-2.5" data-testid={`home-suite-${suite.id}`} data-lock={lock}>
			<div className="flex min-w-0 items-center gap-2">
				<EntityLink kind="suite" id={suite.id} className="min-w-0 truncate text-[13px]" />
				{suite.version && <span className="text-muted-foreground shrink-0 font-mono text-[11px]">{suite.version}</span>}
				<span className="ml-auto shrink-0">
					{suite.error ? (
						<span className="border-chart-error/50 bg-chart-error/10 text-chart-error-ink inline-flex h-5 items-center gap-1 rounded-[5px] border px-1.5 text-xs font-medium">
							<CircleAlert className="size-3" aria-hidden="true" />
							{t("home.suites.error")}
						</span>
					) : (
						<span
							className={cn("inline-flex h-5 items-center gap-1 rounded-[5px] border px-1.5 text-xs font-medium", LOCK_CLASS[lock])}
							data-testid={`home-suite-lock-${suite.id}`}
						>
							<LockIcon className="size-3" aria-hidden="true" />
							{t(`home.suites.lock.${lock}`)}
						</span>
					)}
				</span>
			</div>
			{!suite.error && (
				<div className="flex items-center gap-2.5">
					<div
						className="bg-chart-track h-1.5 flex-1 overflow-hidden rounded-full"
						role="meter"
						aria-valuemin={0}
						aria-valuemax={admission.total}
						aria-valuenow={admission.admitted}
						aria-label={t("home.suites.admitted", { admitted: admission.admitted, total: admission.total })}
					>
						<div
							className={cn("h-full rounded-full", BAR_CLASS[admission.tone])}
							style={{ width: `${Math.round(admission.share * 100)}%` }}
						/>
					</div>
					<span className="text-muted-foreground shrink-0 text-xs tabular-nums" data-testid={`home-suite-admitted-${suite.id}`}>
						{t("home.suites.admitted", { admitted: admission.admitted, total: admission.total })}
					</span>
				</div>
			)}
		</li>
	);
}