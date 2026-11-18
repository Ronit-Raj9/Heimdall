import { ExperimentRunActions } from "@/components/platform/experimentActions";
import { ValueCell } from "@/components/valueCell";
import { formatValueNumber } from "@/components/valueCell.utils";
import { Button } from "@/components/ui/button";
import { getLocale } from "@/lib/i18n";
import type { ExperimentStatus } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { TERMINAL_STATES } from "./experiments.utils";

const STATE_CLASS: Record<ExperimentStatus["state"], string> = {
	created: "border-border text-muted-foreground",
	running: "border-chart-info/60 bg-chart-info/10 text-chart-info-ink",
	analyzing: "border-chart-info/60 bg-chart-info/10 text-chart-info-ink",
	done: "border-chart-success/60 bg-chart-success/10 text-chart-success-ink",
	failed: "border-chart-error/60 bg-chart-error/10 text-chart-error-ink",
	cancelled: "border-border text-muted-foreground",
};

export interface ExperimentProgressCardProps {
	status: ExperimentStatus;
	/** The spend cap the experiment was created with, when this page knows it. */
	capUsd?: number;
	onNew?: () => void;
}

/** Live progress of one experiment: slots done, running and failed, spend, and the verdict link when done. */
export function ExperimentProgressCard({ status, capUsd, onNew }: ExperimentProgressCardProps) {
	const { t } = useTranslation("bench");
	const { total, done, running, failed } = status.slots;
	const share = (n: number) => (total > 0 ? `${Math.min(100, (n / total) * 100)}%` : "0%");
	const terminal = TERMINAL_STATES.has(status.state);

	return (
		<div className="flex flex-col gap-3" data-testid="experiment-progress" data-state={status.state}>
			<div className="flex flex-wrap items-center gap-2">
				<h2 className="font-mono text-sm font-semibold">{t("experiments.new.progress.title", { id: status.exp_id })}</h2>
				<span
					className={cn("rounded-sm border px-1.5 py-0.5 text-xs font-medium", STATE_CLASS[status.state], !terminal && "animate-pulse")}
					data-testid="experiment-progress-state"
				>
					{t(`experiments.new.progress.state.${status.state}`)}
				</span>
			</div>
			{status.name && <p className="text-sm">{status.name}</p>}
			<div
				className="bg-chart-track flex h-2.5 w-full overflow-hidden rounded-full"
				role="progressbar"
				aria-valuemin={0}
				aria-valuemax={total}
				aria-valuenow={done + failed}
				aria-label={t("experiments.new.progress.slots", { done, running, failed, total })}
			>
				<div className="bg-chart-success h-full" style={{ width: share(done) }} />
				<div className="bg-chart-error h-full" style={{ width: share(failed) }} />
				<div className="bg-chart-info h-full animate-pulse" style={{ width: share(running) }} />
			</div>
			<p className="text-muted-foreground text-xs tabular-nums" data-testid="experiment-progress-slots">
				{t("experiments.new.progress.slots", { done, running, failed, total })}
			</p>
			<div className="flex flex-wrap items-baseline gap-2 text-sm">
				<span className="text-muted-foreground text-xs">{t("experiments.new.progress.spent")}</span>
				<ValueCell value={status.spent_usd} kind="usd" testId="experiment-progress-spent" />
				{capUsd !== undefined && (
					<span className="text-muted-foreground text-xs">
						{t("experiments.new.progress.cap", { cap: formatValueNumber(capUsd, "usd", getLocale()) })}
					</span>
				)}
			</div>
			<p className="text-muted-foreground truncate font-mono text-[11px]" title={status.manifest_hash}>
				{t("experiments.new.progress.manifest", { hash: status.manifest_hash })}
			</p>
			<div className="flex flex-wrap gap-2">
				{status.state === "done" && (
					<Button asChild size="sm" dataTestId="experiment-open-verdict">
						<Link to="/experiments/$expId" params={{ expId: status.exp_id }}>
							{t("experiments.new.progress.openVerdict")}
						</Link>
					</Button>
				)}
				{/* Jobs: cancel a running experiment, resume a cancelled or failed one. */}
				<ExperimentRunActions status={status} />
				{onNew && (
					<Button variant="outline" size="sm" onClick={onNew} dataTestId="experiment-plan-another">
						{t("experiments.new.progress.newExperiment")}
					</Button>
				)}
			</div>
		</div>
	);
}