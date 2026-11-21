import { ReleaseActions } from "@/components/govern/releaseActions";
import { ReleasePanel } from "@/components/releases/releasePanel";
import { ReleaseTimeline } from "@/components/releases/releaseTimeline";
import { LoadError } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { Skeleton } from "@/components/ui/skeleton";
import { useListReleasesQuery } from "@/lib/store";
import { useTranslation } from "react-i18next";

/** The releases screen: the hash-chained release log, newest first, and the panel that records a release. */
export default function ReleasesView() {
	const { t } = useTranslation("bench");
	const releases = useListReleasesQuery();

	return (
		<section className="flex flex-col gap-6" data-testid="releases-screen">
			<PageHeader title={t("releases.title")} subtitle={t("releases.subtitle")} />
			<div className="grid grid-cols-[minmax(0,1fr)] gap-6 xl:grid-cols-[minmax(0,1fr)_22rem] xl:items-start" data-testid="releases-layout">
				<div className="flex min-w-0 flex-col gap-3">
					<h2 className="text-sm font-medium">{t("releases.timeline.title")}</h2>
					<p className="text-muted-foreground text-xs" data-testid="releases-disclaimer">
						{t("releases.disclaimer")}
					</p>
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
				<aside className="flex min-w-0 flex-col gap-6 xl:sticky xl:top-0">
					<ReleasePanel />
					<ReleaseActions releases={releases.data?.releases ?? []} onDone={() => void releases.refetch()} />
				</aside>
			</div>
		</section>
	);
}