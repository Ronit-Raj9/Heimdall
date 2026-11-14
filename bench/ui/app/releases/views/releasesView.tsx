import { ReleasePanel } from "@/components/releases/releasePanel";
import { ReleaseTimeline } from "@/components/releases/releaseTimeline";
import { LoadError } from "@/components/runs/screenStates";
import { Skeleton } from "@/components/ui/skeleton";
import { useListReleasesQuery } from "@/lib/store";
import { useTranslation } from "react-i18next";

/** The releases screen: the hash-chained release log, newest first, and the panel that records a release. */
export default function ReleasesView() {
	const { t } = useTranslation("bench");
	const releases = useListReleasesQuery();

	return (
		<section className="flex flex-col gap-6" data-testid="releases-screen">
			<div className="flex flex-col gap-1">
				<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("releases.title")}</h1>
				<p className="text-muted-foreground text-sm">{t("releases.subtitle")}</p>
				<p className="text-muted-foreground text-xs" data-testid="releases-disclaimer">
					{t("releases.disclaimer")}
				</p>
			</div>
			<div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_22rem] xl:items-start">
				<div className="flex flex-col gap-3">
					<h2 className="text-sm font-medium">{t("releases.timeline.title")}</h2>
					{releases.isLoading ? (
						<Skeleton className="h-40 w-full" data-testid="release-timeline-loading" />
					) : releases.isError ? (
						<LoadError
							title={t("releases.timeline.loadError")}
							error={releases.error}
							onRetry={releases.refetch}
							testId="release-timeline-error"
						/>
					) : (
						<ReleaseTimeline releases={releases.data?.releases ?? []} chainOk={releases.data?.chain_ok ?? false} />
					)}
				</div>
				<aside className="xl:sticky xl:top-0">
					<ReleasePanel />
				</aside>
			</div>
		</section>
	);
}