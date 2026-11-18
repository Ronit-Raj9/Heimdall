import { LiveBody } from "@/components/livescore/livePanel";
import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { useGetLivescoreQuery, usePullLivescoreMutation } from "@/lib/store/apis/livescoreApi";
import { useTranslation } from "react-i18next";

/**
 * Live traffic: /live. Real sessions read from the Bifrost gateway, scored without ground truth,
 * two live versions compared, worst sessions linked to the failure inbox (docs/live-scoring.md).
 */
export default function LiveView() {
	const { t } = useTranslation("bench");
	const status = useGetLivescoreQuery();
	const data = status.data;
	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="live-screen">
			<header className="flex flex-col gap-1">
				<div className="flex flex-wrap items-center gap-3">
					<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("live.title")}</h1>
					<ModeBadge mode={data?.mode} isLoading={status.isLoading} isError={status.isError && !data} />
				</div>
				<p className="text-muted-foreground max-w-3xl text-sm">{t("live.subtitle")}</p>
			</header>
			{status.isLoading ? (
				<BlockSkeleton rows={5} testId="live-loading" />
			) : status.error || !data ? (
				<LoadError title={t("live.loadError")} error={status.error} onRetry={() => void status.refetch()} testId="live-error" />
			) : (
				<LiveBody status={data} actions={data.mode === "live" ? <PullButton /> : undefined} />
			)}
		</section>
	);
}

/** POST /livescore/pull with the write token: one read of the gateway now. */
function PullButton() {
	const { t } = useTranslation("bench");
	const [pull, state] = usePullLivescoreMutation();
	const scored = state.data ? Object.values(state.data.sessions).reduce((a, b) => a + b, 0) : 0;
	return (
		<span className="flex items-center gap-2">
			<Button size="sm" variant="outline" disabled={state.isLoading} onClick={() => void pull()} data-testid="live-pull">
				{t("live.pull")}
			</Button>
			{state.isSuccess && <span className="text-muted-foreground text-xs">{t("live.pulled", { sessions: scored })}</span>}
			{state.isError && (
				<span className="text-chart-error-ink text-xs" data-testid="live-pull-error">
					{t("live.pullError")}
				</span>
			)}
		</span>
	);
}
