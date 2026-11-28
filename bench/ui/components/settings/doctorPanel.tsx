import "./ufl8I18n";

import { Button } from "@/components/ui/button";
import { StatusPill, type StatusTone } from "@/components/ui/statusPill";
import { BlockSkeleton, InlineError } from "@/components/runs/screenStates";
import { useGetServerHealthQuery, useLazyGetDoctorQuery } from "@/lib/store/apis/doctorApi";
import type { Doctor, DoctorStatus } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";

const tones: Record<DoctorStatus, StatusTone> = { pass: "ok", fail: "bad", warn: "warn", skip: "neutral" };

/** The result of GET /doctor: one row per check with its state and what the server says to do. Exported for tests. */
export function DoctorResult({ result }: { result: Doctor }) {
	const { t } = useTranslation("ufl8");
	const failed = result.checks.filter((c) => c.status === "fail").length;
	return (
		<div className="flex min-w-0 flex-col gap-2" data-testid="doctor-result" data-ok={result.ok}>
			<p className="text-xs font-medium">{failed === 0 ? t("doctor.allPass") : t("doctor.failed", { count: failed })}</p>
			<ul className="flex min-w-0 flex-col gap-1.5">
				{result.checks.map((c, i) => (
					<li
						key={`${c.id}-${i}`}
						className="grid min-w-0 grid-cols-[5.5rem_minmax(0,1fr)] items-start gap-2 text-xs"
						data-testid="doctor-check"
						data-status={c.status}
					>
						<StatusPill tone={tones[c.status] ?? "neutral"}>{t(`doctor.status.${c.status}`, { defaultValue: c.status })}</StatusPill>
						<span className="min-w-0 break-words">
							<span className="font-medium">{c.id}</span>
							{c.detail && <span className="text-muted-foreground"> {c.detail}</span>}
						</span>
					</li>
				))}
			</ul>
		</div>
	);
}

/** "Check this server": GET /doctor on a self-hosted server; on the shared server, a line pointing at the org check job. */
export function DoctorPanel({ hosted }: { hosted: boolean }) {
	const { t } = useTranslation("ufl8");
	const [run, state] = useLazyGetDoctorQuery();
	const health = useGetServerHealthQuery();
	const version = health.data?.version;
	return (
		<div className="flex min-w-0 flex-col gap-2 border-t pt-3" data-testid="doctor-panel">
			<div className="flex flex-wrap items-center justify-between gap-2">
				<span className="text-sm font-medium">{t("doctor.title")}</span>
				{!hosted && (
					<Button size="sm" variant="outline" onClick={() => void run()} disabled={state.isFetching} data-testid="doctor-run">
						{state.data ? t("doctor.rerun") : t("doctor.run")}
					</Button>
				)}
			</div>
			{version && (
				<p className="text-muted-foreground text-xs" data-testid="server-version">
					{t("doctor.version", { version })}
				</p>
			)}
			{hosted ? (
				<p className="text-muted-foreground text-xs" data-testid="doctor-hosted">
					{t("doctor.hosted")}
				</p>
			) : state.isFetching ? (
				<BlockSkeleton rows={3} testId="doctor-loading" />
			) : state.isError ? (
				<InlineError error={state.error} title={t("doctor.error")} onRetry={() => void run()} testId="doctor-error" />
			) : state.data ? (
				<DoctorResult result={state.data} />
			) : (
				<p className="text-muted-foreground text-xs">{t("doctor.empty")}</p>
			)}
		</div>
	);
}