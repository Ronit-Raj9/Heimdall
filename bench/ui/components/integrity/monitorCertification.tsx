import { EntityLink } from "@/components/shell/entityLink";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CopyId } from "@/components/ui/copyId";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { TONE_CLASS, shortHash } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import { monitorReportUrl } from "@/lib/store/apis/integrityApi";
import type { MonitorCertification, MonitorCertificationSummary, OperatingPoint } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { FileText, ShieldCheck } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { formatFpr, formatInterval, formatRate, headlinePoint } from "./integrity.utils";

/** Replayed runs shown in the detail table; the JSON certificate carries all of them. */
export const MAX_ITEMS = 50;

/**
 * A catch rate as a bar on 0-100 % with its exact interval as a whisker, so a wide interval from a
 * small labelled set is as visible as the rate itself.
 */
export function RateBar({
	rate,
	interval,
	supported,
	testId,
}: {
	rate: number | null;
	interval?: [number, number];
	supported: boolean;
	testId?: string;
}) {
	const pct = (x: number) => `${Math.min(100, Math.max(0, x * 100))}%`;
	return (
		<div className="bg-chart-track relative h-2 w-full min-w-24 rounded-full" data-testid={testId} aria-hidden="true">
			<div
				className={cn("absolute inset-y-0 left-0 rounded-full", supported ? "bg-chart-info" : "bg-chart-warning")}
				style={{ width: pct(rate ?? 0) }}
			/>
			{interval && (
				<div
					className="border-foreground/60 absolute -top-1 -bottom-1 border-x"
					style={{ left: pct(interval[0]), width: `calc(${pct(interval[1])} - ${pct(interval[0])})` }}
				/>
			)}
		</div>
	);
}

/** Certification rows: the monitor, the labelled set and the headline catch rate. */
export function MonitorCertList({ certs, testId = "monitor-cert-list" }: { certs: MonitorCertificationSummary[]; testId?: string }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	return (
		<ul className="flex flex-col divide-y" data-testid={testId}>
			{certs.map((c) => {
				const h = c.headline;
				return (
					<li
						key={c.cert_id}
						className="grid gap-3 py-3 sm:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_auto] sm:items-center"
						data-testid={`monitor-cert-${c.cert_id}`}
					>
						<div className="flex min-w-0 flex-col gap-1">
							<span className="flex items-center gap-2 font-medium">
								<ShieldCheck className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
								{c.monitor_name}
								<span className="text-muted-foreground font-mono text-xs">
									{c.monitor_kind} · {c.mode}
								</span>
							</span>
							<span className="text-muted-foreground text-xs">
								{t("integrity.monitors.honest", { count: c.honest })} · {t("integrity.monitors.attacks", { count: c.attacks })} ·{" "}
								{new Date(c.created_at).toLocaleString(locale)}
							</span>
						</div>
						<div className="flex flex-col gap-1.5">
							{h ? (
								<>
									<span className="flex flex-wrap items-baseline gap-x-2 gap-y-1 text-sm">
										<span className="text-muted-foreground text-xs">
											{t("integrity.monitors.headline", { fpr: formatFpr(h.target_fpr, locale) })}
										</span>
										{h.catch_rate === null ? (
											<span className="text-muted-foreground text-xs" data-testid={`monitor-cert-noattacks-${c.cert_id}`}>
												{t("integrity.monitors.noAttacks")}
											</span>
										) : (
											<>
												<span className="font-semibold tabular-nums">{formatRate(h.catch_rate, locale)}</span>
												<span className="text-muted-foreground text-xs tabular-nums">{formatInterval(h.catch_interval, locale)}</span>
											</>
										)}
										{!h.supported && h.catch_rate !== null && (
											<Badge variant="outline" className={TONE_CLASS.warn}>
												{t("integrity.monitors.unsupported")}
											</Badge>
										)}
									</span>
									{h.catch_rate !== null && <RateBar rate={h.catch_rate} interval={h.catch_interval} supported={h.supported} />}
								</>
							) : (
								<span className="text-muted-foreground text-xs">-</span>
							)}
						</div>
						<Button variant="outline" size="sm" asChild>
							<Link to="/monitors" search={{ cert: c.cert_id } as never} data-testid={`monitor-cert-open-${c.cert_id}`}>
								{t("integrity.monitors.open")}
							</Link>
						</Button>
					</li>
				);
			})}
		</ul>
	);
}

/** One certificate: the monitor, the labelled set, every operating point, families and warnings. */
export function MonitorCertDetail({ cert }: { cert: MonitorCertification }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const head = headlinePoint(cert.operating_points);
	// Attacks first, then the highest scores, so a capped table never hides what the monitor missed or flagged.
	const items = [...(cert.items ?? [])].sort(
		(a, b) => Number(b.label === "attack") - Number(a.label === "attack") || (b.score ?? -1) - (a.score ?? -1),
	);

	return (
		<div className="flex flex-col gap-6" data-testid="monitor-cert-detail" data-cert-id={cert.cert_id}>
			<div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)]">
				<dl className="grid grid-cols-2 gap-x-6 gap-y-3 text-sm">
					<Fact label={t("integrity.monitors.monitor")}>{cert.monitor.name}</Fact>
					<Fact label={t("integrity.monitors.mode")}>
						<span className="font-mono text-xs">
							{cert.monitor.kind} · {cert.monitor.mode}
						</span>
					</Fact>
					<Fact label={t("integrity.monitors.configHash")}>
						<CopyId value={cert.monitor.config_hash} display={shortHash(cert.monitor.config_hash)} />
					</Fact>
					<Fact label={t("integrity.monitors.network")}>
						<span className="font-mono text-xs">{cert.monitor.network ?? "-"}</span>
					</Fact>
					<Fact label={t("integrity.monitors.experiments")}>
						<span className="flex flex-wrap gap-1">
							{cert.exp_ids.map((id) => (
								<EntityLink key={id} kind="experiment" id={id} className="text-xs" />
							))}
						</span>
					</Fact>
					<Fact label={t("integrity.monitors.labels")}>
						<span className="text-xs">
							{t("integrity.monitors.honest", { count: cert.labels.honest })} ·{" "}
							{t("integrity.monitors.attacks", { count: cert.labels.attack })} ·{" "}
							{t("integrity.monitors.excluded", { count: cert.labels.excluded })}
						</span>
					</Fact>
				</dl>
				{head && <Headline point={head} />}
			</div>
			<div className="flex flex-col gap-2 text-xs">
				<p className="text-muted-foreground">{cert.labels.rule}</p>
				{cert.labels.excluded_reasons && Object.keys(cert.labels.excluded_reasons).length > 0 && (
					<p className="text-muted-foreground" data-testid="monitor-cert-excluded">
						{t("integrity.monitors.excludedReasons")}{" "}
						{Object.entries(cert.labels.excluded_reasons)
							.sort((a, b) => b[1] - a[1])
							.map(([reason, n]) => `${reason} (${n})`)
							.join("; ")}
					</p>
				)}
				{cert.integrity && (
					<p className="flex flex-wrap items-center gap-2" data-testid="monitor-cert-integrity" data-state={cert.integrity.state}>
						<span className="text-muted-foreground">{t("integrity.monitors.injection")}</span>
						<Badge
							variant="outline"
							className={
								cert.integrity.state === "passed"
									? TONE_CLASS.good
									: cert.integrity.state === "failed"
										? TONE_CLASS.bad
										: TONE_CLASS.neutral
							}
						>
							{cert.integrity.state}
						</Badge>
						{cert.integrity.detail && <span className="text-muted-foreground">{cert.integrity.detail}</span>}
					</p>
				)}
			</div>

			{(cert.warnings?.length ?? 0) > 0 || (cert.errors ?? 0) > 0 ? (
				<section
					className={cn("flex flex-col gap-1 rounded-sm border px-3 py-2 text-sm", TONE_CLASS.warn)}
					data-testid="monitor-cert-warnings"
				>
					<h3 className="text-xs font-medium tracking-wide uppercase">{t("integrity.monitors.warnings")}</h3>
					<ul className="list-disc pl-5">
						{(cert.errors ?? 0) > 0 && <li>{t("integrity.monitors.errors", { count: cert.errors ?? 0 })}</li>}
						{cert.warnings?.map((w) => (
							<li key={w}>{w}</li>
						))}
					</ul>
				</section>
			) : null}

			<section className="flex flex-col gap-2">
				<h3 className="text-sm font-medium">{t("integrity.monitors.points")}</h3>
				<Table data-testid="monitor-cert-points">
					<TableHeader>
						<TableRow>
							<TableHead>{t("integrity.monitors.targetFpr")}</TableHead>
							<TableHead className="text-right">{t("integrity.monitors.threshold")}</TableHead>
							<TableHead className="text-right">{t("integrity.monitors.achievedFpr")}</TableHead>
							<TableHead className="text-right">{t("integrity.monitors.catchRate")}</TableHead>
							<TableHead className="w-40">{t("integrity.monitors.interval")}</TableHead>
							<TableHead>{t("integrity.monitors.supported")}</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{cert.operating_points.map((p) => (
							<TableRow key={p.target_fpr} data-testid={`monitor-point-${p.target_fpr}`}>
								<TableCell className="tabular-nums">{formatFpr(p.target_fpr, locale)}</TableCell>
								<TableCell className="text-right tabular-nums">{typeof p.threshold === "number" ? p.threshold.toFixed(3) : "-"}</TableCell>
								<TableCell className="text-right tabular-nums">
									{formatFpr(p.achieved_fpr, locale)}
									<span className="text-muted-foreground text-xs">
										{" "}
										({p.false_positives}/{p.honest})
									</span>
								</TableCell>
								<TableCell className="text-right font-semibold tabular-nums">
									{formatRate(p.catch_rate, locale)}
									<span className="text-muted-foreground text-xs font-normal">
										{" "}
										({p.caught}/{p.attacks})
									</span>
								</TableCell>
								<TableCell>
									<div className="flex flex-col gap-1">
										<RateBar rate={p.catch_rate} interval={p.catch_interval} supported={p.supported} />
										<span className="text-muted-foreground text-[11px] tabular-nums">{formatInterval(p.catch_interval, locale)}</span>
									</div>
								</TableCell>
								<TableCell>
									<Badge variant="outline" className={p.supported ? TONE_CLASS.good : TONE_CLASS.warn}>
										{p.supported ? t("integrity.monitors.yes") : t("integrity.monitors.unsupported")}
									</Badge>
									{p.note && <p className="text-muted-foreground mt-1 max-w-72 text-xs whitespace-normal">{p.note}</p>}
								</TableCell>
							</TableRow>
						))}
					</TableBody>
				</Table>
			</section>

			{cert.families.length > 0 && (
				<section className="flex flex-col gap-2">
					<h3 className="text-sm font-medium">{t("integrity.monitors.families")}</h3>
					<Table data-testid="monitor-cert-families">
						<TableHeader>
							<TableRow>
								<TableHead>{t("integrity.monitors.family")}</TableHead>
								<TableHead>{t("integrity.monitors.gates")}</TableHead>
								{cert.operating_points.map((p) => (
									<TableHead key={p.target_fpr} className="text-right">
										{t("integrity.monitors.headline", { fpr: formatFpr(p.target_fpr, locale) })}
									</TableHead>
								))}
							</TableRow>
						</TableHeader>
						<TableBody>
							{cert.families.map((f) => (
								<TableRow key={f.family}>
									<TableCell className="font-mono text-xs">{f.family}</TableCell>
									<TableCell className="font-mono text-xs">{f.gates?.join(", ") ?? "-"}</TableCell>
									{cert.operating_points.map((op) => {
										const fp = f.points.find((x) => x.target_fpr === op.target_fpr);
										return (
											<TableCell key={op.target_fpr} className="text-right tabular-nums">
												{fp ? (
													<>
														{formatRate(fp.catch_rate, locale)}
														<span className="text-muted-foreground text-xs">
															{" "}
															({fp.caught}/{fp.attacks}) {formatInterval(fp.catch_interval, locale)}
														</span>
													</>
												) : (
													"-"
												)}
											</TableCell>
										);
									})}
								</TableRow>
							))}
						</TableBody>
					</Table>
				</section>
			)}

			{items.length > 0 && (
				<section className="flex flex-col gap-2">
					<h3 className="text-sm font-medium">{t("integrity.monitors.items")}</h3>
					<Table data-testid="monitor-cert-items">
						<TableHeader>
							<TableRow>
								<TableHead>{t("integrity.monitors.run")}</TableHead>
								<TableHead>{t("integrity.monitors.label")}</TableHead>
								<TableHead>{t("integrity.monitors.family")}</TableHead>
								<TableHead className="text-right">{t("integrity.monitors.score")}</TableHead>
								<TableHead>{t("integrity.monitors.decision")}</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{items.slice(0, MAX_ITEMS).map((it) => (
								<TableRow key={`${it.exp_id}-${it.run_id}`}>
									<TableCell>
										<CopyId value={it.run_id} className="max-w-36" />
									</TableCell>
									<TableCell>
										<Badge variant="outline" className={it.label === "attack" ? TONE_CLASS.bad : TONE_CLASS.neutral}>
											{it.label}
										</Badge>
									</TableCell>
									<TableCell className="font-mono text-xs">{it.family ?? "-"}</TableCell>
									<TableCell className="text-right tabular-nums">{it.error || it.score === null ? "-" : it.score.toFixed(3)}</TableCell>
									<TableCell className={cn("font-mono text-xs", it.error && "text-chart-error-ink")}>
										{it.error ? it.error : it.decision || "-"}
									</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
					{items.length > MAX_ITEMS && (
						<p className="text-muted-foreground text-xs">{t("integrity.monitors.itemsMore", { count: items.length - MAX_ITEMS })}</p>
					)}
				</section>
			)}

			<div>
				<Button variant="outline" size="sm" asChild>
					<a href={monitorReportUrl(cert.cert_id)} download data-testid="monitor-cert-report">
						<FileText className="size-4" aria-hidden="true" />
						{t("integrity.monitors.report")}
					</a>
				</Button>
			</div>
		</div>
	);
}

function Headline({ point }: { point: OperatingPoint }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	return (
		<div className="bg-muted/40 flex flex-col gap-2 self-start rounded-md border p-4" data-testid="monitor-cert-headline">
			<span className="text-muted-foreground text-xs font-medium tracking-[0.06em] uppercase">
				{t("integrity.monitors.headline", { fpr: formatFpr(point.target_fpr, locale) })}
			</span>
			<span className="flex items-baseline gap-3">
				<span className="text-4xl leading-none font-bold tracking-[-0.02em] tabular-nums">{formatRate(point.catch_rate, locale)}</span>
				<span className="text-muted-foreground text-sm tabular-nums">
					{point.caught}/{point.attacks} {formatInterval(point.catch_interval, locale)}
				</span>
			</span>
			<RateBar rate={point.catch_rate} interval={point.catch_interval} supported={point.supported} />
			{!point.supported && <span className="text-chart-warning-ink text-xs">{point.note || t("integrity.monitors.unsupported")}</span>}
		</div>
	);
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
	return (
		<div className="flex min-w-0 flex-col gap-0.5">
			<dt className="text-muted-foreground text-xs">{label}</dt>
			<dd className="min-w-0">{children}</dd>
		</div>
	);
}