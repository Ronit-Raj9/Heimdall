import { GovDialog } from "@/components/govern/govDialog";
import { ReleaseActions } from "@/components/govern/releaseActions";
import { ReleasePanel } from "@/components/releases/releasePanel";
import { LoadError } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { EmptyState } from "@/components/shell/emptyState";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useListReleasesQuery } from "@/lib/store";
import { Plus, Rocket } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ChainBadge, ReleaseCards } from "./releaseCards";

/** The releases screen: the hash-chained release log as a timeline, newest first; "New release" opens the record dialog. */
export default function ReleasesView() {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("u14");
	const releases = useListReleasesQuery();
	// ?exp=<id>: "Promote this version" on a verdict opens the form with that comparison picked.
	const [expParam, setExpParam] = useQueryState("exp", parseAsString);
	const [open, setOpen] = useState(Boolean(expParam));
	const list = releases.data?.releases ?? [];

	return (
		<section className="mx-auto flex w-full max-w-[1200px] flex-col gap-8" data-testid="releases-screen">
			<PageHeader
				title={tu("releases.title")}
				subtitle={tu("releases.subtitle")}
				actions={
					<Button onClick={() => setOpen(true)} data-testid="release-new">
						<Plus className="size-4" aria-hidden="true" />
						{tu("releases.new")}
					</Button>
				}
			/>
			<div className="grid grid-cols-[minmax(0,1fr)] gap-8 xl:grid-cols-[minmax(0,1fr)_20rem] xl:items-start" data-testid="releases-layout">
				<div className="flex min-w-0 flex-col gap-4">
					<div className="flex flex-wrap items-center justify-between gap-2">
						<h2 className="text-sm font-semibold">{tu("releases.timeline")}</h2>
						{releases.data && <ChainBadge ok={releases.data.chain_ok} />}
					</div>
					{releases.isLoading ? (
						<div className="flex flex-col gap-4" data-testid="release-timeline-loading">
							<Skeleton className="h-28 w-full rounded-xl" />
							<Skeleton className="h-28 w-full rounded-xl" />
						</div>
					) : releases.isError ? (
						<LoadError
							title={t("releases.timeline.loadError")}
							error={releases.error}
							onRetry={releases.refetch}
							testId="release-timeline-error"
						/>
					) : list.length === 0 ? (
						<EmptyState
							icon={Rocket}
							message={tu("releases.empty")}
							action={{ label: tu("releases.new"), onClick: () => setOpen(true) }}
							testId="release-timeline-empty"
						/>
					) : (
						<ReleaseCards releases={list} chainOk={releases.data?.chain_ok ?? false} />
					)}
					<p className="text-muted-foreground text-xs" data-testid="releases-disclaimer" title={t("releases.disclaimer")}>
						<span className="line-clamp-2">{t("releases.disclaimer")}</span>
					</p>
				</div>
				<aside className="flex min-w-0 flex-col gap-6 xl:sticky xl:top-0">
					<ReleaseActions releases={list} onDone={() => void releases.refetch()} />
				</aside>
			</div>
			<GovDialog
				open={open}
				onClose={() => {
					setOpen(false);
					void setExpParam(null);
				}}
				title={tu("releases.newTitle")}
				description={tu("releases.newHint")}
				closeLabel={tu("releases.close")}
				testId="release-dialog"
			>
				<div className="[&_[data-slot=card-header]]:hidden [&>[data-testid=release-panel]]:border-0 [&>[data-testid=release-panel]]:bg-transparent [&>[data-testid=release-panel]]:py-0 [&_[data-slot=card-content]]:px-0">
					<ReleasePanel initialExpId={expParam ?? undefined} />
				</div>
			</GovDialog>
		</section>
	);
}
