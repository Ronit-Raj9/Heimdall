import { RateBar } from "@/components/integrity/monitorCertification";
import { formatFpr, formatInterval, formatRate } from "@/components/integrity/integrity.utils";
import { getLocale } from "@/lib/i18n";
import type { MonitorCertificationSummary } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { ChevronRight, ShieldCheck, TriangleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";

/** Monitor certifications as cards: name, labelled set, and the headline catch rate as a bar. */
export function MonitorCards({ certs }: { certs: MonitorCertificationSummary[] }) {
	const { t } = useTranslation("u14");
	const locale = getLocale();
	return (
		<ul className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4" data-testid="monitor-cert-list">
			{certs.map((c) => {
				const h = c.headline;
				const warnings = c.warnings?.length ?? 0;
				return (
					<li key={c.cert_id} data-testid={`monitor-cert-${c.cert_id}`}>
						<Link
							to="/monitors"
							search={{ cert: c.cert_id } as never}
							className="bg-card group flex h-full flex-col gap-4 rounded-xl border p-5 transition hover:-translate-y-0.5 hover:shadow-md"
						>
							<div className="flex items-start justify-between gap-3">
								<div className="flex min-w-0 items-center gap-3">
									<span className="bg-primary/10 text-primary flex size-9 shrink-0 items-center justify-center rounded-lg">
										<ShieldCheck className="size-4.5" aria-hidden="true" />
									</span>
									<div className="min-w-0">
										<div className="truncate text-base font-semibold tracking-tight" title={c.monitor_name}>
											{c.monitor_name}
										</div>
										<div className="text-muted-foreground truncate text-xs">
											{c.monitor_kind} · {new Date(c.created_at).toLocaleDateString(locale)}
										</div>
									</div>
								</div>
								<ChevronRight className="text-muted-foreground group-hover:text-foreground mt-1 size-4 shrink-0 transition" aria-hidden="true" />
							</div>
							<div className="flex flex-col gap-2">
								<div className="flex items-baseline justify-between gap-2">
									<span className="text-muted-foreground text-xs" title={t("monitors.catchRateHint")}>
										{t("monitors.catchRate")}
										{h && ` ${t("monitors.falseAlarm", { fpr: formatFpr(h.target_fpr, locale) })}`}
									</span>
									{h && h.catch_rate !== null ? (
										<span className="text-2xl font-semibold tracking-tight tabular-nums" title={formatInterval(h.catch_interval, locale)}>
											{formatRate(h.catch_rate, locale)}
										</span>
									) : (
										<span className="text-muted-foreground text-sm" data-testid={`monitor-cert-noattacks-${c.cert_id}`}>
											{t("monitors.noAttacks")}
										</span>
									)}
								</div>
								{h && h.catch_rate !== null && <RateBar rate={h.catch_rate} interval={h.catch_interval} supported={h.supported} />}
							</div>
							<div className="text-muted-foreground mt-auto flex flex-wrap items-center justify-between gap-2 text-xs">
								<span>{t("monitors.labelled", { honest: c.honest, attacks: c.attacks })}</span>
								{warnings > 0 ? (
									<span className="bg-chart-warning/15 text-chart-warning-ink inline-flex items-center gap-1 rounded-full px-2 py-0.5">
										<TriangleAlert className="size-3" aria-hidden="true" />
										{t("monitors.warnings", { count: warnings })}
									</span>
								) : (
									<span className="sr-only">{t("monitors.title")}</span>
								)}
							</div>
						</Link>
					</li>
				);
			})}
		</ul>
	);
}
