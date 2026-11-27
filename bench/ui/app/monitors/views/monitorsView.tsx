import { MonitorCertifyForm } from "@/components/govern/monitorCertifyForm";
import { MonitorCertDetail } from "@/components/integrity/monitorCertification";
import { GovDialog } from "@/components/govern/govDialog";
import { EmptyState } from "@/components/shell/emptyState";
import { MonitorCards } from "./monitorCards";
import { MonitorTestDelete } from "./monitorTestDelete";
import { LoadError } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { useGetMonitorCertificationQuery, useListMonitorCertificationsQuery } from "@/lib/store/apis/integrityApi";
import { Link } from "@tanstack/react-router";
import { ArrowLeft, Plus, ShieldCheck } from "lucide-react";
import { useState } from "react";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";
import { useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import "@/app/schedules/views/ufrjI18n";

/** True when at least one comparison finished: a safety rule test reads its runs (Q-308). Pure. */
export function hasFinishedComparison(exps: readonly { state?: string }[]): boolean {
	return exps.some((e) => e.state === "done");
}

/** The monitors screen (Eval integrity area, S2): every monitor certification, or one with ?cert=. */
export default function MonitorsView() {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("u14");
	const { t: tr } = useTranslation("ufrj");
	const [cert, setCert] = useQueryState("cert", parseAsString);
	const [certifying, setCertifying] = useState(false);
	// While the list is empty its empty state carries the one "certify" action, so the header does not repeat it (M7-68).
	const list = useListMonitorCertificationsQuery();
	const listEmpty = !list.isLoading && !list.isError && (list.data?.certifications ?? []).length === 0;
	// A safety rule test needs a finished comparison (Q-308): without one the action is held and says why.
	const exps = useListExperimentsQuery();
	const noComparison = exps.isSuccess && !hasFinishedComparison(exps.data?.experiments ?? []);
	return (
		<section className="mx-auto flex w-full max-w-[1200px] flex-col gap-8" data-testid="monitors-screen">
			<PageHeader
				title={tu("monitors.title")}
				subtitle={tu("monitors.subtitle")}
				actions={
					cert || listEmpty ? undefined : (
						<Button
							onClick={() => setCertifying(true)}
							disabled={noComparison}
							title={noComparison ? tr("monitors.needComparison") : undefined}
							data-testid="monitors-certify-open"
						>
							<Plus className="size-4" aria-hidden="true" />
							{tu("monitors.certifyNew")}
						</Button>
					)
				}
				breadcrumbs={cert ? [{ label: tu("monitors.title"), to: "/monitors" }, { label: cert }] : undefined}
			/>
			{cert ? <CertificationScreen certId={cert} onDeleted={() => (void setCert(null), void list.refetch())} /> : <CertificationList certifying={certifying} onCertifying={setCertifying} noComparison={noComparison} />}
			<p
				className="text-muted-foreground line-clamp-2 max-w-3xl text-xs"
				data-testid="monitors-disclaimer"
				title={tu("monitors.disclaimer")}
			>
				{tu("monitors.disclaimer")}
			</p>
		</section>
	);
}

function CertificationList({
	certifying,
	onCertifying,
	noComparison = false,
}: {
	certifying: boolean;
	onCertifying(open: boolean): void;
	noComparison?: boolean;
}) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("u14");
	const { t: tr } = useTranslation("ufrj");
	const q = useListMonitorCertificationsQuery();
	const certs = q.data?.certifications ?? [];
	return (
		<>
			{q.isLoading ? (
				<div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3" data-testid="monitors-list-loading">
					<Skeleton className="h-44 rounded-xl" />
					<Skeleton className="h-44 rounded-xl" />
					<Skeleton className="h-44 rounded-xl" />
				</div>
			) : q.isError ? (
				<LoadError title={t("integrity.monitors.loadError")} error={q.error} onRetry={q.refetch} testId="monitors-list-error" />
			) : certs.length === 0 ? (
				<EmptyState
					icon={ShieldCheck}
					message={tu("monitors.empty")}
					body={noComparison ? tr("monitors.needComparison") : undefined}
					action={
						noComparison
							? { label: tr("monitors.runComparison"), to: "/evaluate" }
							: { label: tu("monitors.certifyNew"), onClick: () => onCertifying(true) }
					}
					// Q-134: a second way forward for someone with nothing to certify yet.
					secondary={noComparison ? undefined : { label: tr("monitors.runComparison"), to: "/evaluate" }}
					testId="monitors-empty"
				/>
			) : (
				<MonitorCards certs={certs} />
			)}
			<GovDialog
				open={certifying}
				onClose={() => onCertifying(false)}
				title={tu("monitors.certifyNew")}
				closeLabel={tu("releases.close")}
				className="max-w-2xl"
				testId="monitors-certify-dialog"
			>
				<div className="[&_[data-slot=card-content]]:px-0 [&_h2]:hidden [&>[data-testid=monitor-certify]]:border-0 [&>[data-testid=monitor-certify]]:py-0 [&>[data-testid=monitor-certify]]:shadow-none">
					<MonitorCertifyForm
						onDone={() => {
							void q.refetch();
						}}
					/>
				</div>
			</GovDialog>
		</>
	);
}

function CertificationScreen({ certId, onDeleted }: { certId: string; onDeleted(): void }) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("u14");
	const { t: tr } = useTranslation("ufrj");
	const q = useGetMonitorCertificationQuery(certId);
	return (
		<div className="flex flex-col gap-4">
			<div className="flex flex-wrap items-start justify-between gap-2">
				<Button variant="ghost" size="sm" asChild>
					<Link to="/monitors" data-testid="monitors-back">
						<ArrowLeft className="size-4" aria-hidden="true" />
						{tu("monitors.back")}
					</Link>
				</Button>
				{q.data && <MonitorTestDelete certId={certId} name={q.data.monitor?.name || certId} onDeleted={onDeleted} />}
			</div>
			{q.isLoading ? (
				<Skeleton className="h-96 w-full" data-testid="monitor-cert-loading" />
			) : q.isError || !q.data ? (
				<LoadError
					title={t("integrity.monitors.notFound", { id: certId })}
					error={q.error}
					onRetry={q.refetch}
					testId="monitor-cert-error"
				/>
			) : (
				<Card className="rounded-xl">
					<CardContent className="p-5 sm:p-6">
						<MonitorCertDetail cert={q.data} />
					</CardContent>
				</Card>
			)}
		</div>
	);
}