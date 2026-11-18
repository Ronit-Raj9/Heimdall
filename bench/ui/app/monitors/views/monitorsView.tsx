import { MonitorCertDetail, MonitorCertList } from "@/components/integrity/monitorCertification";
import { LoadError } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { useGetMonitorCertificationQuery, useListMonitorCertificationsQuery } from "@/lib/store/apis/integrityApi";
import { Link } from "@tanstack/react-router";
import { ArrowLeft } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";

/** The monitors screen (Eval integrity area, S2): every monitor certification, or one with ?cert=. */
export default function MonitorsView() {
	const { t } = useTranslation("bench");
	const [cert] = useQueryState("cert", parseAsString);
	return (
		<section className="flex flex-col gap-6" data-testid="monitors-screen">
			<PageHeader
				title={t("integrity.monitors.title")}
				subtitle={t("integrity.monitors.subtitle")}
				breadcrumbs={cert ? [{ label: t("integrity.monitors.title"), to: "/monitors" }, { label: cert }] : undefined}
			/>
			{cert ? <CertificationScreen certId={cert} /> : <CertificationList />}
			<p className="text-muted-foreground max-w-3xl text-xs" data-testid="monitors-disclaimer">
				{t("integrity.monitors.disclaimer")}
			</p>
		</section>
	);
}

function CertificationList() {
	const { t } = useTranslation("bench");
	const q = useListMonitorCertificationsQuery();
	if (q.isLoading) return <Skeleton className="h-40 w-full" data-testid="monitors-list-loading" />;
	if (q.isError)
		return <LoadError title={t("integrity.monitors.loadError")} error={q.error} onRetry={q.refetch} testId="monitors-list-error" />;
	const certs = q.data?.certifications ?? [];
	return (
		<Card>
			<CardContent>
				{certs.length === 0 ? (
					<p className="text-muted-foreground font-mono text-xs" data-testid="monitors-empty">
						{t("integrity.monitors.noneAll")}
					</p>
				) : (
					<MonitorCertList certs={certs} />
				)}
			</CardContent>
		</Card>
	);
}

function CertificationScreen({ certId }: { certId: string }) {
	const { t } = useTranslation("bench");
	const q = useGetMonitorCertificationQuery(certId);
	return (
		<div className="flex flex-col gap-4">
			<div>
				<Button variant="ghost" size="sm" asChild>
					<Link to="/monitors" data-testid="monitors-back">
						<ArrowLeft className="size-4" aria-hidden="true" />
						{t("integrity.monitors.back")}
					</Link>
				</Button>
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
				<Card>
					<CardContent>
						<MonitorCertDetail cert={q.data} />
					</CardContent>
				</Card>
			)}
		</div>
	);
}