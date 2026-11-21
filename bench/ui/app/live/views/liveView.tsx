import { LiveBody } from "@/components/livescore/livePanel";
import { ModeBadge } from "@/components/modeBadge";
import { PageHeader } from "@/components/shell";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { useGetInboxWebhookQuery } from "@/lib/store/apis/liveinboxApi";
import { useGetLivescoreQuery, usePullLivescoreMutation } from "@/lib/store/apis/livescoreApi";
import { LiveActions, LiveSetup } from "./liveActions";
import { useTranslation } from "react-i18next";

/**
 * Live traffic: /live. Real sessions read from the Bifrost gateway, scored without ground truth,
 * two live versions compared, worst sessions linked to the failure inbox (docs/live-scoring.md).
 */
export default function LiveView() {
	const { t } = useTranslation("bench");
	const status = useGetLivescoreQuery();
	const data = status.data;
	// The webhook setup read also says whether this is the hosted server, where pulls are refused.
	const hosted = useGetInboxWebhookQuery().data?.hosted ?? false;
	const configured = Boolean(data?.configured && !data.problem);
	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="live-screen">
			<PageHeader
				title={t("live.title")}
				subtitle={t("live.subtitle")}
				meta={<ModeBadge mode={data?.mode} isLoading={status.isLoading} isError={status.isError && !data} />}
			/>
			{status.isLoading ? (
				<BlockSkeleton rows={5} testId="live-loading" />
			) : status.error || !data ? (
				<LoadError title={t("live.loadError")} error={status.error} onRetry={() => void status.refetch()} testId="live-error" />
			) : (
				<>
					{data.mode === "live" && <LiveActions configured={configured} hosted={hosted} />}
					{data.mode === "live" && !configured && <LiveSetup existing={Boolean(data.problem)} />}
					<LiveBody status={data} actions={data.mode === "live" && !hosted ? <PullButton /> : undefined} />
				</>
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