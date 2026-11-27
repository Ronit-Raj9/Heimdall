import { LiveBody } from "@/components/livescore/livePanel";
import { cn } from "@/lib/utils";
import { pageWidthClass } from "@/components/shell/pageWidth";
import { PageHeader } from "@/components/shell";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { useGetPullReadinessQuery } from "@/lib/store/apis/liveinboxApi";
import { useGetLivescoreQuery, usePullLivescoreMutation } from "@/lib/store/apis/livescoreApi";
import { LiveActions, LiveSetup } from "./liveActions";
import { FeatureHero } from "@/components/inbox/featureHero";
import { Activity } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/**
 * Live traffic: /live. Real sessions read from the Bifrost gateway, scored without ground truth,
 * two live versions compared, worst sessions linked to the failure inbox (docs/live-scoring.md).
 */
export default function LiveView() {
	const { t } = useTranslation("bench");
	const { t: u } = useTranslation("ux13");
	const status = useGetLivescoreQuery();
	const data = status.data;
	// The readiness read says whether this is the hosted server and what a pull still needs.
	const ready = useGetPullReadinessQuery().data;
	const hosted = ready?.hosted ?? false;
	const configured = Boolean(data?.configured && !data.problem);
	const needSetup = !configured || Boolean(ready?.live.steps.some((s) => s.link === "#live-setup"));
	// The setup form opens from the hero, or straight away when a "#live-setup" link brought the user here.
	const [setupOpen, setSetupOpen] = useState(() => typeof window !== "undefined" && window.location.hash === "#live-setup");
	return (
		<section className={cn(pageWidthClass("wide"), "flex flex-col gap-6")} data-page-width="wide" data-testid="live-screen">
			<PageHeader title={t("live.title")} />
			{status.isLoading ? (
				<BlockSkeleton rows={5} testId="live-loading" />
			) : status.error || !data ? (
				<LoadError title={t("live.loadError")} error={status.error} onRetry={() => void status.refetch()} testId="live-error" />
			) : (
				<>
					{data.mode === "live" && <LiveActions configured={configured} hosted={hosted} check={ready?.live} />}
					{data.mode === "live" && needSetup && !setupOpen && (
						<FeatureHero
							icon={Activity}
							title={u("live.heroTitle")}
							message={u("live.heroMessage")}
							testId="live-hero"
							action={
								<Button onClick={() => setSetupOpen(true)} data-testid="live-hero-setup">
									{u("live.heroAction")}
								</Button>
							}
						/>
					)}
					{data.mode === "live" && needSetup && setupOpen && (
						<div id="live-setup" className="scroll-mt-4">
							<LiveSetup existing={Boolean(data.configured || data.problem)} hosted={hosted} onClose={() => setSetupOpen(false)} />
						</div>
					)}
					<LiveBody status={data} hosted={hosted} actions={data.mode === "live" && !hosted ? <PullButton /> : undefined} />
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