import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { ShieldCheck } from "lucide-react";
import "@/components/verdict/uf4aI18n";
import { useGetAwarenessQuery, useListMonitorCertificationsQuery } from "@/lib/store/apis/integrityApi";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { AwarenessPanel } from "./awarenessPanel";
import "./gw06I18n";
import { certificationsFor } from "./integrity.utils";
import { MonitorCertList } from "./monitorCertification";

/** Whether an RTK Query error is a 404 or 503: the server has no such route or no such data. */
function absent(error: unknown): boolean {
	const status = (error as { status?: unknown } | undefined)?.status;
	return status === 404 || status === 503;
}

/** The verdict page's eval-awareness section body: loading, an honest absence, or the panel. */
export function AwarenessSection({ expId, arms }: { expId: string; arms: string[] }) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("uf4a");
	const q = useGetAwarenessQuery(expId);
	// M4-6: a card skeleton that says what it waits for, never a blank.
	if (q.isLoading)
		return <BlockSkeleton rows={4} what={tu("checks.awarenessWhat")} onRetry={() => void q.refetch()} testId="awareness-loading" />;
	if (q.isError) {
		if (absent(q.error)) {
			return (
				<p className="text-muted-foreground text-sm" data-testid="awareness-unavailable">
					{t("integrity.awareness.unavailable")}
				</p>
			);
		}
		return <LoadError title={t("integrity.awareness.loadError")} error={q.error} onRetry={q.refetch} testId="awareness-error" />;
	}
	if (!q.data) return null;
	return <AwarenessPanel report={q.data} arms={arms} />;
}

/** The verdict page's monitor section body: certifications that replayed this experiment. */
export function ExperimentMonitorsSection({ expId }: { expId: string }) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("uf4a");
	const q = useListMonitorCertificationsQuery();
	if (q.isLoading) return <BlockSkeleton rows={2} testId="monitors-loading" />;
	if (q.isError && !absent(q.error)) {
		return <LoadError title={t("integrity.monitors.loadError")} error={q.error} onRetry={q.refetch} testId="monitors-error" />;
	}
	const certs = certificationsFor(q.data?.certifications ?? [], expId);
	return (
		<div className="flex flex-col gap-2">
			{certs.length === 0 ? (
				// M4-44: a plain empty state with the way to set one up; the command line sits in a disclosure.
				<div className="flex flex-col gap-2" data-testid="monitors-none">
					<EmptyState
						compact
						icon={ShieldCheck}
						title={tu("checks.monitorsEmptyTitle")}
						action={{ label: tu("checks.monitorsSetUp"), to: "/monitors" }}
						testId="monitors-empty"
					/>
					<details className="text-muted-foreground text-xs" data-testid="monitors-terminal">
						<summary className="w-fit cursor-pointer">{tu("checks.fromTerminal")}</summary>
						<p className="mt-1 font-mono break-all">{t("integrity.monitors.none", { exp: expId })}</p>
					</details>
				</div>
			) : (
				<MonitorCertList certs={certs} testId="verdict-monitor-certs" />
			)}
			<AllMonitorsLink />
		</div>
	);
}

/** The link to every safety monitor (/monitors), which no menu leads to (N77). */
function AllMonitorsLink() {
	const { t } = useTranslation("gw06");
	return (
		<Link to="/monitors" className="text-primary w-fit text-xs font-medium hover:underline" data-testid="monitors-all-link">
			{t("integrity.allMonitors")}
		</Link>
	);
}